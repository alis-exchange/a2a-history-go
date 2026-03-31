package a2asrv

import (
	"context"
	"slices"
	"sync"

	"github.com/a2aproject/a2a-go/v2/a2a"
	sdka2asrv "github.com/a2aproject/a2a-go/v2/a2asrv"
	"github.com/google/uuid"
	pb "go.alis.build/common/alis/a2a/extension/history/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type invocationKeyType struct{}

func (k invocationKeyType) String() string {
	return "a2a-extension-history-invocation-id"
}

var invocationKey = invocationKeyType{}

var _ sdka2asrv.CallInterceptor = (*interceptor)(nil)

type interceptor struct {
	service pb.ThreadServiceServer
	agentID string
	mu      sync.Mutex
	store   map[string]*pb.ThreadEvent
}

// InterceptorOptions configures [NewInterceptor].
type InterceptorOptions struct {
	// AgentID is stored on appended events and on auto-created threads (see AppendThreadEvent on the storage implementation).
	AgentID string
}

// InterceptorOption mutates [InterceptorOptions] when passed to [NewInterceptor].
type InterceptorOption func(*InterceptorOptions)

// WithAgentID sets the agent identifier recorded on history writes.
func WithAgentID(agentID string) InterceptorOption {
	return func(o *InterceptorOptions) {
		o.AgentID = agentID
	}
}

// NewInterceptor returns an [a2asrv.CallInterceptor] that records history by calling service for each
// relevant request/response. Pass [WithAgentID] so persisted threads/events include your agent id.
//
// See package documentation for Before/After behavior and deferred SendMessage handling.
//
// A2A middleware: https://github.com/a2aproject/a2a-go/blob/main/a2asrv/middleware.go
func NewInterceptor(service pb.ThreadServiceServer, opts ...InterceptorOption) *interceptor {
	options := &InterceptorOptions{}
	for _, opt := range opts {
		opt(options)
	}
	return &interceptor{
		service: service,
		agentID: options.AgentID,
		store:   make(map[string]*pb.ThreadEvent),
	}
}

// Before implements [a2asrv.CallInterceptor]: activates the history extension when requested, and for
// SendMessage requests either appends immediately (when context id is present) or caches the event
// and stores an invocation id on the returned context when context id is empty.
func (i *interceptor) Before(ctx context.Context, callCtx *sdka2asrv.CallContext, req *sdka2asrv.Request) (context.Context, any, error) {
	// Check incoming request for extension activation
	if !slices.Contains(callCtx.Extensions().RequestedURIs(), AgentExtension.URI) {
		return ctx, nil, nil
	}

	// Activate the extension
	callCtx.Extensions().Activate(&AgentExtension)

	// Check if payload is nil
	if req.Payload == nil {
		return ctx, nil, nil
	}

	// Handle incoming event payload type
	switch p := req.Payload.(type) {
	case *a2a.SendMessageRequest:
		{
			// Check if message is nil
			if p == nil || p.Message == nil {
				return ctx, nil, nil
			}

			// Convert message to proto
			message, err := toProtoMessage(p.Message)
			if err != nil {
				return ctx, nil, err
			}

			// Initialize event
			event := &pb.ThreadEvent{
				Payload:    &pb.ThreadEvent_Message{Message: message},
				CreateTime: timestamppb.Now(),
			}

			// Defer event creation to "After" if contextID not currently known.
			// We track a unique "invocationID" to retrieve the corresponding event later.
			if p.Message.ContextID == "" {
				invocationID := uuid.New().String()
				newCtx := context.WithValue(ctx, invocationKey, invocationID)
				i.cache(invocationID, event)
				return newCtx, nil, nil
			}

			// Capture Event
			ctx = i.injectGrpcMetadata(ctx, callCtx)
			_, err = i.service.AppendThreadEvent(ctx, &pb.AppendThreadEventRequest{
				Event:   event,
				AgentId: i.agentID,
			})
			if err != nil {
				return ctx, nil, err
			}
		}
	default:
		return ctx, nil, nil
	}

	return ctx, nil, nil
}

// After implements [a2asrv.CallInterceptor]: maps the response payload to a ThreadEvent, optionally
// flushes a deferred SendMessage using the response context id, then appends the response event when present.
func (i *interceptor) After(ctx context.Context, callCtx *sdka2asrv.CallContext, resp *sdka2asrv.Response) error {
	// Check that the extension is active
	if !callCtx.Extensions().Active(&AgentExtension) {
		return nil
	}

	var contextID string

	// Handle incoming event payload type
	var event *pb.ThreadEvent
	switch p := resp.Payload.(type) {
	case *a2a.Task:
		if p != nil {
			contextID = p.ContextID
			task, err := toProtoTask(p)
			if err != nil {
				return err
			}
			event = &pb.ThreadEvent{
				Payload:    &pb.ThreadEvent_Task{Task: task},
				CreateTime: timestamppb.Now(),
			}
		}
	case *a2a.Message:
		if p != nil {
			contextID = p.ContextID
			message, err := toProtoMessage(p)
			if err != nil {
				return err
			}
			event = &pb.ThreadEvent{
				Payload:    &pb.ThreadEvent_Message{Message: message},
				CreateTime: timestamppb.Now(),
			}
		}
	case *a2a.TaskStatusUpdateEvent:
		if p != nil {
			contextID = p.ContextID
			statusUpdate, err := toProtoTaskStatusUpdate(p)
			if err != nil {
				return err
			}
			event = &pb.ThreadEvent{
				Payload:    &pb.ThreadEvent_StatusUpdate{StatusUpdate: statusUpdate},
				CreateTime: timestamppb.Now(),
			}
		}
	case *a2a.TaskArtifactUpdateEvent:
		if p != nil {
			contextID = p.ContextID
			artifactUpdate, err := toProtoTaskArtifacteUpdate(p)
			if err != nil {
				return err
			}
			event = &pb.ThreadEvent{
				Payload:    &pb.ThreadEvent_ArtifactUpdate{ArtifactUpdate: artifactUpdate},
				CreateTime: timestamppb.Now(),
			}
		}
	}

	// Check for invocationID and any cached events to be captured
	invocationID, ok := ctx.Value(invocationKey).(string)
	if ok {
		if ev, found := i.peekCached(invocationID); found {
			p, ok := ev.Payload.(*pb.ThreadEvent_Message)
			if ok && p != nil && p.Message != nil && contextID != "" {
				// Update the contextID
				p.Message.ContextId = contextID
				_, err := i.service.AppendThreadEvent(ctx, &pb.AppendThreadEventRequest{
					Event:   ev,
					AgentId: i.agentID,
				})
				if err != nil {
					return err
				}
				i.popCached(invocationID)
			}
		}
	}

	// If no event was captured, return nil
	if event == nil {
		// If there is an error, return it
		if resp.Err != nil {
			return resp.Err
		}

		// Otherwise, return nil
		return nil
	}

	// Capture the event
	_, err := i.service.AppendThreadEvent(ctx, &pb.AppendThreadEventRequest{
		Event:   event,
		AgentId: i.agentID,
	})
	if err != nil {
		return err
	}

	return nil
}

// cache stores a deferred ThreadEvent keyed by invocation id until After can fill context id.
func (i *interceptor) cache(invocationID string, event *pb.ThreadEvent) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.store[invocationID] = event
}

