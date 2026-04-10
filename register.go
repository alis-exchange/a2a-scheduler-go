package scheduler

import (
	"net/http"

	"google.golang.org/grpc"

	"go.alis.build/a2a/extension/scheduler/handler"
	"go.alis.build/a2a/extension/scheduler/jsonrpc"
	pb "go.alis.build/common/alis/a2a/extension/scheduler/v1"
)

const (
	// HandlerPath is the canonical HTTP callback path used for scheduled cron execution.
	HandlerPath = handler.HandlerPath
	// JSONRPCPath is the canonical HTTP path used for the scheduler JSON-RPC management API.
	JSONRPCPath = jsonrpc.SchedulerJsonRpcExtensionPath
)

// HTTPRegistrar is implemented by routers that support net/http-style handler registration.
type HTTPRegistrar interface {
	Handle(pattern string, handler http.Handler)
}

// HTTPOption configures [RegisterHTTP].
type HTTPOption func(*httpConfig)

type httpConfig struct {
	registerHandler bool
	registerJSONRPC bool
	handlerOpts     []handler.Option
	jsonrpcOpts     []jsonrpc.JSONRPCHandlerOption
}

func defaultHTTPConfig() httpConfig {
	return httpConfig{
		registerHandler: true,
		registerJSONRPC: true,
	}
}

// WithHandlerOptions forwards options to [handler.Register] when [RegisterHTTP] mounts
// the cron execution callback at [HandlerPath].
func WithHandlerOptions(opts ...handler.Option) HTTPOption {
	return func(cfg *httpConfig) {
		cfg.handlerOpts = append(cfg.handlerOpts, opts...)
	}
}

// WithJSONRPCOptions forwards options to [jsonrpc.Register] when [RegisterHTTP] mounts
// the scheduler JSON-RPC management API at [JSONRPCPath].
func WithJSONRPCOptions(opts ...jsonrpc.JSONRPCHandlerOption) HTTPOption {
	return func(cfg *httpConfig) {
		cfg.jsonrpcOpts = append(cfg.jsonrpcOpts, opts...)
	}
}

// WithoutHandler skips registration of the cron execution callback.
func WithoutHandler() HTTPOption {
	return func(cfg *httpConfig) {
		cfg.registerHandler = false
	}
}

// WithoutJSONRPC skips registration of the scheduler JSON-RPC management API.
func WithoutJSONRPC() HTTPOption {
	return func(cfg *httpConfig) {
		cfg.registerJSONRPC = false
	}
}

// RegisterGRPC wires the scheduler service into a gRPC server or any other ServiceRegistrar.
func RegisterGRPC(registrar grpc.ServiceRegistrar, service pb.SchedulerServiceServer) {
	pb.RegisterSchedulerServiceServer(registrar, service)
}

// RegisterHTTP mounts the scheduler HTTP surfaces on a method-aware mux.
//
// By default this registers both:
// - the cron execution callback at [HandlerPath], used by Cloud Scheduler or Cloud Tasks
// - the JSON-RPC management API at [JSONRPCPath], used by clients managing cron resources
//
// Use [WithHandlerOptions] and [WithJSONRPCOptions] to configure the underlying handlers,
// or [WithoutHandler] / [WithoutJSONRPC] to suppress one of the routes.
func RegisterHTTP(mux HTTPRegistrar, service pb.SchedulerServiceServer, opts ...HTTPOption) {
	cfg := defaultHTTPConfig()
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}

	if cfg.registerHandler {
		handler.Register(mux, service, cfg.handlerOpts...)
	}
	if cfg.registerJSONRPC {
		jsonrpc.Register(mux, service, cfg.jsonrpcOpts...)
	}
}
