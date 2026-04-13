package service

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"cloud.google.com/go/iam/apiv1/iampb"
	"cloud.google.com/go/spanner"
	"github.com/google/uuid"
	pb "go.alis.build/common/alis/a2a/extension/history/v1"
	"go.alis.build/iam/v3"
	"go.alis.build/validation"
	"google.golang.org/genai"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	threadRegex      = `^threads/[a-z0-9-]{2,50}$`
	roleOpen         = "roles/open"
	roleThreadViewer = "roles/thread.viewer"
	roleThreadAdmin  = "roles/thread.admin"
	titleModel       = "gemini-2.5-flash-lite"
	titleLocation    = "global"
)

// SpannerStoreConfig selects the Spanner database and table names used by [ThreadService].
type SpannerStoreConfig struct {
	Project               string // GCP project id
	Instance              string // Spanner instance id
	Database              string // Spanner database id
	DatabaseRole          string // optional Spanner database role for fine-grained access (empty if unused)
	ThreadsTable          string // table storing Thread + IAM Policy proto columns
	EventsTable           string // table storing ThreadEvent proto rows (keys scoped under a thread)
	UserThreadStatesTable string // table storing UserThreadState proto rows keyed by thread/user
}

// ThreadService is an implementation for managing Thread and ThreadEvents via Google Cloud Spanner.
type ThreadService struct {
	db            *spanner.Client
	historyTbl    string
	eventsTbl     string
	userStatesTbl string
	geminiClient  *genai.Client
	authorizer    *iam.IAM
	pb.UnimplementedThreadServiceServer
}

// NewThreadService constructs a [ThreadService] with a Spanner client and IAM authorizer wired to
// the ThreadService RPC names used by this module.
func NewThreadService(ctx context.Context, config *SpannerStoreConfig) (*ThreadService, error) {
	dbName := fmt.Sprintf("projects/%s/instances/%s/databases/%s", config.Project, config.Instance, config.Database)

	db, err := spanner.NewClientWithConfig(ctx, dbName, spanner.ClientConfig{
		DisableNativeMetrics: true,
		DatabaseRole:         config.DatabaseRole,
	})
	if err != nil {
		return nil, err
	}

	authorizer, err := iam.New([]*iam.Role{
		{
			Name: roleOpen,
			Permissions: []string{
				pb.ThreadService_ListThreads_FullMethodName,
				pb.ThreadService_AppendThreadEvent_FullMethodName,
				pb.ThreadService_GetUserThreadState_FullMethodName,
				pb.ThreadService_UpdateUserThreadState_FullMethodName,
			},
			AllUsers: true,
		},
		{
			Name: roleThreadViewer,
			Permissions: []string{
				pb.ThreadService_GetThread_FullMethodName,
			},
			AllUsers: false,
		},
		{
			Name: roleThreadAdmin,
			Permissions: []string{
				pb.ThreadService_GetThread_FullMethodName,
				pb.ThreadService_ListThreadEvents_FullMethodName,
				pb.ThreadService_DeleteThread_FullMethodName,
			},
			AllUsers: false,
		},
	})
	if err != nil {
		return nil, err
	}

	var geminiClient *genai.Client
	projectID := strings.TrimSpace(os.Getenv("ALIS_OS_PROJECT"))
	if projectID == "" {
		projectID = strings.TrimSpace(config.Project)
	}
	if projectID != "" {
		geminiClient, err = genai.NewClient(ctx, &genai.ClientConfig{
			Backend:  genai.BackendVertexAI,
			Project:  projectID,
			Location: titleLocation,
		})
		if err != nil {
			return nil, err
		}
	}

	return &ThreadService{
		db:            db,
		historyTbl:    config.ThreadsTable,
		eventsTbl:     config.EventsTable,
		userStatesTbl: config.UserThreadStatesTable,
		geminiClient:  geminiClient,
		authorizer:    authorizer,
	}, nil
}

