package service

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"cloud.google.com/go/iam/apiv1/iampb"
	"cloud.google.com/go/spanner"
	"github.com/alis-exchange/go-alis-build/iam/v2"
	"github.com/google/uuid"
	v1 "go.alis.build/a2a/extension/history/alis/a2a/extension/history/v1"
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

type SpannerStoreConfig struct {
	Project      string
	Instance     string
	Database     string
	DatabaseRole string
	ThreadsTable string
	EventsTable  string
}

var _ Service = (*SpannerService)(nil)

// SpannerService is an implementation of [Service] for managing Thread and ThreadEvents via Google Cloud Spanner.
type SpannerService struct {
	db         *spanner.Client
	historyTbl string
	eventsTbl  string
	authorizer *iam.IAM
	v1.UnimplementedThreadServiceServer
}

func NewSpannerService(ctx context.Context, config *SpannerStoreConfig) (*SpannerService, error) {
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
				v1.ThreadService_ListThreads_FullMethodName,
				v1.ThreadService_AppendThreadEvent_FullMethodName,
			},
			AllUsers: true,
		},
		{
			Name: roleThreadViewer,
			Permissions: []string{
				v1.ThreadService_GetThread_FullMethodName,
			},
			AllUsers: false,
		},
		{
			Name: roleThreadAdmin,
			Permissions: []string{
				v1.ThreadService_GetThread_FullMethodName,
				v1.ThreadService_ListThreadEvents_FullMethodName,
			},
			AllUsers: false,
		},
	})
	if err != nil {
		return nil, err
	}

	return &SpannerService{
		db:         db,
		historyTbl: config.ThreadsTable,
		eventsTbl:  config.EventsTable,
		authorizer: authorizer,
	}, nil
}

// GetThread implements the [Service.GetThread] method.
func (s *SpannerService) GetThread(ctx context.Context, req *v1.GetThreadRequest) (*v1.Thread, error) {
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
	if !az.HasAccess(v1.ThreadService_GetThread_FullMethodName) {
		return nil, status.Errorf(codes.PermissionDenied, "you do not have permission to access this resource")
	}

	return history, nil
}

// ListThreads implements the [Service.ListThreads] method.
func (s *SpannerService) ListThreads(ctx context.Context, req *v1.ListThreadsRequest) (*v1.ListThreadsResponse, error) {
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
	if req.PageToken != "" {
		offset, err = strconv.Atoi(req.PageToken)
		if err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "invalid page token")
		}
	}
	statement.Params["offset"] = offset

	// make db hit and build up results
	var resources []*v1.Thread
	iterator := s.db.ReadOnlyTransaction().Query(ctx, statement)
	if err := iterator.Do(func(r *spanner.Row) error {
		history := &v1.Thread{}
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

	return &v1.ListThreadsResponse{
		Threads:       resources,
		NextPageToken: nextPageToken,
	}, nil
}

// ListThreadEvents implements the [Service.ListThreadEvents] method.
func (s *SpannerService) ListThreadEvents(ctx context.Context, req *v1.ListThreadEventsRequest) (*v1.ListThreadEventsResponse, error) {
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

	var events []*v1.ThreadEvent
	iterator := s.db.ReadOnlyTransaction().Query(ctx, statement)
	err = iterator.Do(func(r *spanner.Row) error {
		event := &v1.ThreadEvent{}
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

	return &v1.ListThreadEventsResponse{
		Events:        events,
		NextPageToken: nextPageToken,
	}, nil
}

// AppendThreadEvent implements the [Service.AppendThreadEvent] method.
func (s *SpannerService) AppendThreadEvent(ctx context.Context, req *v1.AppendThreadEventRequest) (*v1.AppendThreadEventResponse, error) {
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
	case *v1.ThreadEvent_ArtifactUpdate:
		ctxID = req.GetEvent().GetArtifactUpdate().GetContextId()
	case *v1.ThreadEvent_Message:
		ctxID = req.GetEvent().GetMessage().GetContextId()
	case *v1.ThreadEvent_StatusUpdate:
		ctxID = req.GetEvent().GetStatusUpdate().GetContextId()
	case *v1.ThreadEvent_Task:
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
	if _, _, err := s.readThread(ctx, historyName); err != nil {
		if !strings.Contains(err.Error(), "row not found") {
			return nil, err
		}
		now := time.Now().UTC()
		history := &v1.Thread{
			Name:        historyName,
			DisplayName: now.Format(time.RFC3339),
			AgentId:     req.GetAgentId(),
			CreateTime:  timestamppb.New(now),
		}
		policy := &iampb.Policy{
			Bindings: []*iampb.Binding{
				{
					Role:    roleThreadAdmin,
					Members: []string{az.Identity.PolicyMember()},
				},
			},
		}
		mutation := spanner.Insert(s.historyTbl, []string{"key", "Thread", "Policy"}, []any{history.Name, history, policy})
		if _, err := s.db.Apply(ctx, []*spanner.Mutation{mutation}); err != nil {
			return nil, err
		}
	}

	mutation := spanner.Insert(s.eventsTbl, []string{"key", "Event"}, []any{req.GetEvent().GetName(), req.GetEvent()})
	if _, err := s.db.Apply(ctx, []*spanner.Mutation{mutation}); err != nil {
		return nil, err
	}

	return &v1.AppendThreadEventResponse{}, nil
}

func (s *SpannerService) readThread(ctx context.Context, name string) (*v1.Thread, *iampb.Policy, error) {
	row, err := s.db.Single().ReadRow(ctx, s.historyTbl, spanner.Key{name}, []string{"Thread", "Policy"})
	if err != nil {
		return nil, nil, err
	}
	history := &v1.Thread{}
	policy := &iampb.Policy{}

	if err := row.Columns(history, policy); err != nil {
		return nil, nil, err
	}
	return history, policy, nil
}
