// Package jsonrpc exposes the history API over HTTP as JSON-RPC 2.0, backed by the built-in
// history [service.ThreadService].
//
// In the ge/agent/v2/infra layout, this handler normally runs inside the Cloud Run agent service
// defined in infra/cloudrun.tf, while the required storage tables come from
// infra/modules/alis.a2a.extension.history.v1.
//
// [NewJSONRPCHandler] handles GetThread, ListThreads, and ListThreadEvents over HTTP POST.
// Optional [WithCORS] enables browser cross-origin access. Params and results use protojson
// (camelCase JSON on the wire; unknown fields discarded on input; unpopulated fields emitted on output).
// gRPC status errors from the service are mapped to JSON-RPC error responses with appropriate codes.
// [Register] mounts the handler on method-aware muxes such as the Go 1.22+ ServeMux.
//
// # JSON-RPC handler
//
// POST-only (plus OPTIONS when [WithCORS] is set). Decodes JSON-RPC 2.0, validates jsonrpc version and
// non-empty id, dispatches by method name, unmarshals params into protobuf request types with protojson
// (DiscardUnknown on decode for forward compatibility), and returns a result or error.
// Success responses embed the protobuf message as JSON in the JSON-RPC result field (protojson encode).
// Errors are encoded as [JSONRPCError]; when the service returns a gRPC status, standard codes such as
// InvalidArgument and NotFound map to JSON-RPC error codes in the -326xx and -320xx ranges.
// Mount the handler at [HistoryExtensionPath] (or a path your gateway uses consistently).
package jsonrpc