// GetThread implements the [ThreadService.GetThread] method.
func (s *ThreadService) GetThread(ctx context.Context, req *pb.GetThreadRequest) (*pb.Thread, error) {
	// Authorize
	az, err := s.authorizer.NewAuthorizer(ctx, pb.ThreadService_GetThread_FullMethodName)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to create authorizer: %s", err.Error())
	}

	// Validation
	validator := validation.NewValidator()
	validator.String("name", req.GetName()).IsPopulated().Matches(threadRegex)
	if err := validator.Validate(); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	// Read the resource from the database
	history, policy, err := s.readThread(ctx, req.GetName())
	if err != nil {
		return nil, err
	}

	// Check if the requester has access to this resource
	az.AddPolicy(policy)
	if !az.HasAccess() {
		return nil, status.Errorf(codes.PermissionDenied, "you do not have permission to access this resource")
	}

	return history, nil
}

// DeleteThread implements the [ThreadService.DeleteThread] method.
func (s *ThreadService) DeleteThread(ctx context.Context, req *pb.DeleteThreadRequest) (*emptypb.Empty, error) {
	// Validate
	validator := validation.NewValidator()
	validator.String("name", req.GetName()).IsPopulated().Matches(threadRegex)
	if err := validator.Validate(); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	// Authorize
	az, err := s.authorizer.NewAuthorizer(ctx, pb.ThreadService_DeleteThread_FullMethodName)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to create authorizer: %s", err.Error())
	}

	_, policy, err := s.readThread(ctx, req.GetName())
	if err != nil {
		return nil, err
	}

	az.AddPolicy(policy)
	if err := az.Require(); err != nil {
		return nil, err
	}

	eventsPrefix := req.GetName() + "/%"
	userStatesPrefix := req.GetName() + "/userStates/%"
	if _, err := s.db.ReadWriteTransaction(ctx, func(ctx context.Context, rwt *spanner.ReadWriteTransaction) error {
		if _, err := rwt.Update(ctx, spanner.Statement{
			SQL:    fmt.Sprintf(`DELETE FROM %s WHERE key LIKE @eventsPrefix`, s.eventsTbl),
			Params: map[string]any{"eventsPrefix": eventsPrefix},
		}); err != nil {
			return status.Errorf(codes.Internal, "deleting thread events for %q: %v", req.GetName(), err)
		}
		if s.userStatesTbl != "" {
			if _, err := rwt.Update(ctx, spanner.Statement{
				SQL:    fmt.Sprintf(`DELETE FROM %s WHERE key LIKE @userStatesPrefix`, s.userStatesTbl),
				Params: map[string]any{"userStatesPrefix": userStatesPrefix},
			}); err != nil {
				return status.Errorf(codes.Internal, "deleting user thread state for %q: %v", req.GetName(), err)
			}
		}
		if _, err := rwt.Update(ctx, spanner.Statement{
			SQL:    fmt.Sprintf(`DELETE FROM %s WHERE key = @name`, s.historyTbl),
			Params: map[string]any{"name": req.GetName()},
		}); err != nil {
			return status.Errorf(codes.Internal, "deleting thread %q: %v", req.GetName(), err)
		}
		return nil
	}); err != nil {
		return nil, err
	}

	return &emptypb.Empty{}, nil
}

