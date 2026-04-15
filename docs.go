// Package scheduler exposes the public root import for the A2A scheduler extension.
//
// The package is intentionally small: it provides the top-level constants and
// registration helpers most agents need in order to expose the scheduler
// extension without importing each transport package directly.
//
// # Package layout
//
// The root package is typically used together with these subpackages:
//
//   - `service` provides the built-in Spanner-backed scheduler implementation.
//   - `handler` provides the HTTP callback that Cloud Scheduler and
//     Cloud Tasks post to when a cron should execute.
//   - `jsonrpc` provides the scheduler management API over HTTP
//     JSON-RPC.
//   - `a2asrv` provides the Agent Card extension metadata that
//     advertises scheduler support.
//
// # Standard wiring
//
// A typical integration looks like this:
//
//  1. Construct a concrete scheduler service, usually with
//     `service.NewSchedulerService`.
//  2. Register that service on gRPC with [RegisterGRPC].
//  3. Mount the execution callback and JSON-RPC management API with
//     [RegisterHTTP].
//  4. Expose `a2asrv.AgentExtension` on the Agent Card.
//
// [RegisterHTTP] mounts two canonical HTTP routes by default:
//
//   - [HandlerPath] for scheduled execution callbacks.
//   - [JSONRPCPath] for scheduler resource management.
//
// Call [WithHandlerOptions] or [WithJSONRPCOptions] to configure those
// handlers, or [WithoutHandler] / [WithoutJSONRPC] when only one surface should
// be mounted.
//
// # Identity and transport expectations
//
// The built-in scheduler service authorizes requests from the caller identity
// stored in request context. For gRPC requests this normally means wiring
// `service.UnaryServerInterceptor` so incoming iam/v3 metadata is promoted into
// context before service methods run. The cron execution handler also uses an
// iam/v3 identity for its local scheduler service calls and forwards the cron
// owner to the downstream agent over outgoing gRPC metadata.
//
// Callers that already hold a concrete scheduler service instance can also use
// its RegisterGRPC and RegisterHTTP methods directly; the root helpers simply
// avoid importing the generated protobuf package and transport subpackages in
// the common case.
package scheduler
