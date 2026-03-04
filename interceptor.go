package a2ahistory

import (
	"context"
	"slices"
	"sync"
	"github.com/a2aproject/a2a-go/a2a"
	"github.com/a2aproject/a2a-go/a2asrv"
	"github.com/alis-exchange/a2a-history-go/alis/a2a/extension/history/v1"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	invocationKey = "a2a-extension-history-invocation-id"
	extensionUri = "https://github.com/alis-exchange/a2a-history-go/alis/a2a/extension/history/v1"
)

type interceptor struct {
	service  Service
	agentID  string
	mu       sync.Mutex
	store    map[string]*v1.A2AHistoryEvent
}

// Creates a new call interceptor satisfying the [a2asrv.CallInterceptor] interface.
// For details, seee https://github.com/a2aproject/a2a-go/blob/main/a2asrv/middleware.go
func NewInterceptor(service Service, agentID string) *interceptor{
	return &interceptor{
		service: service,
		agentID: agentID,
		store: make(map[string]*v1.A2AHistoryEvent),
	}
}

// Before allows to observe and modify the request.
func (i *interceptor) Before(ctx context.Context, callCtx *a2asrv.CallContext, req *a2asrv.Request) (context.Context, error) {
	// Check incoming request for extension activation
	if !slices.Contains(callCtx.Extensions().RequestedURIs(), extensionUri) {
		return ctx, nil
	}

	// Activate the extension
	callCtx.Extensions().Activate(&a2a.AgentExtension{URI: extensionUri})

	// Handle incoming event payload type
	var event *v1.A2AHistoryEvent
	switch p := req.Payload.(type) {
	case *a2a.MessageSendParams:
		message, err := toProtoMessage(p.Message)
		if err != nil {
			return ctx, err
		}
		event = &v1.A2AHistoryEvent{
			Payload: &v1.A2AHistoryEvent_Message{Message: message},
			CreateTime: timestamppb.Now(),
		}

		// Defer event creation to "After" if contextID not currently known.
		// We track a unique "invocationID" to retrieve the corresponding event later.
		if p.Message.ContextID == "" {
			invocationID := uuid.New().String()
			newCtx := context.WithValue(ctx, invocationKey, invocationID)
			i.cache(invocationID, event)
			return newCtx, nil
		}
	}

	// Capture Event
	 _, err := i.service.AppendEvent(ctx, &v1.AppendEventRequest{
		Event: 		event,
		AgentId: 	i.agentID,
	})
	if err != nil {
		return ctx, err
	}

	return ctx, nil
}

// After allows to observe and modify the response.
func (i *interceptor) After(ctx context.Context, callCtx *a2asrv.CallContext, resp *a2asrv.Response) error {
	// Check that the extension is active
	if !callCtx.Extensions().Active(&a2a.AgentExtension{URI: extensionUri}) {
		return nil
	}

	var contextID string

	// Handle incoming event payload type
	var event *v1.A2AHistoryEvent
	switch p := resp.Payload.(type) {
	case *a2a.Task:
		contextID = p.ContextID
		task, err := toProtoTask(p)
		if err != nil {
			return err
		}
		event = &v1.A2AHistoryEvent{
			Payload: &v1.A2AHistoryEvent_Task{Task: task},
			CreateTime: timestamppb.Now(),
		}
	case *a2a.Message:
		contextID = p.ContextID
		message, err := toProtoMessage(p)
		if err != nil {
			return err
		}
		event = &v1.A2AHistoryEvent{
			Payload: &v1.A2AHistoryEvent_Message{Message: message},
			CreateTime: timestamppb.Now(),
		}
	case *a2a.TaskStatusUpdateEvent:
		contextID = p.ContextID
		statusUpdate, err := toProtoTaskStatusUpdate(p)
		if err != nil {
			return err
		}
		event = &v1.A2AHistoryEvent{
			Payload: &v1.A2AHistoryEvent_StatusUpdate{StatusUpdate: statusUpdate},
			CreateTime: timestamppb.Now(),
		}
	case *a2a.TaskArtifactUpdateEvent:
		contextID = p.ContextID
		artifactUpdate, err := toProtoTaskArtifacteUpdate(p)
		if err != nil {
			return err
		}
		event = &v1.A2AHistoryEvent{
			Payload: &v1.A2AHistoryEvent_ArtifactUpdate{ArtifactUpdate: artifactUpdate},
			CreateTime: timestamppb.Now(),
		}
	}
    
	// Check for invocationID and any cached events to be captured
    invocationID, ok := ctx.Value(invocationKey).(string)
	if ok {
		event := i.store[invocationID]
		event.Payload.(*v1.A2AHistoryEvent_Message).Message.ContextId = contextID
		_, err := i.service.AppendEvent(ctx, &v1.AppendEventRequest{
			Event: 	  event,
			AgentId:  i.agentID,
		})
		if err != nil {
			return err
		}
		i.clear(invocationID)
	}

	// Capture the event
	_, err := i.service.AppendEvent(ctx, &v1.AppendEventRequest{
		Event: 		event,
		AgentId: 	i.agentID,
	})
	if err != nil {
		return err
	}

	return nil
}

func (i *interceptor) cache(invocationID string, event *v1.A2AHistoryEvent) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.store[invocationID] = event
}

func (i *interceptor) clear(invocationID string) {
	i.mu.Lock()
	defer i.mu.Unlock()
	delete(i.store, invocationID)
}