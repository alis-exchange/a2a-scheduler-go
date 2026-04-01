// Package jsonrpc implements a JSON-RPC 2.0 interface for the Scheduler service.
//
// This package provides an [http.Handler] that dispatches JSON-RPC requests to an
// underlying [pb.SchedulerServiceServer]. It handles the translation between
// JSON-RPC 2.0 protocol envelopes and protobuf-based service calls, including
// error mapping from gRPC status codes to JSON-RPC error objects.
//
// Key features:
//   - Full support for JSON-RPC 2.0 protocol.
//   - Mapping of standard Scheduler service methods (CreateCron, GetCron, etc.).
//   - Integration with gRPC metadata and context for downstream authorization.
//   - Optional CORS support for browser-based clients.
//   - Protobuf-aware JSON marshaling and unmarshaling.
//
// The primary entry point is [NewJSONRPCHandler].
package jsonrpc
