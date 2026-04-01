// Package service provides the core implementation of the Scheduler service.
//
// This package contains the [SpannerService], which implements the [pb.SchedulerServiceServer]
// interface. It manages the lifecycle of Crons (scheduled tasks) using Google Cloud Spanner
// for persistence and leverages Google Cloud Scheduler and Google Cloud Tasks for the
// actual execution of scheduled events.
//
// The service supports:
//   - Recurring tasks via CRON expressions (using Cloud Scheduler).
//   - One-time tasks at a specific point in time (using Cloud Tasks).
//   - Resource-level IAM authorization for managing Crons.
//   - Integration with the A2A (Agent-to-Agent) ecosystem for triggering agent actions.
//
// The primary entry point for this package is [NewSpannerService].
package service
