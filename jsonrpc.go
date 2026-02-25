package a2ahistory

import (
	"context"
	"encoding/json"
	"net/http"
	"errors"
	"log"
	"github.com/alis-exchange/a2a-history-go/alis/a2a/extension/history/v1"
)

// JSON-RPC 2.0 protocol constants
const (
	version = "2.0"
	// JSON-RPC methods supported by this extension
	methodSessionGet           = "history/get"
	methodSessionsList         = "history/list"
	methodEventsList           = "history/events/list"
	HistoryExtensionPath       = "/extensions/a2ahistory"
)

var (
	// JSONRPC Errors
	errInvalidRequest = errors.New("invalid request")
	errInvalidParams  = errors.New("invalid parameters")
	errMethodNotFound = errors.New("method not found")
)

// jsonrpcRequest represents a JSON-RPC 2.0 request.
type jsonrpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
	ID any 				    `json:"id"`
}

// jsonrpcResponse represents a JSON-RPC 2.0 response.
type jsonrpcResponse struct {
	JSONRPC string         `json:"jsonrpc"`
	ID      any            `json:"id"`
	Result  any            `json:"result,omitempty"`
	Error   string         `json:"error,omitempty"`
}

type jsonrpcHandler struct {
	service Service
}

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

func (h *jsonrpcHandler) handleRequest(ctx context.Context, rw http.ResponseWriter, req *jsonrpcRequest) {
	var result any
	var err error 
	switch req.Method {
	case methodSessionsList:
		result, err = h.onHandleHistoriesList(ctx, req.Params)
	case methodSessionGet:
		result, err = h.onHandleHistoryGet(ctx, req.Params)
	case methodEventsList:
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

func (h *jsonrpcHandler) onHandleHistoriesList(ctx context.Context, raw json.RawMessage) (*v1.ListA2AHistoriesResponse, error) {
	var query *v1.ListA2AHistoriesRequest
	if err := json.Unmarshal(raw, &query); err != nil {
		return nil, errInvalidParams
	}
	return h.service.ListA2AHistories(ctx, query)
}

func (h *jsonrpcHandler) onHandleHistoryGet(ctx context.Context, raw json.RawMessage) (*v1.A2AHistory, error) {
	var query *v1.GetA2AHistoryRequest
	if err := json.Unmarshal(raw, &query); err != nil {
		return nil, errInvalidParams
	}
	return h.service.GetA2AHistory(ctx, query)
}

func (h *jsonrpcHandler) onHandleEventsList(ctx context.Context, raw json.RawMessage) (*v1.ListEventsResponse, error) {
	var query *v1.ListEventsRequest
	if err := json.Unmarshal(raw, &query); err != nil {
		return nil, errInvalidParams
	}
	return h.service.ListEvents(ctx, query)
}

func (h *jsonrpcHandler) writeJSONRPCError(ctx context.Context, rw http.ResponseWriter, err error, reqID any) {
	resp := jsonrpcResponse{JSONRPC: version, Error: err.Error(), ID: reqID}
	if err := json.NewEncoder(rw).Encode(resp); err != nil {
		log.Fatal(ctx, "failed to send error response", err)
	}
}

func NewJSONRPCHandler(service Service) http.Handler {
	return &jsonrpcHandler{
		service: service,
	}
}