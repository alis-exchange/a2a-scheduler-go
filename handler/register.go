package handler

import (
	"net/http"

	pb "go.alis.build/common/alis/a2a/extension/scheduler/v1"
)

const (
	// Endpoint for handling scheduled invocations
	HandlerPath = "/alis.a2a.extension.v1.SchedulerService/handler"
)

// HTTPRegistrar is implemented by routers that support net/http-style handler registration.
//
// Register uses method-aware patterns such as "POST /path", so this helper is intended for
// routers that support that form, including the Go 1.22+ ServeMux.
type HTTPRegistrar interface {
	Handle(pattern string, handler http.Handler)
}

// Register mounts the scheduler execution handler at [HandlerPath] for POST
// requests on method-aware muxes. For routers without method-pattern support, create the handler
// directly via [NewCronHandler] and mount it according to that router's API.
func Register(mux HTTPRegistrar, service pb.SchedulerServiceServer, opts ...Option) {
	mux.Handle("POST "+HandlerPath, NewCronHandler(service, opts...))
}