// ListThreads implements the [ThreadService.ListThreads] method and projects caller user-thread
// state onto returned thread views.
func (s *ThreadService) ListThreads(ctx context.Context, req *pb.ListThreadsRequest) (*pb.ListThreadsResponse, error) {
	// Authorize
	az, err := s.authorizer.NewAuthorizer(ctx, pb.ThreadService_ListThreads_FullMethodName)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to create authorizer: %s", err.Error())
	}

	if err = az.Require(); err != nil {
		return nil, err
	}

	// Prepare query statement
	statement := spanner.NewStatement(`select Thread, Policy from ` + s.historyTbl + " as t")
	if !az.Caller().IsDeploymentServiceAccount() {
		statement.SQL += `
			WHERE EXISTS (
			SELECT 1
			FROM UNNEST(t.Policy.bindings) AS binding
			CROSS JOIN UNNEST(binding.members) AS member
			WHERE member = @member
			)`
		statement.Params["member"] = az.Caller().PolicyMember()
	}
	statement.SQL += ` order by t.create_time DESC limit @limit offset @offset;`

	// set query parameters
	limit := int(req.GetPageSize())
	if limit < 1 || limit > 100 {
		limit = 100
	}
	statement.Params["limit"] = limit
	offset := 0
	if req.GetPageToken() != "" {
		offset, err = strconv.Atoi(req.GetPageToken())
		if err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "invalid page token")
		}
	}
	statement.Params["offset"] = offset

	// make db hit and build up results
	var threads []*pb.Thread
	iterator := s.db.ReadOnlyTransaction().Query(ctx, statement)
	if err := iterator.Do(func(r *spanner.Row) error {
		thread, _, err := decodeThreadRow(r)
		if err != nil {
			return err
		}
		threads = append(threads, thread)
		return nil
	}); err != nil {
		return nil, status.Errorf(codes.Internal, "querying database: %v", err)
	}

	userStates, err := s.readUserThreadStates(ctx, currentUserResource(az.Caller()), threads)
	if err != nil {
		return nil, err
	}

	// determine next page token
	nextPageToken := ""
	if len(threads) == limit {
		nextPageToken = fmt.Sprintf("%d", offset+limit)
	}

	views := make([]*pb.ThreadView, 0, len(threads))
	for _, thread := range threads {
		state := userStates[thread.GetName()]
		view := &pb.ThreadView{
			Thread: thread,
		}
		if state != nil {
			view.ReadSequence = state.GetReadSequence()
			view.Pinned = state.GetPinned()
			view.PinnedTime = state.GetPinnedTime()
		}
		view.HasUnread = thread.GetLatestSequence() > view.GetReadSequence()
		views = append(views, view)
	}

	return &pb.ListThreadsResponse{
		Threads:       views,
		NextPageToken: nextPageToken,
	}, nil
}

// GetUserThreadState implements the [ThreadService.GetUserThreadState] method.
func (s *ThreadService) GetUserThreadState(ctx context.Context, req *pb.GetUserThreadStateRequest) (*pb.UserThreadState, error) {
	az, err := s.authorizer.NewAuthorizer(ctx, pb.ThreadService_GetUserThreadState_FullMethodName)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to create authorizer: %s", err.Error())
	}
	if err = az.Require(); err != nil {
		return nil, err
	}

	threadName, userID, err := parseUserThreadStateName(req.GetName())
	if err != nil {
		return nil, err
	}
	userResource, err := requireCurrentUserResource(az.Caller())
	if err != nil {
		return nil, err
	}
	if userID != userIDFromResource(userResource) {
		return nil, status.Error(codes.PermissionDenied, "you may only access your own thread state")
	}

	_, policy, err := s.readThread(ctx, threadName)
	if err != nil {
		return nil, err
	}
	threadAz, err := s.authorizer.NewAuthorizer(ctx, pb.ThreadService_ListThreads_FullMethodName)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to create authorizer: %s", err.Error())
	}
	threadAz.AddPolicy(policy)
	if !threadAz.HasAccess() {
		return nil, status.Error(codes.PermissionDenied, "you do not have permission to access this thread")
	}

	state, err := s.readUserThreadState(ctx, req.GetName())
	if err != nil {
		return nil, err
	}
	return state, nil
}

