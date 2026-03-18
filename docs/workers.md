# Spindle Workers

## Purpose

Workers are long-lived websocket-connected processes that register functions with Spindle, emit events into the system, and execute assigned work through local SDK runtimes. They are the bridge between the central coordinator and polyglot application code.

## Worker Model

A `Worker` is any process that can:

- connect to the Spindle websocket endpoint
- authenticate with the shared secret via the initial `hello`
- register one or more `Function` definitions
- emit `Event` records or user-land triggers through the protocol
- receive `Execution` requests
- execute function callables locally through an SDK
- report state transitions back to the server

Workers can be application servers such as Django processes, serverless runtimes in TypeScript, or services written in Go or Rust. The server remains Go-only; the worker side is intentionally polyglot.

## Connectivity

Workers should be treated like a lobby of connected participants:

- many workers may connect at the same time
- workers may disconnect and reconnect at any time
- multiple workers may advertise the same logical function ID
- the server should not assume any single worker is permanent

This model supports natural scale-up and scale-down topology. Adding workers increases available function references. Removing workers should shrink capacity without corrupting logical registrations.

## Registration Ownership

Function registrations must be tracked at two levels:

- logical `Function` identity, keyed by function ID
- worker-scoped `FunctionRef` ownership, keyed by connection/session

This distinction matters because the same function ID may be present on several workers. On disconnect, Spindle should clean up only the stale `FunctionRef` records owned by that worker session. The logical function remains available as long as at least one live worker still advertises it.

For v1, `FunctionRef` liveness should be derived directly from websocket connection state. If the connection is gone, the `FunctionRef` is gone. A separate lease renewal protocol is not required yet.

## Disconnect Handling

Disconnects are normal, not exceptional. The worker model must support:

- connection-derived liveness for active workers
- cleanup of stale `FunctionRef` ownership after disconnect
- re-evaluation of in-flight executions that were assigned to the lost worker
- safe convergence when the worker reconnects and re-registers

V1 should rely on whatever heartbeat or disconnect detection the websocket framework already provides. If no eligible connected worker exists when work is dispatched, dropped delivery is an accepted v1 limitation and should be documented rather than hidden.

The cleanup path should be part of the normal lifecycle, not an operational repair step.

## Reader Model

On the worker side, execution consumption should be modeled as a reader over the websocket stream rather than a pile of ad hoc callbacks. A worker should feel like it is consuming assigned work from a stream of commands and writing execution updates back onto the same connection.

## Server And SDK Split

Spindle server responsibilities:

- authenticate workers
- maintain registries of workers, functions, triggers, and function references
- accept emitted events from authenticated workers or capability adapters
- assign executions
- enforce concurrency and rate limits
- persist the durable execution record

SDK responsibilities:

- expose registration APIs for functions and event-emission APIs for user-land triggers
- manage local executors and callables
- receive execution requests from the websocket connection through a reader-style consumer
- run the callable inside the client runtime
- send execution updates back to the server

Workers are execution hosts. Spindle server is the coordinator of truth.
