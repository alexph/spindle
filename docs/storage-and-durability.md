# Storage And Durability

## Purpose

Spindle coordinates durable execution from a single binary, so the storage model needs to stay small and unified. The goal is to avoid separate persistence stories for queues, RPC, workflows, streaming, and retries by reducing them to one durable execution model.

The recommended v1 direction is:

- SQLite on local disk
- WAL mode enabled
- `sqlx` for database access
- `goose` for Go-managed migrations

## Core Durable Model

The storage model should be built around a few durable primitives:

- `Function`: the logical executable definition
- `FunctionVersion`: a versioned snapshot of function configuration
- `Run`: a durable unit of work or coordination
- `RunChunk`: an append-only record attached to a run
- `WorkerSession`: leased liveness for a connected worker

This keeps the world small:

- a queue delivery can become a `Run`
- an RPC request can become a `Run`
- a workflow instance can become a `Run`
- a stream-driven execution can become a `Run`

Different capabilities should differ mostly by origin metadata and chunk types, not by separate storage abstractions.

## SQLite Direction

SQLite in WAL mode is the recommended v1 backend because it fits the product shape:

- one binary
- local disk durability
- append-heavy write patterns
- simple operator story
- transactional updates for chunk append plus derived-state updates

This is enough for the first implementation without introducing a larger storage surface too early.

## Proposed Tables

The initial storage design should center on these tables:

- `functions`
- `function_versions`
- `runs`
- `run_chunks`
- `worker_sessions`
- `leases`

Additional tables can be introduced later only when the shared model is clearly insufficient.

## `functions`

`functions` stores the logical function identity. It should represent the durable, human-meaningful function rather than a single live worker registration.

Suggested fields:

- `id`
- `current_version_hash`
- `created_at`
- `updated_at`

The function should stay stable across worker reconnects and across multiple workers advertising the same logical ID.

## `function_versions`

Function configuration should be versioned by hashing the serialized configuration object. That object is the same logical payload described in the protocol:

```json
{
  "id": "email.send",
  "label": "Send email to a user",
  "triggers": [],
  "concurrency": [],
  "rate_limit": [],
  "retries": {}
}
```

Suggested fields:

- `function_id`
- `version_hash`
- `config_json`
- `created_at`

This gives Spindle a durable record of what configuration was active when a run was created, without inventing separate tables for every policy subtype on day one.

## `runs`

Everything that behaves like an invocation or durable coordination unit should be modeled as a `Run`.

Examples:

- a queue event handled by a function
- an HTTP or webhook delivery
- an RPC request
- a workflow instance
- a workflow step when step-level tracking is needed

Suggested fields:

- `id`
- `kind`
- `function_id`
- `function_version_hash`
- `source_name`
- `idempotency_id`
- `correlation_id`
- `status`
- `created_at`
- `updated_at`

`kind` should distinguish broad origins such as `event`, `rpc`, `workflow`, or `schedule`, but these should all still use the same run machinery.

## `run_chunks`

`run_chunks` is the append-only history of a run. This is the most important table in the model.

Suggested fields:

- `run_id`
- `position`
- `chunk_type`
- `payload_json`
- `created_at`

Every meaningful transition should append a new chunk rather than mutate a row in place. Expected chunk types include:

- `created`
- `accepted`
- `started`
- `progress`
- `output`
- `deferred`
- `retry_scheduled`
- `completed`
- `failed`

Derived current state can still be cached on `runs.status` for fast dispatch and queries, but the source of truth should be append-only chunks.

## `worker_sessions` And `leases`

Worker connectivity should be modeled as leased liveness, not durable business state.

`worker_sessions` should capture:

- `id`
- `worker_name` or worker identity metadata
- `connected_at`
- `disconnected_at`
- `last_seen_at`

`leases` should capture the active validity window for dispatch decisions:

- `holder_type`
- `holder_id`
- `lease_key`
- `expires_at`

This allows the dispatcher to treat worker presence and function ownership as expiring facts. On reconnect, the worker recreates its live registrations and leases. On expiry, the server cleans up stale ownership and re-evaluates in-flight work.

## Must Be Durable

The initial design should treat these as durable records:

- function definitions and function version hashes
- runs
- run chunks
- workflow progress derived from run chunks
- scheduled or deferred work that must survive restart
- idempotency and correlation data required to safely resume or deduplicate work

Durability is required because workers are expected to disconnect and the server may restart while work is still logically in flight.

## May Be In-Memory With Recovery Rules

These may be kept in memory if they can be rebuilt or safely expired:

- live websocket connections
- in-memory routing maps from function IDs to live worker-held `FunctionRef`s
- transient dispatch caches
- waiter maps used to resolve local RPC promises or futures for currently connected clients

If an in-memory structure affects correctness, the docs should explain how it is reconstructed after restart or disconnect.

## Recovery Model

On restart or failover, the server should be able to:

- rebuild current run state from durable run chunks
- determine which runs were incomplete at the time of failure
- restore scheduled, deferred, or retriable work that should still occur
- treat all worker sessions as non-live until new leases are established
- remove stale `FunctionRef` ownership that depended on expired sessions or leases
- continue to serve idempotent replays without duplicating durable work

The system should favor replay or reconstruction from durable records over ad hoc repair logic.

## Worker Presence And Leases

Worker presence is important for dispatch, but it should not be treated as a durable business fact. A worker session should be modeled as leased liveness:

- active while the websocket is connected and heartbeats or connection state remain valid
- expired after disconnect or lease timeout
- able to recreate its registrations on reconnect

This keeps durable coordination focused on executions and events rather than ephemeral network sessions.

The lease model should be simple:

- dispatch only considers worker-owned `FunctionRef`s backed by an active lease
- disconnect or lease expiry invalidates those placements
- the durable function definition remains, but the live placement disappears until re-registration

## RPC Result Delivery

RPC should use the same durable model as any other run:

- inbound `rpc` creates a `Run`
- progress and outputs append `RunChunk` records
- the final RPC result is just another chunk, typically `output` or `completed`

If the caller is still connected, the server can resolve the waiting promise or future and rebroadcast the result over the websocket. If the caller has disconnected, the durable run history still exists and the result can be recovered or reattached later under future protocol rules.

This keeps RPC from becoming a separate persistence system.

## Subscribers And Cursors

The design should not force every capability into a subscriber or cursor abstraction in v1.

Recommended rule:

- `Run` and `RunChunk` are universal
- subscriber positions or cursors are optional add-on concepts for stream and replay use cases

This avoids overfitting queues, RPC, and workflows to a stream-consumer model they may not all need immediately.

## Cleanup Semantics

Disconnect and restart handling must converge safely:

- stale worker sessions lose their active leases and `FunctionRef` ownership
- logical `Function` identities and version records remain if other workers still advertise them
- in-flight runs assigned to a lost worker are re-evaluated under retry, defer, or failure policy
- pending runs must not be lost simply because the chosen worker disappeared

These rules should be shared between the worker registry, dispatcher, and execution state tracker.

## Open Constraints

- WAL checkpoint policy, retention policy, and compaction strategy remain open.
- Exact table names and column names may still change, but the durable model should stay centered on `functions`, `function_versions`, `runs`, and append-only `run_chunks`.
- The durability model should stay simple enough that one Go binary can own it without introducing an oversized systems surface area too early.
