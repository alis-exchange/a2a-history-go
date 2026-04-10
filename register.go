package a2ahistory

import (
	"net/http"

	"go.alis.build/a2a/extension/history/jsonrpc"
	pb "go.alis.build/common/alis/a2a/extension/history/v1"
	"google.golang.org/grpc"
)

const (
	// JSONRPCPath is the canonical HTTP path used for the history JSON-RPC API.
	JSONRPCPath = jsonrpc.HistoryExtensionPath
)

// HTTPRegistrar is implemented by routers that support net/http-style handler registration.
type HTTPRegistrar interface {
	Handle(pattern string, handler http.Handler)
}

// HTTPOption configures [RegisterHTTP].
type HTTPOption func(*httpConfig)

type httpConfig struct {
	jsonrpcOpts []jsonrpc.JSONRPCHandlerOption
}

// WithJSONRPCOptions forwards options to [jsonrpc.Register] when [RegisterHTTP] mounts
// the history JSON-RPC API at [JSONRPCPath].
func WithJSONRPCOptions(opts ...jsonrpc.JSONRPCHandlerOption) HTTPOption {
	return func(cfg *httpConfig) {
		cfg.jsonrpcOpts = append(cfg.jsonrpcOpts, opts...)
	}
}

// RegisterGRPC wires the history service into a gRPC server or any other ServiceRegistrar.
func RegisterGRPC(registrar grpc.ServiceRegistrar, svc pb.ThreadServiceServer) {
	pb.RegisterThreadServiceServer(registrar, svc)
}

// RegisterHTTP mounts the history JSON-RPC API on a method-aware mux.
//
// This registers POST and OPTIONS handlers at [JSONRPCPath]. Use
// [WithJSONRPCOptions] to forward options such as [jsonrpc.WithCORS] to the
// underlying JSON-RPC handler.
func RegisterHTTP(mux HTTPRegistrar, svc pb.ThreadServiceServer, opts ...HTTPOption) {
	cfg := httpConfig{}
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}

	jsonrpc.Register(mux, svc, cfg.jsonrpcOpts...)
}
