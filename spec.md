# A2A History Extension Specification

## 1. Introduction

### 1.1 Overview

This document desribes the `a2a-history` extension. The extension standardises how A2A clients retrieve, list, and manage historical interaction data produced by A2A agents. By providing a structured way to access past histories and events, clients can implement features like "conversation history" and "conversation resumption" across different client channels. 

### 1.2 Motivation

The core A2A protocol excels at real-time streaming and task execution. However, it does not natively define how a client should discover or reconstruct past interaction contexts once they have ended or a client has disconnected from them. Resumption of interaction contexts are also typically tightly coupled to the client channel where the interaction context was initiated. 

The `a2a-history` extension addresses this by:

- Standardising Retrieval: Providing a uniform set of RPC methods to fetch historical, persisted event payloads.

- Decoupling Event Storage from Agent Execution: Allowing agents to offer history independently of the active task stream.

- Providing Cross-Channel Continuity: Enabling multiple client channels to inspect and sync with ongoing interaction contexts simultaneously. Important to note here is that the extension aims to achieve this decoupling of context persistence and interaction from any one particular client channel, while still allowing different client channels to deliver opinionated UI experiences. In fact, most clients / UI experiences will perform post-processing of the raw persistent layer exposed by the extension to customise the particular channel-specific consumer experience.

### 1.3 Extension URI

The URI for this extension is:

`https://a2a.alis.build/extensions/history/v1`.

The following is sample of an AgentCard advertising support for this extension:

```json
{
  "name": "My agent",
  "description": "My agent which supports the a2a-history extension",
  "capabilities": {
    "extensions": [
      {
        "uri": "https://a2a.alis.build/extensions/history/v1",
        "description": "",
        "required": false
      }
    ]
  }
}
```

## 2. Resource Model and Schema

The extension introduces a hierarchical resource model.

### Thread
A lightweight collection representing a unique interaction context. This is tightly coupled to the A2A 'context_id' field.

```proto
// Represents a collection of events tied to a specific context.
message Thread {
    // The unique identifier for this thread collection.
    string context_id = 1;
    // The ID of the agent to which this collection belongs.
    string agent_id = 2;
    // Timestamp when the collection was initiated.
    google.protobuf.Timestamp create_time = 3;
    // Summary or title of the collection (optional).
    string display_name = 4;
}

```

### ThreadEvent
An immutable record of a specific A2A event payload (`Task`, `Message`, `TaskStatusUpdateEvent`, `TaskArtifactUpdateEvent`) emitted by an agent within an interaction context and stored within a given Thread collection.

```proto

message ThreadEvent {
    // The resource name of the event.
    // threads/{thread_id}/events/{event_id}
    string name = 1;
    oneof payload {
        // An event indicating an A2A task object.
        Task task = 2;
        // An event indicating an A2A message object.
        Message message = 3;
        // An event indicating an A2A task status update object.
        TaskStatusUpdateEvent status_update = 4;
        // An event indicating a task artifact update object.
        TaskArtifactUpdateEvent artifact_update = 5;
    }

    // When this event was created.
    google.protobuf.Timestamp create_time = 98;
}

```

Note: The resource model naming convention used by this extension delibrately avoids the use of the term 'session'. This is to prevent confusion with the concept of a 'session' typically used to describe and model an agent's own internal event persistence and management (e.g. Vertex AI Agent Engine sessions). These are normally stored independantly of the A2A consumption layer and serve a different purpose.

## 3. Method Definitions

The extension introduces a set of custom RPC methods. Agents that support the extension MUST expose all of these methods.

#### `ListThreads`

This method allows the client to retrieve a paginated list of available interaction histories. Clients would typically use this method to discover the set of available Threads made available by the agent. 

```proto
message ListThreadsRequest {
    // Reserved.
    reserved 1;
    // The maximum number of threads to return.
    int32 page_size = 2;
    // A page token to retrieve the next page of results.
    string page_token = 3;
    // Optional filtering of threads by unique agent identifier.
    string agent_id = 4;
    // A read mask to specify which fields to return.
    // If not specified, view will determine the fields returned.
    google.protobuf.FieldMask read_mask = 5;
}
```

#### `GetThread`

This method allows the client to retrieve a specific Thread.

```proto
message GetThreadRequest {
    // The resource name of the thread to retrieve.
    string name = 1;
    // A read mask to specify which fields to return.
    google.protobuf.FieldMask read_mask = 3;
}
```

#### `ListThreadEvents`

This method allows the client to retrieve a paginated list of all events associated with a specific Thread. After identifying the relevant parent Thread, clients will typically use this method to retrieve all of the available events, perform any processing logic and allow rendering of results. 

```proto
message ListThreadEventsRequest {
    // The name of the parent thread collection.
    // Format: threads/{thread_id}
    string parent = 1;
    // The maximum number of events to return.
    int32 page_size = 2;
    // A page token to retrieve the next page of results.
    string page_token = 3;
    // A read mask to specify which fields to return.
    google.protobuf.FieldMask read_mask = 4;
}

message ListThreadEventsResponse {
    // The list of events.
    repeated A2AHistoryEvent events = 1;
    // A token to retrieve the next page of results.
    string next_page_token = 2;
}
```
