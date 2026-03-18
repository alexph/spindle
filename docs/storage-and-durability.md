# Storage And Durability

## Purpose

Spindle coordinates durable execution. This means the system must distinguish carefully between state that can remain in memory for a live process and state that must survive worker disconnects or server restarts.

## Must Be Durable

The initial design should treat these as durable records:

- the ordered internal event log
- execution records and their state transitions
- workflow progress derived from those state transitions
- scheduled events that must survive process restart
- the minimum dispatch state needed to resume pending or deferred work safely

Durability is required because workers are expected to disconnect and the server may restart while work is still logically in flight.

## May Be In-Memory With Recovery Rules

These may be kept in memory if they can be rebuilt or safely expired:

- live websocket connections
- transient worker presence data backed by leases or heartbeats
- connection-scoped `FunctionRef` ownership that can be rebuilt through re-registration after reconnect
- short-lived caches used for routing or dispatch acceleration

If an in-memory structure affects correctness, the docs should explain how it is reconstructed after restart or disconnect.

## Recovery Model

On restart or failover, the server should be able to:

- rebuild durable execution state from persisted records
- determine which executions were incomplete at the time of failure
- restore scheduled or deferred work that should still occur
- treat worker presence as lost until workers reconnect and re-register
- remove stale `FunctionRef` ownership that depended on dead connections

The system should favor replay or reconstruction from durable records over ad hoc repair logic.

## Worker Presence And Leases

Worker presence is important for dispatch, but it should not be treated as a durable business fact. A worker session should be modeled as leased liveness:

- active while the websocket is connected and heartbeats or connection state remain valid
- expired after disconnect or lease timeout
- able to recreate its registrations on reconnect

This keeps durable coordination focused on executions and events rather than ephemeral network sessions.

## Cleanup Semantics

Disconnect and restart handling must converge safely:

- stale worker sessions lose their `FunctionRef` ownership
- logical `Function` identities remain if other workers still advertise them
- in-flight executions assigned to a lost worker are re-evaluated under retry, defer, or failure policy
- pending events must not be lost simply because the chosen worker disappeared

These rules should be shared between the worker registry, dispatcher, and execution state tracker.

## Open Constraints

- This docs set does not select a storage backend yet.
- Event log shape, snapshot strategy, and retention policy remain open.
- The durability model should stay simple enough that one Go binary can own it without introducing an oversized systems surface area too early.
