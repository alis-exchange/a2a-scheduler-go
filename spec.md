# A2A Scheduler Extension Specification

## 1. Introduction

### 1.1 Overview

This document describes the `a2a-scheduler` extension. The extension standardises how A2A agents schedule, manage, and execute tasks at specific times or intervals. By providing a structured way to manage schedules (Crons), clients and agents can implement features like "daily summaries," "reminders," and "scheduled reporting" consistently across the A2A ecosystem.

### 1.2 Motivation

While the core A2A protocol handles real-time interactions, many agent use cases require asynchronous or deferred execution. Currently, agents must implement their own bespoke scheduling logic, making it difficult for clients to inspect, modify, or manage these schedules.

The `a2a-scheduler` extension addresses this by:

- **Standardising Schedule Management**: Providing a uniform set of RPC methods to create, list, update, and delete scheduled tasks.
- **Supporting Recurring and One-time Tasks**: Decoupling the scheduling intent (CRON expressions vs. specific timestamps) from the execution engine.
- **Enabling Cross-Agent Consistency**: Allowing clients to manage schedules across different agents using a single, well-defined interface.
- **Integrating with A2A Messaging**: When a schedule triggers, the extension standardises the delivery of a pre-configured prompt back to the agent, effectively "waking it up" to perform a task.

### 1.3 Extension URI

The URI for this extension is:

`https://a2a.alis.build/extensions/scheduler/v1`.

The following is a sample of an AgentCard advertising support for this extension:

```json
{
  "name": "My agent",
  "description": "My agent which supports the a2a-scheduler extension",
  "capabilities": {
    "extensions": [
      {
        "uri": "https://a2a.alis.build/extensions/scheduler/v1",
        "description": "Enables agents to schedule and manage CRON tasks",
        "required": false
      }
    ]
  }
}
```

## 2. Resource Model and Schema

The extension introduces the `Cron` resource.

### Cron

Represents a scheduled task, which can be either recurring (using a CRON expression) or scheduled for a single point in time.

```protobuf
// A Cron resource representing a scheduled CRON job.
message Cron {
    // The resource name of the Cron.
    // Format: crons/{cron_id}
    string name = 1;
    // The prompt payload that the agent will be invoked with when the Cron runs.
    string prompt = 2;
    // The unix-cron string format expression (* * * * *) for recurring jobs.
    // Required when using type='TYPE_CRON'.
    // See https://docs.cloud.google.com/scheduler/docs/configuring/cron-job-schedules for details.
    string expr = 3;
    // Timezone to be used in interpreting the cron expr. Must exist within the tz database. 
    // Required when using type='TYPE_CRON'.
    // See https://en.wikipedia.org/wiki/List_of_tz_database_time_zones for list of valid values.
    // Example: Europe/London, America/New_York, etc.
    string timezone = 4;
    // The specified (once-off) timestamp for when this Cron will be invoked.
    // Required when using type='TYPE_AT'.
    google.protobuf.Timestamp at = 5;
    // The Cron type.
    Type type = 6;
    // Cron owner. Used for 'on-behalf-of' when invoking agent.
    // Format: users/*
    string owner = 7;
    // Cron owner email. Used in combination with owner for 'on-behalf-of'
    // E.g. me@email.com
    string email = 8;

    // When this Cron was created.
    google.protobuf.Timestamp create_time = 98;
    // When this Cron was last updated.
    google.protobuf.Timestamp update_time = 99;

     // Cron Type definition.
    enum Type {
        // Unspecified type
        TYPE_UNSPECIFIED = 0;
        // CRON type. Must be used for tasks that run on a recurring schedule.
        TYPE_CRON = 1;
        // AT type. Must be used for once-off, non-recurring tasks that run at a specified time only.
        TYPE_AT = 2;
    }
}
```

## 3. Method Definitions

The extension introduces a set of RPC methods for managing Crons. Agents that support the extension MUST expose these methods.

#### `ListCrons`

This method allows the client to retrieve a paginated list of Crons. Results are typically filtered by the requester's identity or IAM permissions.

```protobuf
message ListCronsRequest {
    // The maximum number of crons to return.
    int32 page_size = 1;
    // A page token to retrieve the next page of results.
    string page_token = 2;
}

message ListCronsResponse {
    // The list of crons.
    repeated Cron crons = 1;
    // A token to retrieve the next page of results.
    string next_page_token = 2;
}
```

#### `GetCron`

This method allows the client to retrieve a specific Cron by its resource name.

```protobuf
message GetCronRequest {
    // The resource name of the cron to retrieve.
    // Format: crons/{cron_id}
    string name = 1;
}
```

#### `CreateCron`

This method allows the client to create a new Cron.

```protobuf
message CreateCronRequest {
    // The cron resource to create.
    Cron cron = 1;
}
```

#### `UpdateCron`

This method allows the client to update an existing Cron.

```protobuf
message UpdateCronRequest {
    // The cron resource to update.
    Cron cron = 1;
    // The update mask applies to the resource.
    google.protobuf.FieldMask update_mask = 2;
}
```

#### `DeleteCron`

This method allows the client to delete an existing Cron.

```protobuf
message DeleteCronRequest {
    // The resource name of the cron to delete.
    // Format: crons/{cron_id}
    string name = 1;
}
```

#### `RunCron`

This method allows the client to manually trigger a Cron execution immediately, regardless of its schedule.

```protobuf
message RunCronRequest {
    // The unique ID of the cron to run.
    string id = 1;
}
```
