# A2A HISTORY GO SDK

[![License](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](LICENSE)

This project contains a lightweight Go library for developers supporting the [a2a-history](spec.md) A2A extension.

## Features

- **Integration with the official [A2A Go SDK](https://github.com/a2aproject/a2a-go/tree/main):** Builds on top of the official library for building A2A-compliant agents in Go.
- **Built-in persistence:** Includes a Google Cloud Spanner-backed [`service.ThreadService`](service/thread.go).
- **Two integration points:** An [`a2asrv`](a2asrv/) call interceptor (records traffic as your agent runs) and an optional [`jsonrpc`](jsonrpc/) HTTP handler for querying history from clients.

## Packages

| Package                                                   | Role                                                                                                                                                                                                                                                                                         |
| --------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| [`go.alis.build/a2a/extension/history/service`](service/) | [`ThreadService`](service/thread.go) and [`NewThreadService`](service/thread.go) for the built-in Google Cloud Spanner + IAM implementation.                                                                                                                                                 |
| [`go.alis.build/a2a/extension/history/a2asrv`](a2asrv/)   | [`NewInterceptor`](a2asrv/interceptor.go) ([`a2asrv.CallInterceptor`](https://pkg.go.dev/github.com/a2aproject/a2a-go/v2/a2asrv#CallInterceptor)) and A2A-to-proto conversion helpers ([`pbconv.go`](a2asrv/pbconv.go)).                                                                |
| [`go.alis.build/a2a/extension/history/jsonrpc`](jsonrpc/) | [`NewJSONRPCHandler`](jsonrpc/jsonrpc.go) with options such as [`WithCORS`](jsonrpc/cors.go), plus JSON-RPC error mapping ([`errors.go`](jsonrpc/errors.go)).                                                                                                                               |

Package-level documentation (design, IAM roles, interceptor flow) lives in [`service/docs.go`](service/docs.go), [`a2asrv/docs.go`](a2asrv/docs.go), and [`jsonrpc/docs.go`](jsonrpc/docs.go). Run `go doc -all ./...` locally for the full commentary.

## Architecture (high level)

```mermaid
flowchart LR
  subgraph agent [A2A server]
    H[a2asrv.Handler]
    EX[Agent executor]
    I[history Interceptor]
    H --> I
    I --> EX
  end
  subgraph storage [Persistence]
    S[service.ThreadService]
    DB[(Spanner)]
    S --> DB
  end
  I -->|AppendThreadEvent| S
  subgraph api [Optional HTTP]
    J[JSON-RPC handler]
    J -->|GetThread / List*| S
  end
```

1. **Interceptor path:** On each RPC, `Before` activates the history extension when the client requested it, converts `SendMessage` payloads to `ThreadEvent`s, and either appends immediately or defers until `After` has a `ContextID` from the response. `After` appends response-shaped events (task, message, status, artifact updates) and may append twice when a deferred user message is flushed first.
2. **JSON-RPC path:** Browsers or tools call `GetThread`, `ListThreads`, `ListThreadEvents` over JSON-RPC 2.0 POST; the same `ThreadService` backs reads. Params and `result` use **protojson** (camelCase JSON; unknown fields are ignored on decode). Errors returned by the service as **gRPC statuses** are mapped to JSON-RPC error codes (for example `InvalidArgument` → invalid params, `NotFound` → not found). For cross-origin browsers, register the handler with `jsonrpc.WithCORS()` (or tailored `CORSAllow*` options).

## Installation

```bash
go get -u go.alis.build/a2a/extension/history
```

## Getting started

### History service

Use the built-in Spanner-backed `ThreadService`:

```go
import (
	"go.alis.build/a2a/extension/history/service"
)

historyService, err := service.NewThreadService(ctx, &service.SpannerStoreConfig{
	Project:      "SPANNER_PROJECT_NAME",
	Instance:     "SPANNER_INSTANCE_NAME",
	Database:     "SPANNER_DATABASE_NAME",
	ThreadsTable: "THREADS_TABLE_NAME",
	EventsTable:  "EVENTS_TABLE_NAME",
})
```

Register it on your gRPC server without importing the generated history proto package:

```go
grpcServer := grpc.NewServer()
historyService.Register(grpcServer)
```

Below is Terraform aligned with `ThreadService` expectations (proto columns, foreign key).

```hcl
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
        name          = "Policy",
        type          = "PROTO"
        proto_package = "google.iam.v1.Policy"
        required      = true
      },
      {
        name            = "create_time",
        type            = "TIMESTAMP",
        required        = false,
        is_computed     = true,
        computation_ddl = "TIMESTAMP_ADD(TIMESTAMP_SECONDS(Thread.create_time.seconds),INTERVAL CAST(FLOOR(Thread.create_time.nanos / 1000) AS INT64) MICROSECOND)",
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

### Registering the call interceptor

Wire the interceptor into the A2A request handler so history is recorded as the executor runs:

```go
import (
	sdka2asrv "github.com/a2aproject/a2a-go/v2/a2asrv"
	historya2asrv "go.alis.build/a2a/extension/history/a2asrv"
)

requestHandler := sdka2asrv.NewHandler(
	&agentExecutor{},
	// ... other options ...
	sdka2asrv.WithCallInterceptor(historya2asrv.NewInterceptor(historyService, historya2asrv.WithAgentID("my-agent-id"))),
)
```

### JSON-RPC handler (optional)

Expose history reads over HTTP with [`jsonrpc.NewJSONRPCHandler`](jsonrpc/jsonrpc.go). The handler accepts optional functional options (`...jsonrpc.JSONRPCHandlerOption`). Mount it at [`jsonrpc.HistoryExtensionPath`](jsonrpc/jsonrpc.go) or any path your gateway uses. Wire format: JSON-RPC 2.0 with protobuf messages in `params` / `result` via **protojson**; service errors that are gRPC statuses are translated to JSON-RPC errors (see [`jsonrpc/errors.go`](jsonrpc/errors.go) for codes such as [`ErrNotFound`](jsonrpc/errors.go), [`ErrInvalidParams`](jsonrpc/errors.go)).

Same-origin or non-browser clients (no CORS):

```go
import "go.alis.build/a2a/extension/history/jsonrpc"

mux.Handle(jsonrpc.HistoryExtensionPath, jsonrpc.NewJSONRPCHandler(historyService))
```

Browser clients crossing origins need CORS on the JSON-RPC responses and an OPTIONS preflight. Pass [`jsonrpc.WithCORS`](jsonrpc/cors.go) (defaults: `Access-Control-Allow-Origin: *`, `POST` and `OPTIONS`, and common `Content-Type` / `Authorization` / Alis `X-Alis-*` headers):

```go
mux.Handle(jsonrpc.HistoryExtensionPath, jsonrpc.NewJSONRPCHandler(historyService, jsonrpc.WithCORS()))
```

Override origin or allowed headers/methods with [`jsonrpc.CORSAllowOrigin`](jsonrpc/cors.go), [`jsonrpc.CORSAllowHeaders`](jsonrpc/cors.go), and [`jsonrpc.CORSAllowMethods`](jsonrpc/cors.go):

```go
mux.Handle(jsonrpc.HistoryExtensionPath, jsonrpc.NewJSONRPCHandler(historyService,
	jsonrpc.WithCORS(
		jsonrpc.CORSAllowOrigin("https://app.example.com"),
		jsonrpc.CORSAllowHeaders("Content-Type", "Authorization"),
		jsonrpc.CORSAllowMethods("POST", "OPTIONS"),
	),
))
```

## Documentation

- See [`service/docs.go`](service/docs.go), [`a2asrv/docs.go`](a2asrv/docs.go), and [`jsonrpc/docs.go`](jsonrpc/docs.go) for method-level flows, IAM roles, and transport semantics.
- Proto definitions: `alis/a2a/extension/history/v1` in this module.
