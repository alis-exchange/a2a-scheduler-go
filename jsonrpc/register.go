package jsonrpc

import (
	"net/http"

	pb "go.alis.build/common/alis/a2a/extension/scheduler/v1"
)

var registeredMethods = [...]string{
	http.MethodPost,
	http.MethodOptions,
}

// HTTPRegistrar is implemented by routers that support net/http-style handler registration.
//
// Register uses method-aware patterns such as "POST /path", so this helper is intended for
// routers that support that form, including the Go 1.22+ ServeMux.
type HTTPRegistrar interface {
	Handle(pattern string, handler http.Handler)
}

// Register mounts the scheduler JSON-RPC handler at [SchedulerJsonRpcExtensionPath] for POST requests
// and OPTIONS preflight requests on method-aware muxes. For routers without method-pattern support,
// create the handler directly via [NewJSONRPCHandler] and mount it according to that router's API.
func Register(mux HTTPRegistrar, service pb.SchedulerServiceServer, opts ...JSONRPCHandlerOption) {
	handler := NewJSONRPCHandler(service, opts...)
	for _, method := range registeredMethods {
		mux.Handle(method+" "+SchedulerJsonRpcExtensionPath, handler)
	}
}
