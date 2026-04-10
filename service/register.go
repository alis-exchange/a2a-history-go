package service

import (
	"net/http"

	"go.alis.build/a2a/extension/history/jsonrpc"
	pb "go.alis.build/common/alis/a2a/extension/history/v1"
	"google.golang.org/grpc"
)

// HTTPRegistrar is implemented by routers that support net/http-style handler registration.
type HTTPRegistrar interface {
	Handle(pattern string, handler http.Handler)
}

// RegisterGRPC wires ThreadService into a gRPC server or any other ServiceRegistrar.
func (s *ThreadService) RegisterGRPC(registrar grpc.ServiceRegistrar) {
	pb.RegisterThreadServiceServer(registrar, s)
}

// RegisterHTTP mounts the history JSON-RPC API on a method-aware mux.
func (s *ThreadService) RegisterHTTP(mux HTTPRegistrar, opts ...jsonrpc.JSONRPCHandlerOption) {
	jsonrpc.Register(mux, s, opts...)
}