// UpdateUserThreadState implements the [ThreadService.UpdateUserThreadState] method.
func (s *ThreadService) UpdateUserThreadState(ctx context.Context, req *pb.UpdateUserThreadStateRequest) (*pb.UserThreadState, error) {
	validator := validation.NewValidator()
	validator.MessageIsPopulated("user_thread_state", req.GetUserThreadState() != nil)
	if err := validator.Validate(); err != nil {
		return nil, err
	}

	az, err := s.authorizer.NewAuthorizer(ctx, pb.ThreadService_UpdateUserThreadState_FullMethodName)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to create authorizer: %s", err.Error())
	}
	if err = az.Require(); err != nil {
		return nil, err
	}

	userResource, err := requireCurrentUserResource(az.Caller())
	if err != nil {
		return nil, err
	}
	threadName := req.GetUserThreadState().GetThread()
	if threadName == "" {
		parsedThread, _, parseErr := parseUserThreadStateName(req.GetUserThreadState().GetName())
		if parseErr != nil {
			return nil, status.Error(codes.InvalidArgument, "user_thread_state.thread is required")
		}
		threadName = parsedThread
	}
	if !validThreadName(threadName) {
		return nil, status.Error(codes.InvalidArgument, "invalid user_thread_state.thread")
	}
	if req.GetUserThreadState().GetUser() != "" && req.GetUserThreadState().GetUser() != userResource {
		return nil, status.Error(codes.PermissionDenied, "you may only update your own thread state")
	}
	stateName := req.GetUserThreadState().GetName()
	if stateName == "" {
		stateName = userThreadStateName(threadName, userResource)
	}
	parsedThread, userID, err := parseUserThreadStateName(stateName)
	if err != nil {
		return nil, err
	}
	if parsedThread != threadName || userID != userIDFromResource(userResource) {
		return nil, status.Error(codes.PermissionDenied, "user_thread_state.name must match the current user and thread")
	}

	thread, policy, err := s.readThread(ctx, threadName)
	if err != nil {
		return nil, err
	}
	threadAz, err := s.authorizer.NewAuthorizer(ctx, pb.ThreadService_ListThreads_FullMethodName)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to create authorizer: %s", err.Error())
	}
	threadAz.AddPolicy(policy)
	if !threadAz.HasAccess() {
		return nil, status.Error(codes.PermissionDenied, "you do not have permission to access this thread")
	}

	now := timestamppb.Now()
	state := &pb.UserThreadState{
		Name:       stateName,
		Thread:     threadName,
		User:       userResource,
		UpdateTime: now,
	}
	existing, err := s.readUserThreadState(ctx, stateName)
	if err != nil && status.Code(err) != codes.NotFound {
		return nil, err
	}
	if existing != nil {
		state.ReadSequence = existing.GetReadSequence()
		state.Pinned = existing.GetPinned()
		state.PinnedTime = existing.GetPinnedTime()
	}

	updatePaths := req.GetUpdateMask().GetPaths()
	if len(updatePaths) == 0 {
		updatePaths = []string{"read_sequence", "pinned", "pinned_time"}
	}
	for _, path := range updatePaths {
		switch path {
		case "read_sequence":
			if req.GetUserThreadState().GetReadSequence() < 0 {
				return nil, status.Error(codes.InvalidArgument, "read_sequence must be non-negative")
			}
			if req.GetUserThreadState().GetReadSequence() > thread.GetLatestSequence() {
				return nil, status.Error(codes.InvalidArgument, "read_sequence cannot exceed latest_sequence")
			}
			state.ReadSequence = req.GetUserThreadState().GetReadSequence()
		case "pinned":
			state.Pinned = req.GetUserThreadState().GetPinned()
			if !state.GetPinned() {
				state.PinnedTime = nil
			} else if state.GetPinnedTime() == nil {
				state.PinnedTime = now
			}
		case "pinned_time":
			state.PinnedTime = req.GetUserThreadState().GetPinnedTime()
		default:
			return nil, status.Errorf(codes.InvalidArgument, "unsupported update_mask path %q", path)
		}
	}
	if state.GetPinned() && state.GetPinnedTime() == nil {
		state.PinnedTime = now
	}
	if !state.GetPinned() {
		state.PinnedTime = nil
	}

	mutation := spanner.InsertOrUpdate(s.userStatesTbl, []string{"key", "UserThreadState"}, []any{state.GetName(), state})
	if _, err := s.db.Apply(ctx, []*spanner.Mutation{mutation}); err != nil {
		return nil, status.Errorf(codes.Internal, "writing user thread state %q: %v", state.GetName(), err)
	}
	return state, nil
}

