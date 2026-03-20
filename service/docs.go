// Package service defines the [Service] contract for persisting and querying A2A thread history
// (threads and thread events), and provides [SpannerService], a Google Cloud Spanner implementation.
//
// # Contract
//
// [Service] is the abstraction used by HTTP/JSON-RPC handlers and by the A2A call interceptor in
// package go.alis.build/a2a/extension/history/srv.
// Implementations must honor the protobuf API semantics for alis.a2a.extension.history.v1
// (GetThread, ListThreads, ListThreadEvents, AppendThreadEvent).
//
// # SpannerService
//
// [NewSpannerService] opens a Spanner client and configures an IAM authorizer with three roles:
//
//   - roles/open — anonymous ListThreads and AppendThreadEvent (see code for exact RPC names).
//   - roles/thread.viewer — GetThread when the caller’s policy member is bound on the thread.
//   - roles/thread.admin — GetThread and ListThreadEvents for threads where the caller is admin.
//
// Thread names must match `^threads/[a-z0-9-]{2,50}$`. [AppendThreadEvent] derives the thread key
// from the context id inside the event payload, ensures the thread row exists (inserting it with
// IAM policy on first write), assigns a unique event name, and inserts the event in one transaction.
//
// # Code flow (SpannerService)
//
//	GetThread / ListThreadEvents: authorize → (validate) → read thread policy → check RPC permission.
//	ListThreads: authorize open RPC → query threads table (optionally filter by policy member).
//	AppendThreadEvent: validate event → authorize → extract context id from payload → upsert thread + insert event.
//
// Internal helper [SpannerService.readThread] loads Thread + IAM Policy from the threads table.
package service
