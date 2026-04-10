package service

import "go.alis.build/a2a/extension/history/a2asrv"

// NewInterceptor returns an A2A call interceptor that records thread history using this service.
func (s *ThreadService) NewInterceptor(opts ...a2asrv.InterceptorOption) *a2asrv.Interceptor {
	return a2asrv.NewInterceptor(s, opts...)
}
