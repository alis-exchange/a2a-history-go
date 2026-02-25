# A2A History Extension Specification

## 1. Introduction

### 1.1 Overview

This document desribes the `a2a-history` extension. The extension standardises how A2A clients retrieve, list, and manage historical interaction data produced by A2A agents. By providing a structured way to access past histories and events, clients can implement features like "conversation cistory" and "conversation resumption" across different client implementations. 

### 1.2 Motivation

The core A2A protocol excels at real-time streaming and task execution. However, it does not natively define how a client should discover or reconstruct past interaction contexts once they have ended or a client has disconnected from them. Resumption of interaction contexts are also typically tightly coupled to the client channel where the interaction context was initiated. 

The `a2a-history` extension addresses this by:

- Standardising Retrieval: Providing a uniform set of RPC methods to fetch historical, persisted event payloads.

- Decoupling Event Storage from Agent Execution: Allowing agents to offer history independently of the active task stream.

- Providing Cross-Channel Continuity: Enabling multiple client channels to inspect and sync with ongoing interaction contexts simultaneously. Important to note here is that the extension aims to achieve this decoupling of context persistence and interaction from any one particular client channel, while still allowing different client channels to deliver opinionated UI experiences. In fact, most clients / UI experiences will perform post-processing of the raw persistent layer exposed by the extension to customise the particular channel-specific consumer experience.

### 1.3 Extension URI

The URI for this extension is:

`https://github.com/alis-exchange/a2a-history-go/alis/a2a/extension/history/v1`. 

The following is sample of an AgentCard advertising support for this extension:

```json
{
  "name": "My agent",
  "description": "My agent which supports the a2a-history extension",
  "capabilities": {
    "extensions": [
      {
        "uri": "https://github.com/alis-exchange/a2a-history-go/alis/a2a/extension/history/v1/spec.md",
        "description": "",
        "required": false,
        "params": {
            "a2a_extension_history_v1_agent_id": "some unique identifier (this is optional)"
        },
      }
    ]
  }
}
```

Note: Details on the expected use of 'a2a_extension_history_v1_agent_id' parameter will be covered further [below](#3-method-definitions).

## 2. Resource Model and Schema

The extension introduces a hierarchical resource model.

### A2AHistory 
A lightweight collection representing a unique interaction context. This is tightly coupled to the A2A 'context_id' field.

```proto
// Represents a collection of events tied to a specific context.
message A2AHistory {
    // The unique identifier for this history collection.
    string context_id = 1;
    // The ID of the agent to which this collection belongs.
    string agent_id = 2;
    // Timestamp when the collection was initiated.
    google.protobuf.Timestamp create_time = 3;
    // Summary or title of the collection (optional).
    string display_name = 4;
}

```

### A2AHistoryEvent
An immutable record of a specific A2A event payload (`Task`, `Message`, `TaskStatusUpdateEvent`, `TaskArtifactUpdateEvent`) emitted by an agent within an interaction context and stored within a given A2AHistory collection.

```proto

message A2AHistoryEvent {
    // The resource name of the event.
    // a2ahistories/{context_id}/events/{event_id}
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

#### `history/list`

This method allows the client to retrieve a paginated list of available interaction histories. Clients would typically use this method to discover the set of available histories made available by the agent. 

By design, the `history/list` method (and `history/events/list` below) accomodates for two architectural scenarios with respect to the resource persistence layer:

1. An agent has a dedicated storage layer responsible only for storing events relevant to itself. In this scenario, any returned histories are guarenteed to belong to the agent. 
2. An agent shares a central event storage layer (together with other agents), in which case the agent must be uniquely identified in order to retrieve only relevant histories. 

In the latter case, the 'AgentCard' MUST expose the 'a2a_extension_history_v1_agent_id' parameter so that clients can correctly populate the 'agent_id'. In the simpler case, the 'agent_id' field can be left empty and the 'a2a_extension_history_v1_agent_id' parameter can be ommited or left empty. Implementors of the storage persistence layer are responsible for deciding if the 'agent_id' field is required and returning appropriate errors. 

```proto
message ListA2AHistoriesRequest {
    // Reserved.
    reserved 1;
    // The maximum number of histories to return.
    int32 page_size = 2;
    // A page token to retrieve the next page of results.
    string page_token = 3;
    // Optional filtering of histories by unique agent identifier.
    string agent_id = 4;
    // A read mask to specify which fields to return.
    // If not specified, view will determine the fields returned.
    google.protobuf.FieldMask read_mask = 5;
}
```

#### `history/events/list`

This method allows the client to retrieve a paginated list of all events associated with a specific history. After identifying the relevant parent history, clients will typically use this method to retrieve all of the available events, perform any processing logic and allow rendering of results. 

```proto
message ListEventsRequest {
    // The name of the parent history collection.
    // Format: a2ahistories/{context_id}
    string parent = 1;
    // The maximum number of events to return.
    int32 page_size = 2;
    // A page token to retrieve the next page of results.
    string page_token = 3;
    // A read mask to specify which fields to return.
    google.protobuf.FieldMask read_mask = 4;
}

message ListEventsResponse {
    // The list of events.
    repeated A2AHistoryEvent events = 1;
    // A token to retrieve the next page of results.
    string next_page_token = 2;
}
```