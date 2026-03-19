# A2A HISTORY GO SDK

[![License](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](LICENSE)


This project contains a lightweight Go library for developers supporting the  the [a2a-history](spec.md) A2A extension.


## ✨ Features

- **Integration with the offical [A2A Go SDK](https://github.com/a2aproject/a2a-go/tree/main):** Builds on top of the official library for building A2A-compliant Agents in Go.
- **Extensible:** Easily add support and customise for different database backend implementations.


## 🚀 Installation

To install `a2a-history-go` to your project, run:

```bash
go get -u go.alis.build/a2a/extension/history
```

## 🛠️ Getting Started

### History Service
For advanced developers wishing to have total flexibiliy and control over the backend storage solution used to persist A2A events, `a2a-history-go` exposes the `Service` interface which must be satisfied and implemented:

```go
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
```

Alternatively, developers may choose to use any of the pre-built Service implementations. Currently, only the `SpannerService` implementation is available, which integrates with Google Cloud Spanner and the Alis Build ecosystem:

```go

import (
	"go.alis.build/a2a/extension/history/service"
)

historyService, err := service.NewSpannerService(ctx, &service.SpannerStoreConfig{
	    // TODO: Complete with your values.
		Project:      "SPANNER_PROJECT_NAME",
		Instance:     "SPANNER_INSTANCE_NAME",
		Database:     "SPANNER_DATABASE_NAME",
		ThreadsTable: "THREADS_TABLE_NAME",
		EventsTable:  "EVENTS_TABLE_NAME"
})
```

Below is the corresponding Terraform resource configuration that must be used for compatibility with the `SpannerService`.

```
resource "alis_google_spanner_table" "a2a_thread" {
  project         = "SPANNER_PROJECT_NAME"
  instance        = "SPANNER_INSTANCE_NAME"
  database        = "SPANNER_DATABASE_NAME"
  name            = "THREADS_TABLE_NAME"
  schema = {
    columns = [
      {
        name           = "key",
        type           = "STRING",
        is_primary_key = true,
        required       = true
      },
      {
        name          = "Thread",
        type          = "PROTO"
        proto_package = "alis.a2a.extension.history.v1.Thread"
        required      = true
      },
      {
        name          = "Policy"
        type          = "PROTO"
        proto_package = "google.iam.v1.Policy"
        required      = true
      },
      {
        name            = "create_time",
        type            = "TIMESTAMP",
        required        = false,
        is_computed     = true,
        computation_ddl = "TIMESTAMP_ADD(TIMESTAMP_SECONDS(Thread.create_time.seconds),INTERVAL CAST(FLOOR(History.create_time.nanos / 1000) AS INT64) MICROSECOND)",
        is_stored       = true
      },
    ]
  }
}

resource "alis_google_spanner_table" "a2a_thread_events" {
  project         = "SPANNER_PROJECT_NAME"
  instance        = "SPANNER_INSTANCE_NAME"
  database        = "SPANNER_DATABASE_NAME"
  name            = "EVENTS_TABLE_NAME"
  schema = {
    columns = [
      {
        name           = "key",
        type           = "STRING",
        is_primary_key = true,
        required       = true
      },
      {
        name          = "Event",
        type          = "PROTO"
        proto_package = "alis.a2a.extension.history.v1.ThreadEvent"
        required      = true
      },
      {
        name           = "thread",
        type           = "STRING",
        required       = false
        is_stored      = true
        is_computed    = true
        computation_ddl = "REGEXP_EXTRACT(Event.name, r'^(threads/[^/]+)')",
      },
      {
        name            = "create_time",
        type            = "TIMESTAMP",
        required        = false,
        is_computed     = true,
        computation_ddl = "TIMESTAMP_ADD(TIMESTAMP_SECONDS(Event.create_time.seconds),INTERVAL CAST(FLOOR(Event.create_time.nanos / 1000) AS INT64) MICROSECOND)",
        is_stored       = true
      },
    ]
  }
}

resource "alis_google_spanner_table_foreign_key" "a2a_thread_events_fk" {
  project           = "SPANNER_PROJECT_NAME"
  instance          = "SPANNER_INSTANCE_NAME"
  database          = "SPANNER_DATABASE_NAME"
  name              = "EVENT_FOREIGN_KEY_NAME"
  table             = "EVENTS_TABLE_NAME"
  column            = "thread"
  referenced_table  = alis_google_spanner_table.a2a_thread.name
  referenced_column = "key"
  on_delete         = "CASCADE"
}

```

### Registering the custom CallInterceptor
Registering the custom CallInterceptor on the requestHandler manages extension activation and capturing of A2A events as they are generated by the AgentExecutor. 

```go

import (
    "github.com/a2aproject/a2a-go/a2asrv"
	"go.alis.build/a2a/extension/history/srv"
)

// Add CallInterceptor to A2A Server\s request handler
requestHandler := a2asrv.NewHandler(
		&agentExecutor{},
		...
		a2asrv.WithCallInterceptor(serv.NewInterceptor(historyService),
)
```

Or, if using your own custom Service implementation:

```go

import (
    "github.com/a2aproject/a2a-go/a2asrv"
	"go.alis.build/a2a/extension/history/srv"
)

var MyCustomService = MyCustomHistoryService{} // MyCustomService implements the Service interface

// Add CallInterceptor to A2A Server\s request handler
requestHandler := a2asrv.NewHandler(
		&agentExecutor{},
		...
		a2asrv.WithCallInterceptor(srv.NewInterceptor(MyCustomService)),
	)
```
