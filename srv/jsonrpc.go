package srv

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"

	v1 "go.alis.build/common/alis/a2a/extension/scheduler/v1"
	"go.alis.build/a2a/extension/scheduler/service"
	"go.alis.build/alog"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// JSON-RPC 2.0 protocol constants
const (
	version = "2.0"
	// JSON-RPC methods supported by this extension
	methodCreateCron       = "CreateCron"
	methodUpdateCron       = "UpdateCron"
	methodGetCron          = "GetCron"
	methodDeleteCron       = "DeleteCron"
	methodListCrons        = "ListCrons"
	methodRunCron          = "RunCron"
	// SchedulerExtensionPath is the default HTTP path segment for mounting [NewJSONRPCHandler].
	SchedulerExtensionPath = "/extensions/a2ascheduler"
)

var (
	// jsonrpcMarshaler encodes protobuf results in the JSON-RPC envelope (camelCase JSON names,
	// default protojson behavior; EmitUnpopulated so clients see stable field shapes).
	jsonrpcMarshaler = protojson.MarshalOptions{
		UseProtoNames:   false,
		EmitUnpopulated: true,
	}
	// jsonrpcUnmarshaler decodes params into request protos; DiscardUnknown ignores new fields
	// for forward-compatible clients.
	jsonrpcUnmarshaler = protojson.UnmarshalOptions{
		DiscardUnknown: true,
	}
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
	cors    *corsConfig
}

// jsonrpcStream implements grpc.ServerTransportStream to provide the gRPC method name
// to downstream authorizers that rely on it.
type jsonrpcStream struct {
	method string
}

func (s *jsonrpcStream) Method() string { return s.method }
func (s *jsonrpcStream) SetHeader(md metadata.MD) error  { return nil }
func (s *jsonrpcStream) SendHeader(md metadata.MD) error { return nil }
func (s *jsonrpcStream) SetTrailer(md metadata.MD) error { return nil }

// JSONRPCHandlerOption configures [NewJSONRPCHandler].
type JSONRPCHandlerOption func(*jsonrpcHandler)

// ServeHTTP implements JSON-RPC 2.0 over HTTP: optional CORS (including OPTIONS preflight),
// POST-only RPC, decodes the body, validates jsonrpc version and non-empty id, dispatches to
// GetThread / ListThreads / ListThreadEvents, and writes a JSON result (protobuf-as-JSON in result)
// or a JSON-RPC error. Non-POST requests (other than OPTIONS when CORS is enabled) receive
// an invalid-request error.
func (h *jsonrpcHandler) ServeHTTP(rw http.ResponseWriter, req *http.Request) {
	ctx := req.Context()

	// Extract all incoming headers and set them in gRPC metadata so that
	// downstream service calls can use them.
	md := metadata.MD{}
	for k, vs := range req.Header {
		md[strings.ToLower(k)] = vs
	}
	ctx = metadata.NewIncomingContext(ctx, md)

	if h.cors != nil {
		h.cors.writeHeaders(rw)
		if req.Method == http.MethodOptions {
			rw.WriteHeader(http.StatusOK)
			return
		}
	}

	// Validate that is "POST" request
	if req.Method != http.MethodPost {
		h.writeJSONRPCError(ctx, rw, ErrInvalidRequest{err: errors.New("method not allowed")}, nil)
		return
	}

	defer func() {
		if err := req.Body.Close(); err != nil {
			log.Fatal(ctx, "failed to close request body", err)
		}
	}()

	var payload jsonrpcRequest
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		h.writeJSONRPCError(ctx, rw, ErrInvalidRequest{err: err}, payload.ID)
		return
	}

	// Validate payload ID
	if payload.ID == "" {
		h.writeJSONRPCError(ctx, rw, ErrInvalidRequest{err: errors.New("missing request id")}, nil)
		return
	}

	// Validate JSONRPC Version
	if payload.JSONRPC != version {
		h.writeJSONRPCError(ctx, rw, ErrInvalidRequest{err: errors.New("invalid JSON-RPC version")}, payload.ID)
		return
	}

	// Handle the request
	h.handleRequest(ctx, rw, &payload)
}

