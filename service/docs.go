// Package service provides [SpannerService], the built-in Google Cloud Spanner implementation for
// persisting and managing A2A scheduler crons.
//
// # SpannerService
//
// [NewSpannerService] opens a Spanner client, Cloud Scheduler client, Cloud Tasks client, and
// configures an IAM authorizer with two roles:
//
//   - roles/open - anonymous CreateCron and ListCrons (see code for exact RPC names).
//   - roles/cron.owner - GetCron, UpdateCron, DeleteCron, and RunCron for callers bound on the cron.
//
// [SpannerService.RegisterGRPC] and [SpannerService.RegisterHTTP] mount the gRPC and HTTP
// transports without requiring callers to import the generated protobuf package, handler,
// or jsonrpc subpackages directly.
//
// Cron names must match `^crons/[a-z0-9-]{2,50}$`. Recurring crons are materialized as Cloud Scheduler
// jobs; one-time crons are materialized as Cloud Tasks. Cron metadata and IAM policy are stored in Spanner.
//
// # Code flow (SpannerService)
//
//	CreateCron: authorize open RPC -> validate -> create Cloud Scheduler job or Cloud Task -> persist cron + IAM policy.
//	GetCron / UpdateCron / DeleteCron / RunCron: authorize -> validate -> read cron policy -> check RPC permission -> act on backing resource.
//	ListCrons: authorize open RPC -> query cron table (optionally filter by policy member).
package service
