// Package a2asrv bridges the A2A Go server runtime to the built-in history [service.ThreadService].
//
// This package is typically wired into the agent container where infra/cloudrun.tf injects the 
// managed Spanner coordinates used by the historyvservice and infra/main.tf enables the matching 
// history storage module.
//
// [NewInterceptor] implements github.com/a2aproject/a2a-go/v2/a2asrv.CallInterceptor, activating
// the a2a-history extension URI and appending ThreadEvent records (see alis.a2a.extension.history.v1)
// for inbound SendMessage traffic and outbound Task / Message / status / artifact responses.
//
// # Call interceptor — request path (Before)
//
//  1. If the client did not request the history extension URI, return without side effects.
//  2. Activate the extension on the call context.
//  3. For [*a2a.SendMessageRequest], convert the message to a [v1.ThreadEvent] with a Message payload.
//  4. If the message already has a context id, call [service.ThreadService.AppendThreadEvent] immediately.
//  5. If the context id is empty, generate an invocation id, stash the event under it (see cache), and
//     attach the id to [context.Context] for the downstream handler.
//
// # Call interceptor — response path (After)
//
//  1. If the extension is not active on the call, return.
//  2. Classify [a2asrv.Response].Payload (Task, Message, TaskStatusUpdateEvent, TaskArtifactUpdateEvent)
//     and derive context id + a response [v1.ThreadEvent] when the concrete pointer is non-nil.
//  3. If the context carries an invocation id and a matching cached SendMessage event exists, copy the
//     response context id onto the message and append that event first (peek → append → pop).
//  4. If a response event was built, append it as a second row (intended: deferred user message + response).
//
// Cached entries are protected by a mutex; peek does not remove; pop removes after a successful append.
package a2asrv
