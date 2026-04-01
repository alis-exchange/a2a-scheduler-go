# A2A SCHEDULER GO SDK

[![License](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](LICENSE)

This project contains a lightweight Go library for developers supporting the [a2a-scheduler](spec.md) A2A extension.

## Features

- **Integration with the official [A2A Go SDK](https://github.com/a2aproject/a2a-go/tree/main):** Builds on top of the official library for building A2A-compliant agents in Go.
- **Built-in persistence:** Includes a Google Cloud Spanner-backed [`service.SpannerService`](service/spanner.go) which leverages Google Cloud Scheduler and Cloud Tasks for execution.
- **Agent Extension support:** An [`a2asrv.AgentExtension`](a2asrv/extension.go) to advertise support for the scheduler extension.
- **HTTP handler:** A [`handler`](handler/) for receiving and processing scheduled task executions from Cloud Scheduler or Cloud Tasks.
- **JSON-RPC handler:** A [`jsonrpc`](jsonrpc/) HTTP handler for managing crons from clients.

## Packages

| Package                                                     | Role                                                                                                                                                                                                                                                                                         |
| ----------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| [`go.alis.build/a2a/extension/scheduler/service`](service/) | [`SpannerService`](service/spanner.go) and [`NewSpannerService`](service/spanner.go) for the built-in Google Cloud Spanner + IAM implementation, integrating with Cloud Scheduler and Cloud Tasks.                                                                                         |
| [`go.alis.build/a2a/extension/scheduler/a2asrv`](a2asrv/)   | [`AgentExtension`](a2asrv/extension.go) ([`a2a.AgentExtension`](https://pkg.go.dev/github.com/a2aproject/a2a-go/v2/a2a#AgentExtension)) for advertising extension support.                                                                                                                    |
| [`go.alis.build/a2a/extension/scheduler/handler`](handler/) | [`NewCronHandler`](handler/handler.go) for processing incoming execution requests at [`SchedulerExtensionHandlerPath`](handler/handler.go).                                                                                                                                                  |
| [`go.alis.build/a2a/extension/scheduler/jsonrpc`](jsonrpc/) | [`NewJSONRPCHandler`](jsonrpc/jsonrpc.go) with options such as [`WithCORS`](jsonrpc/cors.go), plus JSON-RPC error mapping ([`errors.go`](jsonrpc/errors.go)).                                                                                                                                 |

Package-level documentation (design, IAM roles, execution flow) lives in [`service/docs.go`](service/docs.go), [`a2asrv/docs.go`](a2asrv/docs.go), [`handler/docs.go`](handler/docs.go), and [`jsonrpc/docs.go`](jsonrpc/docs.go). Run `go doc -all ./...` locally for the full commentary.

## Architecture (high level)

```mermaid
flowchart TD
  subgraph client [Client / UI]
    C[Browser / Tool]
  end
  subgraph agent [A2A server]
    J[JSON-RPC handler]
    S[service.SpannerService]
    H[handler.CronHandler]
  end
  subgraph gcp [GCP Services]
    CS[Cloud Scheduler]
    CT[Cloud Tasks]
    DB[(Spanner)]
  end
  subgraph target [Target Agent]
    TA[Agent RPC]
  end

  C -->|Create/List Crons| J
  J --> S
  S -->|Persist| DB
  S -->|Schedule| CS
  S -->|Schedule| CT
  CS -->|Trigger| H
  CT -->|Trigger| H
  H -->|Fetch Cron| S
  H -->|SendMessage| TA
```

1. **Management path:** Clients use the JSON-RPC handler to manage `Cron` resources. `SpannerService` persists these in Spanner and synchronizes them with Google Cloud Scheduler (for recurring tasks) or Google Cloud Tasks (for one-time tasks).
2. **Execution path:** When a scheduled time is reached, Cloud Scheduler or Cloud Tasks sends a POST request to the `CronHandler`. The handler verifies the request, retrieves the cron details from the service, and invokes the target agent with the configured prompt using the A2A protocol.

## Installation

```bash
go get -u go.alis.build/a2a/extension/scheduler
```

## Getting started

### Scheduler service

Use the built-in Spanner-backed `SpannerService`:

```go
import (
	"go.alis.build/a2a/extension/scheduler/service"
)

schedulerService, err := service.NewSpannerService(ctx, &service.SpannerServiceConfig{
	SpannerProject:    "SPANNER_PROJECT_ID",
	SchedulingProject: "SCHEDULING_PROJECT_ID",
	SchedulingQueue:   "CLOUD_TASKS_QUEUE_NAME",
	SchedulingRegion:  "GCP_REGION",
	Instance:          "SPANNER_INSTANCE_ID",
	Database:          "SPANNER_DATABASE_ID",
	CronTable:         "CRONS_TABLE_NAME",
	ServiceAccount:    "triggering-sa@project.iam.gserviceaccount.com",
	Audience:          "https://your-agent-endpoint.com",
	TargetUrl:         "https://your-agent-endpoint.com/alis.a2a.extension.v1.SchedulerService/handler",
})
```

Below is Terraform aligned with `SpannerService` expectations (proto columns, IAM).

```hcl
# Cloud Tasks Queue used for scheduling one-time (AT type) tasks.
resource "google_cloud_tasks_queue" "a2a_scheduler_queue" {
  name     = "CLOUD_TASKS_QUEUE_NAME"
  location = "GCP_REGION"
  project  = "SCHEDULING_PROJECT_ID"
}

# Spanner table for persisting Cron resources and their associated IAM policies.
resource "alis_google_spanner_table" "a2a_scheduler_crons" {
  project         = "SPANNER_PROJECT_ID"
  instance        = "SPANNER_INSTANCE_ID"
  database        = "SPANNER_DATABASE_ID"
  name            = "CRONS_TABLE_NAME"
  schema = {
    columns = [
      {
        name           = "key",
        type           = "STRING",
        is_primary_key = true,
        required       = true
      },
      {
        name          = "Cron",
        type          = "PROTO"
        proto_package = "alis.a2a.extension.scheduler.v1.Cron"
        required      = true
      },
      {
        name          = "Policy",
        type          = "PROTO"
        proto_package = "google.iam.v1.Policy"
        required      = true
      },
      {
        name            = "create_time",
        type            = "TIMESTAMP",
        required        = false,
        is_computed     = true,
        computation_ddl = "TIMESTAMP_ADD(TIMESTAMP_SECONDS(Cron.create_time.seconds),INTERVAL CAST(FLOOR(Cron.create_time.nanos / 1000) AS INT64) MICROSECOND)",
        is_stored       = true
      },
    ]
  }
}
```

### Advertising the extension on the Agent Card

Advertise support for the scheduler extension in your Agent Card:

```go
import (
	"github.com/a2aproject/a2a-go/v2/a2a"
	schedulera2asrv "go.alis.build/a2a/extension/scheduler/a2asrv"
)

// Define the Agent Card
agentCard := a2a.AgentCard{
    Capabilities: a2a.AgentCapabilities{
        Extensions: []a2a.AgentExtension{
            schedulera2asrv.AgentExtension,
        },
    },
}
```

### HTTP Handlers

Mount the JSON-RPC handler for management and the Cron handler for executions:

```go
import (
	schedulerhandler "go.alis.build/a2a/extension/scheduler/handler"
	schedulerjsonrpc "go.alis.build/a2a/extension/scheduler/jsonrpc"
)

// JSON-RPC for management (Create, List, etc.)
mux.Handle(schedulerjsonrpc.SchedulerJsonRpcExtensionPath, schedulerjsonrpc.NewJSONRPCHandler(schedulerService))

// Cron handler for triggered executions (e.g. at http://localhost:8000/jsonrpc)
mux.HandleFunc("POST "+schedulerhandler.SchedulerExtensionHandlerPath, schedulerhandler.NewCronHandler("http://localhost:8000/jsonrpc", schedulerService))
```

## Documentation

- See [`service/docs.go`](service/docs.go), [`handler/docs.go`](handler/docs.go), and [`jsonrpc/docs.go`](jsonrpc/docs.go) for detailed method-level flows and IAM roles.
- [Proto definitions](https://github.com/alis-exchange/common-protos/blob/main/alis/a2a/extension/scheduler/v1/scheduler.proto)
- [Generated Go Protobufs](https://github.com/alis-exchange/common-go/tree/main/alis/a2a/extension/scheduler/v1)