// ListThreadEvents implements the [ThreadService.ListThreadEvents] method.
func (s *ThreadService) ListThreadEvents(ctx context.Context, req *pb.ListThreadEventsRequest) (*pb.ListThreadEventsResponse, error) {
	// Validate
	validator := validation.NewValidator()
	validator.String("parent", req.GetParent()).IsPopulated().Matches(threadRegex)
	if err := validator.Validate(); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	// Authorize
	az, err := s.authorizer.NewAuthorizer(ctx, pb.ThreadService_ListThreadEvents_FullMethodName)
	if err != nil {
		return nil, err
	}
	_, policy, err := s.readThread(ctx, req.GetParent())
	if err != nil {
		return nil, err
	}
	az.AddPolicy(policy)
	if err := az.Require(); err != nil {
		return nil, err
	}

	// build query
	statement := spanner.NewStatement(fmt.Sprintf(`SELECT Event FROM %s WHERE key LIKE(@thread) ORDER BY create_time DESC LIMIT @limit OFFSET @offset`, s.eventsTbl))
	statement.Params["thread"] = req.GetParent() + "/%"
	limit := int(req.GetPageSize())
	if limit < 1 || limit > 100 {
		limit = 100
	}
	statement.Params["limit"] = limit
	offset := 0
	if req.GetPageToken() != "" {
		var err error
		offset, err = strconv.Atoi(req.GetPageToken())
		if err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "invalid page token")
		}
	}
	statement.Params["offset"] = offset

	var events []*pb.ThreadEvent
	iterator := s.db.ReadOnlyTransaction().Query(ctx, statement)
	err = iterator.Do(func(r *spanner.Row) error {
		event := &pb.ThreadEvent{}
		if err := r.ColumnByName("Event", event); err != nil {
			return err
		}
		events = append(events, event)
		return nil
	})
	if err != nil {
		return nil, status.Errorf(codes.Internal, "querying events: %v", err)
	}

	nextPageToken := ""
	if len(events) == limit {
		nextPageToken = strconv.Itoa(offset + limit)
	}

	return &pb.ListThreadEventsResponse{
		Events:        events,
		NextPageToken: nextPageToken,
	}, nil
}

// AppendThreadEvent implements the [ThreadService.AppendThreadEvent] method. It assigns a unique
// monotonic event sequence within the thread and updates the thread's sequence counters in the same
// transaction as the event insert.
func (s *ThreadService) AppendThreadEvent(ctx context.Context, req *pb.AppendThreadEventRequest) (*pb.AppendThreadEventResponse, error) {
	// Validation
	validator := validation.NewValidator()
	validator.MessageIsPopulated("event", req.GetEvent() != nil)
	if err := validator.Validate(); err != nil {
		return nil, err
	}

	// Authorize
	az, err := s.authorizer.NewAuthorizer(ctx, pb.ThreadService_AppendThreadEvent_FullMethodName)
	if err != nil {
		return nil, err
	}
	if err = az.Require(); err != nil {
		return nil, err
	}

	var ctxID string
	switch req.GetEvent().GetPayload().(type) {
	case *pb.ThreadEvent_ArtifactUpdate:
		ctxID = req.GetEvent().GetArtifactUpdate().GetContextId()
	case *pb.ThreadEvent_Message:
		ctxID = req.GetEvent().GetMessage().GetContextId()
	case *pb.ThreadEvent_StatusUpdate:
		ctxID = req.GetEvent().GetStatusUpdate().GetContextId()
	case *pb.ThreadEvent_Task:
		ctxID = req.GetEvent().GetTask().GetContextId()
	}
	if ctxID == "" {
		return nil, status.Error(codes.InvalidArgument, "event payload must include a context_id")
	}

	historyName := fmt.Sprintf("threads/%s", ctxID)
	req.GetEvent().Name = fmt.Sprintf("%s/events/%s", historyName, uuid.NewString())

	if req.GetEvent().GetCreateTime() == nil {
		req.GetEvent().CreateTime = timestamppb.Now()
	}
	now := time.Now().UTC()
	threadCreateTime := timestamppb.New(now)
	threadDisplayName := s.generateThreadDisplayName(ctx, req.GetEvent(), now)

	// Apply thread read/write state and event insert in a single transaction.
	if _, err := s.db.ReadWriteTransaction(ctx, func(ctx context.Context, rwt *spanner.ReadWriteTransaction) error {
		thread := &pb.Thread{}
		policy := &iampb.Policy{}
		threadExists := true

		row, err := rwt.ReadRow(ctx, s.historyTbl, spanner.Key{historyName}, []string{"Thread", "Policy"})
		if err != nil {
			if spanner.ErrCode(err) != codes.NotFound {
				return status.Errorf(codes.Internal, "reading thread %q: %v", historyName, err)
			}
			threadExists = false
			thread = &pb.Thread{
				Name:           historyName,
				DisplayName:    threadDisplayName,
				AgentId:        req.GetAgentId(),
				CreateTime:     threadCreateTime,
				NextSequence:   1,
				LatestSequence: 0,
			}
			policy = &iampb.Policy{
				Bindings: []*iampb.Binding{
					{
						Role:    roleThreadAdmin,
						Members: []string{az.Caller().PolicyMember()},
					},
				},
			}
		} else if err := row.Columns(thread, policy); err != nil {
			return status.Errorf(codes.Internal, "decoding thread %q: %v", historyName, err)
		}

		sequence := thread.GetNextSequence()
		if sequence < 1 {
			sequence = max(thread.GetLatestSequence()+1, 1)
		}
		req.GetEvent().Sequence = sequence
		thread.NextSequence = sequence + 1
		thread.LatestSequence = sequence

		mutations := []*spanner.Mutation{
			spanner.Insert(s.eventsTbl, []string{"key", "Event"}, []any{req.GetEvent().GetName(), req.GetEvent()}),
		}
		if threadExists {
			mutations = append(mutations, spanner.Update(s.historyTbl, []string{"key", "Thread"}, []any{thread.GetName(), thread}))
		} else {
			mutations = append(mutations, spanner.Insert(s.historyTbl, []string{"key", "Thread", "Policy"}, []any{thread.GetName(), thread, policy}))
		}
		if err := rwt.BufferWrite(mutations); err != nil {
			return status.Errorf(codes.Internal, "writing thread/event mutations: %v", err)
		}
		return nil
	}); err != nil {
		return nil, err
	}

	return &pb.AppendThreadEventResponse{}, nil
}

