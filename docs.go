// Package a2ahistory provides a Go library for interacting with the A2A History extension.
//
// In addition to the storage and transport subpackages, the root package provides
// convenience registration helpers such as [RegisterGRPC] and [RegisterHTTP] so
// callers do not need to import the jsonrpc subpackage just to mount the
// standard history management surface.
//
// The library is designed to pair with the Terraform-based agent infra layout under
// alis/build/ge/agent/v2/infra, where the history storage schema is provisioned by the
// modules/alis.a2a.extension.history.v1 module and then consumed by the agent runtime.
package a2ahistory
