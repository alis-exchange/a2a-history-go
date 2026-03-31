package service

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"cloud.google.com/go/iam/apiv1/iampb"
	"cloud.google.com/go/spanner"
	"github.com/google/uuid"
	pb "go.alis.build/common/alis/a2a/extension/history/v1"
	"go.alis.build/iam/v2"
	"go.alis.build/validation"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	threadRegex      = `^threads/[a-z0-9-]{2,50}$`
	roleOpen         = "roles/open"
	roleThreadViewer = "roles/thread.viewer"
	roleThreadAdmin  = "roles/thread.admin"
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
	db         *spanner.Client
	historyTbl string
	eventsTbl  string
	authorizer *iam.IAM
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
			},
			AllUsers: false,
		},
	})
	if err != nil {
		return nil, err
	}

	return &ThreadService{
		db:         db,
		historyTbl: config.ThreadsTable,
		eventsTbl:  config.EventsTable,
		authorizer: authorizer,
	}, nil
}

// GetThread implements the [Service.GetThread] method.
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

// ListThreads implements the [Service.ListThreads] method.
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
	if len(resources) < limit {
		nextPageToken = fmt.Sprintf("%d", offset+limit)
	}

	return &pb.ListThreadsResponse{
		Threads:       resources,
		NextPageToken: nextPageToken,
	}, nil
}

// ListThreadEvents implements the [Service.ListThreadEvents] method.
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

// AppendThreadEvent implements the [Service.AppendThreadEvent] method.
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
		if spanner.ErrCode(err) != codes.NotFound {
			return nil, err
		}
		now := time.Now().UTC()
		history := &pb.Thread{
			Name:        historyName,
			DisplayName: now.Format(time.RFC3339),
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

// readThread loads the Thread and Policy columns for a thread primary key, or returns the Spanner error
// (typically NotFound if the row does not exist).
func (s *ThreadService) readThread(ctx context.Context, name string) (*pb.Thread, *iampb.Policy, error) {
	row, err := s.db.Single().ReadRow(ctx, s.historyTbl, spanner.Key{name}, []string{"Thread", "Policy"})
	if err != nil {
		return nil, nil, err
	}
	history := &pb.Thread{}
	policy := &iampb.Policy{}

	if err := row.Columns(history, policy); err != nil {
		return nil, nil, err
	}
	return history, policy, nil
}
