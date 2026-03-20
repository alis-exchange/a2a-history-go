package service

import (
	"context"
	"go.alis.build/a2a/extension/history/alis/a2a/extension/history/v1"
)

// Service is the persistence contract for A2A thread history: one [v1.Thread] per conversation
// context and ordered [v1.ThreadEvent] rows (messages, tasks, status updates, artifact updates).
//
// Implementations enforce authorization and validation; callers include JSON-RPC handlers and the
// A2A server call interceptor (see package go.alis.build/a2a/extension/history/srv).
type Service interface {
	// GetThread returns a single thread by resource name (e.g. threads/{id}).
	GetThread(ctx context.Context, req *v1.GetThreadRequest) (*v1.Thread, error)
	// ListThreads lists threads visible to the caller, with optional paging via page_token / page_size.
	ListThreads(ctx context.Context, req *v1.ListThreadsRequest) (*v1.ListThreadsResponse, error)
	// ListThreadEvents lists events for a thread parent (threads/{id}), newest first, with paging.
	ListThreadEvents(ctx context.Context, req *v1.ListThreadEventsRequest) (*v1.ListThreadEventsResponse, error)
	// AppendThreadEvent appends one event; the event payload must carry a non-empty context id for routing.
	AppendThreadEvent(ctx context.Context, req *v1.AppendThreadEventRequest) (*v1.AppendThreadEventResponse, error)
}
