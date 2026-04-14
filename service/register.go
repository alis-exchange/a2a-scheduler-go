package service

import (
	"net/http"

	"go.alis.build/a2a/extension/scheduler/handler"
	"go.alis.build/a2a/extension/scheduler/jsonrpc"
	pb "go.alis.build/common/alis/a2a/extension/scheduler/v1"
	"go.alis.build/iam/v3"
	"google.golang.org/grpc"
)

// HTTPRegistrar is implemented by routers that support net/http-style handler registration.
type HTTPRegistrar interface {
	Handle(pattern string, handler http.Handler)
}

// HTTPOption configures [SchedulerService.RegisterHTTP].
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

// WithHandlerOptions forwards options to [handler.Register] when [SchedulerService.RegisterHTTP]
// mounts the cron execution callback at [handler.HandlerPath].
func WithHandlerOptions(opts ...handler.Option) HTTPOption {
	return func(cfg *httpConfig) {
		cfg.handlerOpts = append(cfg.handlerOpts, opts...)
	}
}

// WithJSONRPCOptions forwards options to [jsonrpc.Register] when [SchedulerService.RegisterHTTP]
// mounts the scheduler JSON-RPC management API at [jsonrpc.SchedulerJsonRpcExtensionPath].
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

// RegisterGRPC wires SchedulerService into a gRPC server or any other ServiceRegistrar.
func (s *SchedulerService) RegisterGRPC(registrar grpc.ServiceRegistrar) {
	pb.RegisterSchedulerServiceServer(registrar, s)
}

// UnaryServerInterceptor returns the IAM v3 interceptor that attaches an already-authenticated
// transport identity to inbound gRPC calls before service authorization runs.
func UnaryServerInterceptor(resolve iam.GRPCAuthenticatedIdentityResolver) grpc.UnaryServerInterceptor {
	return iam.UnaryServerInterceptor(resolve)
}

// RegisterHTTP mounts the scheduler HTTP surfaces on a method-aware mux.
func (s *SchedulerService) RegisterHTTP(mux HTTPRegistrar, opts ...HTTPOption) {
	cfg := defaultHTTPConfig()
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}

	if cfg.registerHandler {
		handler.Register(mux, s, cfg.handlerOpts...)
	}
	if cfg.registerJSONRPC {
		jsonrpc.Register(mux, s, cfg.jsonrpcOpts...)
	}
}
