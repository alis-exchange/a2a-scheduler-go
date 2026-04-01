// Package handler provides the HTTP handlers for processing scheduled task executions.
//
// The package implements the logic required to receive and process requests from
// external schedulers (such as Cloud Scheduler or Cloud Tasks). When a scheduled
// event is triggered, the handler:
//  1. Receives the execution request at [SchedulerExtensionHandlerPath].
//  2. Fetches the associated task details using the provided [pb.SchedulerServiceServer].
//  3. Authorizes the request and prepares the necessary credentials.
//  4. Invokes the target agent with the configured prompt using the A2A JSON-RPC protocol.
//
// The primary entry point for this package is [NewCronHandler].
package handler