// handleRequest unmarshals params with [jsonrpcUnmarshaler], calls [service.Service], then either
// marshals the protobuf result with [jsonrpcMarshaler] into the JSON-RPC result field or writes
// an error via [jsonrpcHandler.writeJSONRPCError].
func (h *jsonrpcHandler) handleRequest(ctx context.Context, rw http.ResponseWriter, req *jsonrpcRequest) {
	var result proto.Message
	var err error

	switch req.Method {
	case methodCreateCron:
		result, err = h.onHandleCronCreate(ctx, req.Params)
	case methodGetCron:
		result, err = h.onHandleCronGet(ctx, req.Params)
	case methodUpdateCron:
		result, err = h.onHandleCronUpdate(ctx, req.Params)
	case methodDeleteCron:
		result, err = h.onHandleCronDelete(ctx, req.Params)
	case methodListCrons:
		result, err = h.onHandleCronList(ctx, req.Params)
	case methodRunCron:
		result, err = h.onHandleCronRun(ctx, req.Params)
	case "":
		err = ErrInvalidRequest{err: errors.New("method not found")}
	default:
		err = ErrMethodNotFound{err: errors.New("method not found")}
	}
	if err != nil {
		h.writeJSONRPCError(ctx, rw, err, req.ID)
		return
	}

	if result != nil {
		resultJSON, err := jsonrpcMarshaler.Marshal(result)
		if err != nil {
			h.writeJSONRPCError(ctx, rw, ErrInternalError{err: err}, req.ID)
			return
		}
		resp := jsonrpcResponse{JSONRPC: version, ID: req.ID, Result: json.RawMessage(resultJSON)}
		if err := json.NewEncoder(rw).Encode(resp); err != nil {
			alog.Alertf(ctx, "failed to encode response: %v", err)
		}
	}
}

func (h *jsonrpcHandler) onHandleCronCreate(ctx context.Context, raw json.RawMessage) (*v1.Cron, error) {
	query := &v1.CreateCronRequest{}
	if err := jsonrpcUnmarshaler.Unmarshal(raw, query); err != nil {
		return nil, ErrInvalidParams{err: err}
	}
	ctx = grpc.NewContextWithServerTransportStream(ctx, &jsonrpcStream{method: v1.SchedulerService_CreateCron_FullMethodName})
	return h.service.CreateCron(ctx, query)
}

func (h *jsonrpcHandler) onHandleCronGet(ctx context.Context, raw json.RawMessage) (*v1.Cron, error) {
	query := &v1.GetCronRequest{}
	if err := jsonrpcUnmarshaler.Unmarshal(raw, query); err != nil {
		return nil, ErrInvalidParams{err: err}
	}
	ctx = grpc.NewContextWithServerTransportStream(ctx, &jsonrpcStream{method: v1.SchedulerService_GetCron_FullMethodName})
	return h.service.GetCron(ctx, query)
}

func (h *jsonrpcHandler) onHandleCronUpdate(ctx context.Context, raw json.RawMessage) (*v1.Cron, error) {
	query := &v1.UpdateCronRequest{}
	if err := jsonrpcUnmarshaler.Unmarshal(raw, query); err != nil {
		return nil, ErrInvalidParams{err: err}
	}
	ctx = grpc.NewContextWithServerTransportStream(ctx, &jsonrpcStream{method: v1.SchedulerService_UpdateCron_FullMethodName})
	return h.service.UpdateCron(ctx, query)
}

