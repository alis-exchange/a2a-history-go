package srv

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"

	v1 "go.alis.build/a2a/extension/history/alis/a2a/extension/history/v1"
	"go.alis.build/a2a/extension/history/service"
)

// JSON-RPC 2.0 protocol constants
const (
	version = "2.0"
	// JSON-RPC methods supported by this extension
	methodGetThread        = "GetThread"
	methodListThreads      = "ListThreads"
	methodListThreadEvents = "ListThreadEvents"
	// HistoryExtensionPath is the default HTTP path segment for mounting [NewJSONRPCHandler].
	HistoryExtensionPath = "/extensions/a2ahistory"
)

var (
	// JSONRPC Errors
	errInvalidRequest = ErrInvalidRequest{err: errors.New("invalid request")}
	errInvalidParams  = ErrInvalidParams{err: errors.New("invalid parameters")}
	errMethodNotFound = ErrMethodNotFound{err: errors.New("method not found")}
	errInternalError  = ErrInternalError{err: errors.New("internal error")}
	errParseError     = ErrParseError{err: errors.New("parse error")}
	errServerError    = ErrServerError{err: errors.New("server error")}
)

// jsonrpcRequest represents a JSON-RPC 2.0 request.
type jsonrpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
	ID      any             `json:"id"`
}

// jsonrpcResponse represents a JSON-RPC 2.0 response.
type jsonrpcResponse struct {
	JSONRPC string              `json:"jsonrpc"`
	ID      any                 `json:"id"`
	Result  any                 `json:"result,omitempty"`
	Error   *jsonrpcErrorObject `json:"error,omitempty"`
}

type jsonrpcHandler struct {
	service service.Service
}

// ServeHTTP handles a single JSON-RPC 2.0 call: POST only, decodes body, validates version and id,
// dispatches to GetThread / ListThreads / ListThreadEvents, and writes JSON result or error.
func (h *jsonrpcHandler) ServeHTTP(rw http.ResponseWriter, req *http.Request) {
	ctx := req.Context()

	// Validate that is "POST" request
	if req.Method != "POST" {
		h.writeJSONRPCError(ctx, rw, errInvalidRequest, nil)
		return
	}

	defer func() {
		if err := req.Body.Close(); err != nil {
			log.Fatal(ctx, "failed to close request body", err)
		}
	}()

	var payload jsonrpcRequest
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		h.writeJSONRPCError(ctx, rw, errInvalidRequest, payload.ID)
		return
	}

	// Validate payload ID
	if payload.ID == "" {
		h.writeJSONRPCError(ctx, rw, errInvalidRequest, nil)
		return
	}

	// Validate JSONRPC Version
	if payload.JSONRPC != version {
		h.writeJSONRPCError(ctx, rw, errInvalidRequest, payload.ID)
		return
	}

	// Handle the request
	h.handleRequest(ctx, rw, &payload)
}

// handleRequest runs after top-level validation and encodes success or delegates to [jsonrpcHandler.writeJSONRPCError].
func (h *jsonrpcHandler) handleRequest(ctx context.Context, rw http.ResponseWriter, req *jsonrpcRequest) {
	var result any
	var err error
	switch req.Method {
	case methodListThreads:
		result, err = h.onHandleThreadsList(ctx, req.Params)
	case methodGetThread:
		result, err = h.onHandleThreadGet(ctx, req.Params)
	case methodListThreadEvents:
		result, err = h.onHandleEventsList(ctx, req.Params)
	case "":
		err = errInvalidRequest
	default:
		err = errMethodNotFound
	}
	if err != nil {
		h.writeJSONRPCError(ctx, rw, err, req.ID)
		return
	}

	if result != nil {
		resp := jsonrpcResponse{JSONRPC: version, ID: req.ID, Result: result}
		if err := json.NewEncoder(rw).Encode(resp); err != nil {
			log.Fatal(ctx, "failed to encode response", err)
		}
	}
}

func (h *jsonrpcHandler) onHandleThreadsList(ctx context.Context, raw json.RawMessage) (*v1.ListThreadsResponse, error) {
	var query *v1.ListThreadsRequest
	if err := json.Unmarshal(raw, &query); err != nil {
		return nil, errInvalidParams
	}
	return h.service.ListThreads(ctx, query)
}

func (h *jsonrpcHandler) onHandleThreadGet(ctx context.Context, raw json.RawMessage) (*v1.Thread, error) {
	var query *v1.GetThreadRequest
	if err := json.Unmarshal(raw, &query); err != nil {
		return nil, errInvalidParams
	}
	return h.service.GetThread(ctx, query)
}

func (h *jsonrpcHandler) onHandleEventsList(ctx context.Context, raw json.RawMessage) (*v1.ListThreadEventsResponse, error) {
	var query *v1.ListThreadEventsRequest
	if err := json.Unmarshal(raw, &query); err != nil {
		return nil, errInvalidParams
	}
	return h.service.ListThreadEvents(ctx, query)
}

func (h *jsonrpcHandler) writeJSONRPCError(ctx context.Context, rw http.ResponseWriter, err error, reqID any) {
	if err == nil {
		return
	}

	var jsonrpcError JSONRPCError
	if !errors.As(err, &jsonrpcError) {
		jsonrpcError = errInternalError
	}
	resp := jsonrpcResponse{JSONRPC: version, Error: jsonrpcError.JSONRPCErrorObject(), ID: reqID}
	if err := json.NewEncoder(rw).Encode(resp); err != nil {
		log.Fatal(ctx, "failed to send error response", err)
	}
}

// NewJSONRPCHandler returns an [http.Handler] that implements JSON-RPC 2.0 for the history API
// (ListThreads, GetThread, ListThreadEvents). The service must implement [service.Service].
func NewJSONRPCHandler(service service.Service) http.Handler {
	return &jsonrpcHandler{
		service: service,
	}
}