// peekCached returns the cached event without removing it (used before append in After).
func (i *interceptor) peekCached(invocationID string) (*pb.ThreadEvent, bool) {
	i.mu.Lock()
	defer i.mu.Unlock()
	ev, ok := i.store[invocationID]
	return ev, ok
}

// popCached deletes a key after a successful deferred append.
func (i *interceptor) popCached(invocationID string) {
	i.mu.Lock()
	defer i.mu.Unlock()
	delete(i.store, invocationID)
}

// injectGrpcMetadata injects all incoming headers and set them in gRPC metadata so that
// downstream service calls can use them.
func (i *interceptor) injectGrpcMetadata(ctx context.Context, callCtx *sdka2asrv.CallContext) context.Context {
	md := metadata.MD{}
	for k, v := range callCtx.ServiceParams().List() {
		md[k] = v
	}

	ctx = grpc.NewContextWithServerTransportStream(ctx, &grpcMethodStream{method: pb.ThreadService_AppendThreadEvent_FullMethodName})
	return metadata.NewIncomingContext(ctx, md)
}

type grpcMethodStream struct {
	method string
}

func (s *grpcMethodStream) Method() string                  { return s.method }
func (s *grpcMethodStream) SetHeader(md metadata.MD) error  { return nil }
func (s *grpcMethodStream) SendHeader(md metadata.MD) error { return nil }
func (s *grpcMethodStream) SetTrailer(md metadata.MD) error { return nil }
