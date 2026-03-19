package a2ahistory

import (
	"context"

	"go.alis.build/a2a/extension/history/alis/a2a/extension/history/v1"
)

// Service is a A2A History storage service.
// It provides a set of methods for managing A2A histories and their events
type Service interface {
	// GetA2AHistory
	GetA2AHistory(ctx context.Context, req *v1.GetA2AHistoryRequest) (*v1.A2AHistory, error)
	// ListA2AHistories
	ListA2AHistories(ctx context.Context, req *v1.ListA2AHistoriesRequest) (*v1.ListA2AHistoriesResponse, error)
	// ListEvents
	ListEvents(ctx context.Context, req *v1.ListEventsRequest) (*v1.ListEventsResponse, error)
	// AppendEvent
	AppendEvent(ctx context.Context, req *v1.AppendEventRequest) (*v1.AppendEventResponse, error)
}
