package service

import (
	"context"
	"go.alis.build/a2a/extension/history/alis/a2a/extension/history/v1"
)

// Service is a A2A thread storage service.
// It provides a set of methods for managing A2A threads and their events
type Service interface {
	// GetThread
	GetThread(ctx context.Context, req *v1.GetThreadRequest) (*v1.Thread, error)
	// ListThreads
	ListThreads(ctx context.Context, req *v1.ListThreadsRequest) (*v1.ListThreadsResponse, error)
	// ListThreadEvents
	ListThreadEvents(ctx context.Context, req *v1.ListThreadEventsRequest) (*v1.ListThreadEventsResponse, error)
	// AppendThreadEvent
	AppendThreadEvent(ctx context.Context, req *v1.AppendThreadEventRequest) (*v1.AppendThreadEventResponse, error)
}
