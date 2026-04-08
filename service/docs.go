// Package service provides [ThreadService], the built-in Google Cloud Spanner implementation for
// persisting and querying A2A thread history (threads and thread events).
//
// In the ge/agent/v2/infra deployment layout, the backing Spanner tables are provisioned by the
// Terraform module at alis/build/ge/agent/v2/infra/modules/alis.a2a.extension.history.v1. That
// module creates the Threads, ThreadEvents, and UserThreadStates tables with names derived from
// ALIS_OS_PROJECT and the neuron identifier, matching the schema expected by [ThreadService].
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
// updates shared thread sequence state (`next_sequence`, `latest_sequence`) in the same transaction
// as the event insert.
//
// [ListThreads] returns caller-scoped [pb.ThreadView] projections by joining each thread with the
// caller's [pb.UserThreadState] row when one exists. Per-user read/pin state is stored outside the
// shared Thread resource.
//
// # Code flow (ThreadService)
//
//	GetThread / ListThreadEvents / DeleteThread: authorize → (validate) → read thread policy → check RPC permission.
//	ListThreads: authorize open RPC → query threads table (optionally filter by policy member) → join caller user-thread state → return ThreadView projections.
//	GetUserThreadState / UpdateUserThreadState: require authenticated caller → authorize access to parent thread → read/write caller-scoped UserThreadState.
//	AppendThreadEvent: validate event → authorize → extract context id from payload → atomically load/update thread sequence state + insert event.
//
// Internal helper [ThreadService.readThread] loads Thread + IAM Policy from the threads table.
package service
