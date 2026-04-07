// Package service provides [ThreadService], the built-in Google Cloud Spanner implementation for
// persisting and querying A2A thread history (threads and thread events).
//
// # ThreadService
//
// [NewThreadService] opens a Spanner client and configures an IAM authorizer with three roles:
//
//   - roles/open — anonymous ListThreads and AppendThreadEvent (see code for exact RPC names).
//   - roles/thread.viewer — GetThread when the caller’s policy member is bound on the thread.
//   - roles/thread.admin — GetThread, ListThreadEvents, and DeleteThread for threads where the caller is admin.
//
// [ThreadService.Register] wraps the generated gRPC registration helper so callers can mount the
// service without importing the generated protobuf package directly.
//
// Thread names must match `^threads/[a-z0-9-]{2,50}$`. [AppendThreadEvent] derives the thread key
// from the context id inside the event payload, ensures the thread row exists (inserting it with
// IAM policy on first write), assigns a unique event name and monotonic per-thread sequence, and
// updates thread sequence state (`next_sequence`, `latest_sequence`, unread projection fields) in
// the same transaction as the event insert.
//
// [ListThreads] returns caller-scoped read projections. For callers bound as thread admins, the
// returned threads are marked read by advancing `read_sequence` to `latest_sequence` and clearing
// `has_unread`. Other callers receive `has_unread` derived from the stored thread state.
//
// # Code flow (ThreadService)
//
//	GetThread / ListThreadEvents / DeleteThread: authorize → (validate) → read thread policy → check RPC permission.
//	ListThreads: authorize open RPC → query threads table (optionally filter by policy member) → project/update read cursors for owning admins.
//	AppendThreadEvent: validate event → authorize → extract context id from payload → atomically load/update thread sequence state + insert event.
//
// Internal helper [ThreadService.readThread] loads Thread + IAM Policy from the threads table.
package service