// readThread loads the Thread and Policy columns for a thread primary key.
func (s *ThreadService) readThread(ctx context.Context, name string) (*pb.Thread, *iampb.Policy, error) {
	row, err := s.db.Single().ReadRow(ctx, s.historyTbl, spanner.Key{name}, []string{"Thread", "Policy"})
	if err != nil {
		if spanner.ErrCode(err) == codes.NotFound {
			return nil, nil, status.Errorf(codes.NotFound, "thread %q not found", name)
		}
		return nil, nil, status.Errorf(codes.Internal, "reading thread %q: %v", name, err)
	}
	thread, policy, err := decodeThreadRow(row)
	if err != nil {
		return nil, nil, status.Errorf(codes.Internal, "decoding thread %q: %v", name, err)
	}
	return thread, policy, nil
}

// decodeThreadRow decodes a threads-table row containing Thread and Policy proto columns.
func decodeThreadRow(row *spanner.Row) (*pb.Thread, *iampb.Policy, error) {
	thread := &pb.Thread{}
	policy := &iampb.Policy{}
	if err := row.Columns(thread, policy); err != nil {
		return nil, nil, err
	}
	return thread, policy, nil
}

func (s *ThreadService) readUserThreadStates(ctx context.Context, userResource string, threads []*pb.Thread) (map[string]*pb.UserThreadState, error) {
	states := map[string]*pb.UserThreadState{}
	if s.userStatesTbl == "" || userResource == "" || len(threads) == 0 {
		return states, nil
	}

	keys := make([]string, 0, len(threads))
	for _, thread := range threads {
		keys = append(keys, userThreadStateName(thread.GetName(), userResource))
	}
	statement := spanner.NewStatement(fmt.Sprintf(`SELECT UserThreadState FROM %s WHERE key IN UNNEST(@keys)`, s.userStatesTbl))
	statement.Params["keys"] = keys

	iterator := s.db.ReadOnlyTransaction().Query(ctx, statement)
	if err := iterator.Do(func(r *spanner.Row) error {
		state := &pb.UserThreadState{}
		if err := r.ColumnByName("UserThreadState", state); err != nil {
			return err
		}
		states[state.GetThread()] = state
		return nil
	}); err != nil {
		return nil, status.Errorf(codes.Internal, "querying user thread states: %v", err)
	}
	return states, nil
}

