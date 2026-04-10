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
// requests on method-aware muxes.
//
// This path is the HTTP entry point that receives cron events for the agent.
// In production, a job runner such as Google Cloud Scheduler can call this 
// endpoint directly, or enqueue work through Google Cloud Tasks that eventually 
// POSTs to the same path. That request is then handled as a scheduler/cron 
// action by [NewCronHandler].
//
// [HandlerPath] is the canonical route for that flow, so callers that need to
// configure external schedulers, proxies, or documentation should reuse the
// constant instead of copying the raw string.
//
// For routers without method-pattern support, create the handler directly via
// [NewCronHandler] and mount it at [HandlerPath] according to that router's API.
func Register(mux HTTPRegistrar, service pb.SchedulerServiceServer, opts ...Option) {
	// Register the shared cron-event endpoint so external systems post to the
	// same path whether the handler is mounted through this helper or manually.
	mux.Handle("POST "+HandlerPath, NewCronHandler(service, opts...))
}
