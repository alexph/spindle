# Spindle Protocol

## Purpose

The Spindle protocol is a websocket-first, bidirectional command and event protocol between the Spindle server and polyglot workers. A worker connects once, authenticates with a shared secret, registers its functions, receives execution requests, and reports execution state back over the same connection.

This document is a message-spec level design. It defines the lifecycle, message categories, and core payload shapes needed to build the server and SDKs without freezing every field, status code, or error taxonomy.

## Core Entities

- `Worker`: a connected process that authenticates, registers functions, emits events, and executes work locally through an SDK.
- `Function`: a named unit of work identified by a stable function ID and associated options.
- `Trigger`: a structured description of an input source or event shape that can create internal events.
- `Command`: an instruction sent over the websocket that asks the peer to perform or acknowledge an action.
- `Event`: a reported fact or state transition sent over the websocket or recorded internally.
- `Execution`: a single dispatch instance for a function or workflow step.
- `FunctionRef`: the binding between a connected worker session and a registered function definition.

## Transport And Security

- Transport is websocket for all worker/server runtime communication.
- Both the server and workers are configured with the same secure secret key.
- Authentication happens at connection start before capability registration is accepted.
- Registration traffic and execution traffic share the same websocket session.

## Connection Lifecycle

1. A worker opens a websocket connection to the Spindle server.
2. The worker authenticates with the shared secret and worker metadata.
3. The server accepts the session and creates a live `Worker` record in the registry.
4. The worker registers one or more `Function` definitions, creating worker-owned `FunctionRef` records.
5. The server dispatches eligible executions to matching workers over the same connection.
6. The worker reports execution progress and terminal outcomes back as protocol events or command responses.
7. If the worker disconnects, the server expires its presence lease, removes stale `FunctionRef` ownership, and re-evaluates any affected in-flight executions.

## Message Categories

The protocol should use a small set of message categories:

- `authenticate`: sent by the worker to prove possession of the shared secret and describe the worker.
- `registered`: sent by the server to confirm accepted worker or function registration.
- `register_function`: sent by the worker to declare a function ID, metadata, and execution options.
- `emit_event`: sent by the worker or capability adapter to submit an ephemeral event into the shared model.
- `execution_request`: sent by the server to assign an execution to a worker-held `FunctionRef`.
- `execution_update`: sent by the worker to report accepted, running, progress, blocked, retried, deferred, completed, or failed states.
- `ack`: positive acceptance of a command or execution request.
- `nack`: explicit refusal or failure to accept a command or execution request.
- `disconnect_notice`: optional terminal message to support graceful cleanup.

Exact message names may change in implementation, but the protocol must preserve these roles.

## Worker Registration Flow

Worker registration should establish a durable relationship between a live connection and the capabilities it owns:

1. Worker sends `authenticate` with worker metadata and proof derived from the shared secret.
2. Server validates authentication and marks the session active.
3. Worker sends `register_function` for each function it can execute.
4. Server responds with `registered` acknowledgements and creates `FunctionRef` records scoped to that worker session.

The server should reject registration attempts that arrive before authentication or that conflict with protocol invariants. Event emission is separate from registration and should remain available only after authentication.

## Function Shape

At the protocol level, a function registration should include:

- `function_id`: the stable logical ID used for dispatch and concurrency control.
- `worker_id` or equivalent session identity: the worker claiming the registration.
- `ref_id`: a connection-scoped identifier for this specific function reference.
- `options`: execution metadata such as concurrency hints, retry hints, idempotency expectations, timeout expectations, tags, or routing metadata.
- `trigger_bindings` when needed: a description of which triggers or inputs map to the function.

The callable itself never crosses the protocol. Spindle coordinates and dispatches work; the client SDK owns local execution of the callable and reporting of resulting state.

## Trigger And Event Shape

`Trigger` should remain a first-class protocol concept, but not a long-lived registered resource by default. In the initial design:

- a trigger describes the shape, source, or semantics of an event
- a trigger may appear as metadata on a function definition or inside an emitted event payload
- a trigger does not create worker-owned lifecycle state unless a future capability requires durable subscriptions

This keeps the protocol small. Persistent registration is for executable functions. Ephemeral user-land signals, HTTP ingress, schedule firings, and queue deliveries should enter the system as emitted events.

An emitted event should be able to carry:

- `event_id`: a unique identity for the ingress event
- `trigger`: the event shape or source descriptor
- `payload`: user data or ingress payload
- `source`: origin metadata such as worker, queue, route, schedule, or stream partition
- `timestamp`: creation or delivery time
- `routing metadata`: optional tags, partition keys, tenant keys, or other dispatch hints

## Execution Flow

The core dispatch loop should look like this:

1. A capability adapter, worker-originated event, or workflow transition appends an internal event.
2. The dispatcher resolves that event to one or more eligible `Function` targets.
3. The dispatcher selects an available `FunctionRef` while enforcing concurrency and rate limits.
4. The server sends `execution_request` to the chosen worker connection.
5. The worker responds with an initial acceptance state such as `ack`, `nack`, or a richer execution update.
6. The worker emits follow-up `execution_update` messages as execution progresses.
7. The server records each update and decides whether to complete, retry, defer, fail, or reschedule the execution.

## Execution State Vocabulary

`ack` and `nack` are the minimum protocol outcomes, but the execution model needs richer intermediate states. The initial docs should treat the following as the expected vocabulary:

- `accepted`: worker has received and accepted the execution.
- `running`: local execution has started.
- `progress`: optional non-terminal progress or heartbeat update.
- `deferred`: execution is intentionally delayed and should be reconsidered later.
- `retry`: execution failed in a retriable way and should be re-enqueued under server policy.
- `completed`: execution finished successfully.
- `failed`: execution finished unsuccessfully and is terminal under current policy.
- `rejected`: worker could not or would not accept the work, equivalent to a richer `nack`.

The exact final taxonomy can be refined later, but the protocol should clearly support non-terminal states between `ack` and final completion.

## Disconnects And Cleanup

Workers are expected to connect and disconnect over time. The protocol must support scale-up and scale-down without manual cleanup:

- A disconnected worker loses ownership of its live `FunctionRef` records after lease expiry or explicit disconnect handling.
- The server must remove stale worker-scoped registrations without deleting the underlying logical `Function` identity if other workers still advertise it.
- In-flight executions assigned to a disconnected worker must be re-evaluated according to durability and retry policy.
- The worker registry and function registry must remain consistent when multiple workers advertise the same function ID.

## Open Items

- Exact auth payload shape and proof method remain open.
- Exact error codes and negative acknowledgement structure remain open.
- Whether protocol frames are JSON-only or support a second encoding remains open.
- Workflow-specific protocol extensions should be added only if the shared execution model proves insufficient.
- Trigger metadata shape remains open, but trigger lifecycle should stay ephemeral unless a later requirement proves otherwise.