func (s *ThreadService) readUserThreadState(ctx context.Context, name string) (*pb.UserThreadState, error) {
	row, err := s.db.Single().ReadRow(ctx, s.userStatesTbl, spanner.Key{name}, []string{"UserThreadState"})
	if err != nil {
		if spanner.ErrCode(err) == codes.NotFound {
			return nil, status.Errorf(codes.NotFound, "user thread state %q not found", name)
		}
		return nil, status.Errorf(codes.Internal, "reading user thread state %q: %v", name, err)
	}
	state := &pb.UserThreadState{}
	if err := row.ColumnByName("UserThreadState", state); err != nil {
		return nil, status.Errorf(codes.Internal, "decoding user thread state %q: %v", name, err)
	}
	return state, nil
}

func parseUserThreadStateName(name string) (threadName string, userID string, err error) {
	if name == "" {
		return "", "", status.Error(codes.InvalidArgument, "name is required")
	}
	parts := strings.Split(name, "/")
	if len(parts) != 4 || parts[0] != "threads" || parts[2] != "userStates" || parts[1] == "" || parts[3] == "" {
		return "", "", status.Errorf(codes.InvalidArgument, "invalid user thread state name %q", name)
	}
	return strings.Join(parts[:2], "/"), parts[3], nil
}

func userThreadStateName(threadName, userResource string) string {
	return fmt.Sprintf("%s/userStates/%s", threadName, userIDFromResource(userResource))
}

func userIDFromResource(userResource string) string {
	return strings.TrimPrefix(userResource, "users/")
}

func currentUserResource(identity *iam.Identity) string {
	if identity == nil || identity.IsDeploymentServiceAccount() {
		return ""
	}
	if identity.ID() != "" {
		return "users/" + identity.ID()
	}
	return ""
}

func requireCurrentUserResource(identity *iam.Identity) (string, error) {
	userResource := currentUserResource(identity)
	if userResource == "" {
		return "", status.Error(codes.Unauthenticated, "an authenticated user is required")
	}
	return userResource, nil
}

func validThreadName(name string) bool {
	validator := validation.NewValidator()
	validator.String("name", name).IsPopulated().Matches(threadRegex)
	return validator.Validate() == nil
}

// generateThreadDisplayName derives a user-facing thread title from the initial message via Gemini,
// falling back to the timestamp string when no prompt text is available or generation fails.
func (s *ThreadService) generateThreadDisplayName(ctx context.Context, event *pb.ThreadEvent, now time.Time) string {
	fallback := now.Format(time.RFC3339)
	promptText := extractTitlePrompt(event)
	if promptText == "" {
		return fallback
	}
	if s.geminiClient == nil {
		return fallback
	}

	prompt := fmt.Sprintf(`You create short conversation titles.
Return a concise title of at most 8 words.
Do not use quotes or punctuation unless necessary.
User message:
%s`, promptText)

	resp, err := s.geminiClient.Models.GenerateContent(ctx, titleModel, genai.Text(prompt), &genai.GenerateContentConfig{})
	if err != nil {
		return fallback
	}

	title := strings.TrimSpace(resp.Text())
	title = strings.Trim(title, `"'`)
	if title == "" {
		return fallback
	}
	return title
}

// extractTitlePrompt returns a compact text prompt from the event's message parts for title generation.
func extractTitlePrompt(event *pb.ThreadEvent) string {
	if event == nil || event.GetMessage() == nil {
		return ""
	}

	parts := event.GetMessage().GetParts()
	textParts := make([]string, 0, len(parts))
	for _, part := range parts {
		text := strings.TrimSpace(part.GetText())
		if text == "" {
			continue
		}
		textParts = append(textParts, text)
		if len(strings.Join(textParts, " ")) >= 500 {
			break
		}
	}

	prompt := strings.TrimSpace(strings.Join(textParts, " "))
	if len(prompt) > 500 {
		prompt = strings.TrimSpace(prompt[:500])
	}
	return prompt
}