func (h *jsonrpcHandler) onHandleCronDelete(ctx context.Context, raw json.RawMessage) (proto.Message, error) {
	query := &v1.DeleteCronRequest{}
	if err := jsonrpcUnmarshaler.Unmarshal(raw, query); err != nil {
		return nil, ErrInvalidParams{err: err}
	}
	ctx = grpc.NewContextWithServerTransportStream(ctx, &jsonrpcStream{method: v1.SchedulerService_DeleteCron_FullMethodName})
	return h.service.DeleteCron(ctx, query)
}

func (h *jsonrpcHandler) onHandleCronList(ctx context.Context, raw json.RawMessage) (*v1.ListCronsResponse, error) {
	query := &v1.ListCronsRequest{}
	if err := jsonrpcUnmarshaler.Unmarshal(raw, query); err != nil {
		return nil, ErrInvalidParams{err: err}
	}
	ctx = grpc.NewContextWithServerTransportStream(ctx, &jsonrpcStream{method: v1.SchedulerService_ListCrons_FullMethodName})
	return h.service.ListCrons(ctx, query)
}

func (h *jsonrpcHandler) onHandleCronRun(ctx context.Context, raw json.RawMessage) (*v1.RunCronResponse, error) {
	query := &v1.RunCronRequest{}
	if err := jsonrpcUnmarshaler.Unmarshal(raw, query); err != nil {
		return nil, ErrInvalidParams{err: err}
	}
	ctx = grpc.NewContextWithServerTransportStream(ctx, &jsonrpcStream{method: v1.SchedulerService_RunCron_FullMethodName})
	return h.service.RunCron(ctx, query)
}

func (h *jsonrpcHandler) writeJSONRPCError(ctx context.Context, rw http.ResponseWriter, err error, reqID any) {
	if err == nil {
		return
	}

	var jsonrpcError JSONRPCError
	if st, ok := status.FromError(err); ok {
		jsonrpcError = h.grpcToJSONRPCError(st)
	} else if errors.As(err, &jsonrpcError) {
		// As walks the chain and fills jsonrpcError
	} else {
		jsonrpcError = ErrInternalError{err: err}
	}

	resp := jsonrpcResponse{JSONRPC: version, Error: jsonrpcError.JSONRPCErrorObject(), ID: reqID}
	if err := json.NewEncoder(rw).Encode(resp); err != nil {
		alog.Alertf(ctx, "failed to send error response: %v", err)
	}
}

// grpcToJSONRPCError maps google.golang.org/grpc/status codes from the history [service.Service]
// (for example NotFound, InvalidArgument) to [JSONRPCError] values, using -326xx standard codes
// where they apply and -320xx implementation-defined codes for auth, permission, and not-found.
func (h *jsonrpcHandler) grpcToJSONRPCError(st *status.Status) JSONRPCError {
	switch st.Code() {
	case codes.InvalidArgument:
		return ErrInvalidParams{err: st.Err()}
	case codes.NotFound:
		return ErrNotFound{err: st.Err()}
	case codes.Unauthenticated:
		return ErrUnauthenticated{err: st.Err()}
	case codes.PermissionDenied:
		return ErrPermissionDenied{err: st.Err()}
	case codes.Unimplemented:
		return ErrUnimplemented{err: st.Err()}
	default:
		return ErrInternalError{err: st.Err()}
	}
}

// NewJSONRPCHandler returns an [http.Handler] that implements JSON-RPC 2.0 for the history API
// (ListThreads, GetThread, ListThreadEvents). The service must implement [service.Service].
// Request params and response results are protobuf JSON (protojson): camelCase keys, unknown fields
// ignored on input, unpopulated fields included on output. gRPC status errors from the service are
// mapped to JSON-RPC errors. Pass [WithCORS] (and [CORSAllowOrigin] / [CORSAllowHeaders] / [CORSAllowMethods])
// for browser clients.
func NewJSONRPCHandler(service service.Service, opts ...JSONRPCHandlerOption) http.Handler {
	h := &jsonrpcHandler{service: service}
	for _, o := range opts {
		o(h)
	}
	return h
}
