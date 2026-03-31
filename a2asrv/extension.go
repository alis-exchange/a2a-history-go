package a2asrv

import "github.com/a2aproject/a2a-go/v2/a2a"

const extensionURI = "https://a2a.alis.build/extensions/history/v1"

// AgentExtension advertises support for the A2A history extension.
var AgentExtension = &a2a.AgentExtension{
	Description: "Provides persisted thread and event history so clients can list prior interaction contexts, inspect their events, and resume conversations across channels.",
	Params:      map[string]any{},
	Required:    false,
	URI:         extensionURI,
}
