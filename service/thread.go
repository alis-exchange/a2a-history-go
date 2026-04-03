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
	pb "go.alis.build/a2a/extension/history/alis/a2a/extension/history/v1"
	"go.alis.build/iam/v2"
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
	Project      string // GCP project id
	Instance     string // Spanner instance id
	Database     string // Spanner database id
	DatabaseRole string // optional Spanner database role for fine-grained access (empty if unused)
	ThreadsTable string // table storing Thread + IAM Policy proto columns
	EventsTable  string // table storing ThreadEvent proto rows (keys scoped under a thread)
}

// ThreadService is an implementation for managing Thread and ThreadEvents via Google Cloud Spanner.
type ThreadService struct {
	db           *spanner.Client
	historyTbl   string
	eventsTbl    string
	geminiClient *genai.Client
	authorizer   *iam.IAM
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
		db:           db,
		historyTbl:   config.ThreadsTable,
		eventsTbl:    config.EventsTable,
		geminiClient: geminiClient,
		authorizer:   authorizer,
	}, nil
}

// GetThread implements the [ThreadService.GetThread] method.
func (s *ThreadService) GetThread(ctx context.Context, req *pb.GetThreadRequest) (*pb.Thread, error) {
	// Authorize
	az, ctx, err := s.authorizer.NewAuthorizer(ctx)
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
	if !az.HasAccess(pb.ThreadService_GetThread_FullMethodName) {
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
	az, ctx, err := s.authorizer.NewAuthorizer(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to create authorizer: %s", err.Error())
	}

	_, policy, err := s.readThread(ctx, req.GetName())
	if err != nil {
		return nil, err
	}

	az.AddPolicy(policy)
	if err := az.AuthorizeRpc(); err != nil {
		return nil, err
	}

	eventsPrefix := req.GetName() + "/%"
	if _, err := s.db.ReadWriteTransaction(ctx, func(ctx context.Context, rwt *spanner.ReadWriteTransaction) error {
		if _, err := rwt.Update(ctx, spanner.Statement{
			SQL:    fmt.Sprintf(`DELETE FROM %s WHERE key LIKE @eventsPrefix`, s.eventsTbl),
			Params: map[string]any{"eventsPrefix": eventsPrefix},
		}); err != nil {
			return status.Errorf(codes.Internal, "deleting thread events for %q: %v", req.GetName(), err)
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

// ListThreads implements the [ThreadService.ListThreads] method.
func (s *ThreadService) ListThreads(ctx context.Context, req *pb.ListThreadsRequest) (*pb.ListThreadsResponse, error) {
	// Authorize
	az, ctx, err := s.authorizer.NewAuthorizer(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to create authorizer: %s", err.Error())
	}

	if err = az.AuthorizeRpc(); err != nil {
		return nil, err
	}

	// Prepare query statement
	statement := spanner.NewStatement(`select Thread from ` + s.historyTbl + " as t")
	if !az.Identity.IsDeploymentServiceAccount() {
		statement.SQL += `
			WHERE EXISTS (
			SELECT 1
			FROM UNNEST(t.Policy.bindings) AS binding
			CROSS JOIN UNNEST(binding.members) AS member
			WHERE member = @member
			)`
		statement.Params["member"] = az.Identity.PolicyMember()
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
	var resources []*pb.Thread
	iterator := s.db.ReadOnlyTransaction().Query(ctx, statement)
	if err := iterator.Do(func(r *spanner.Row) error {
		history := &pb.Thread{}
		if err := r.Columns(history); err != nil {
			return err
		}
		resources = append(resources, history)
		return nil
	}); err != nil {
		return nil, status.Errorf(codes.Internal, "querying database: %v", err)
	}

	// determine next page token
	nextPageToken := ""
	if len(resources) == limit {
		nextPageToken = fmt.Sprintf("%d", offset+limit)
	}

	return &pb.ListThreadsResponse{
		Threads:       resources,
		NextPageToken: nextPageToken,
	}, nil
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
	az, ctx, err := s.authorizer.NewAuthorizer(ctx)
	if err != nil {
		return nil, err
	}
	_, policy, err := s.readThread(ctx, req.GetParent())
	if err != nil {
		return nil, err
	}
	az.AddPolicy(policy)
	if err := az.AuthorizeRpc(); err != nil {
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

// AppendThreadEvent implements the [ThreadService.AppendThreadEvent] method.
func (s *ThreadService) AppendThreadEvent(ctx context.Context, req *pb.AppendThreadEventRequest) (*pb.AppendThreadEventResponse, error) {
	// Validation
	validator := validation.NewValidator()
	validator.MessageIsPopulated("event", req.GetEvent() != nil)
	if err := validator.Validate(); err != nil {
		return nil, err
	}

	// Authorize
	az, ctx, err := s.authorizer.NewAuthorizer(ctx)
	if err != nil {
		return nil, err
	}
	if err = az.AuthorizeRpc(); err != nil {
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
		return nil, err
	}

	historyName := fmt.Sprintf("threads/%s", ctxID)
	req.GetEvent().Name = fmt.Sprintf("%s/events/%s", historyName, uuid.NewString())

	if req.GetEvent().GetCreateTime() == nil {
		req.GetEvent().CreateTime = timestamppb.Now()
	}

	// insert Thread resource if missing
	var mutations []*spanner.Mutation
	if _, _, err := s.readThread(ctx, historyName); err != nil {
		if status.Code(err) != codes.NotFound {
			return nil, err
		}
		now := time.Now().UTC()
		history := &pb.Thread{
			Name:        historyName,
			DisplayName: s.generateThreadDisplayName(ctx, req.GetEvent(), now),
			AgentId:     req.GetAgentId(),
			CreateTime:  timestamppb.Now(),
		}
		policy := &iampb.Policy{
			Bindings: []*iampb.Binding{
				{
					Role:    roleThreadAdmin,
					Members: []string{az.Identity.PolicyMember()},
				},
			},
		}
		mutation := spanner.Insert(s.historyTbl, []string{"key", "Thread", "Policy"}, []any{history.GetName(), history, policy})
		mutations = append(mutations, mutation)

	}

	mutation := spanner.Insert(s.eventsTbl, []string{"key", "Event"}, []any{req.GetEvent().GetName(), req.GetEvent()})
	mutations = append(mutations, mutation)

	// Apply mutations in a single transaction
	if _, err := s.db.ReadWriteTransaction(ctx, func(ctx context.Context, rwt *spanner.ReadWriteTransaction) error {
		if err := rwt.BufferWrite(mutations); err != nil {
			return err
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
	history := &pb.Thread{}
	policy := &iampb.Policy{}

	if err := row.Columns(history, policy); err != nil {
		return nil, nil, status.Errorf(codes.Internal, "decoding thread %q: %v", name, err)
	}
	return history, policy, nil
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
