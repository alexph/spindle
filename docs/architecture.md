# Spindle Architecture

## Purpose

Spindle is one Go binary with modular capabilities layered over a shared execution core. The architecture should keep a tiny project surface area by reducing queues, schedules, webhooks, streams, workflows, and user-triggered events to the same internal model instead of building a separate subsystem for each capability.

## Core Model

The unifying primitive is an ordered internal event log. Every external input becomes an internal event record, and every execution transition is reflected back into that log. Capability modules translate domain-specific inputs into the shared event model, while the dispatcher and execution state tracker interpret those records to decide what work should happen next.

This design keeps the server coherent:

- HTTP requests and webhooks become ingress events.
- Cron and schedules become time-based events.
- Queues and user events become dispatchable events.
- Streaming becomes a continuous sequence of ordered events.
- Workflows become durable chains of execution events and state transitions.

## Runtime Modules

Spindle should be described as a small set of cooperating runtime modules rather than one giant `engine` package:

- `websocket ingress`: accepts worker connections and carries all command and event traffic.
- `auth`: validates the shared secret and binds a connection to a worker identity.
- `worker registry`: tracks live worker sessions, presence, leases, and disconnects.
- `function registry`: tracks registered functions, triggers, options, and worker-owned function references.
- `dispatcher`: turns eligible events into execution assignments while enforcing concurrency and rate limits.
- `event log`: the ordered internal record of ingress, commands, events, and execution state transitions.
- `capability adapters`: modules for webhooks, HTTP, cron, queues, streaming, and workflows that all target the same core primitives.
- `execution state tracker`: keeps the current durable state of each execution and interprets worker responses such as ack, nack, retry, and defer.
- `durability/storage`: persists the event log and the minimum state needed to recover after restart.

To keep layers small, the implementation should separate only:

- `commands/*` for websocket message structs and readers/writers
- `events/*` for concrete internal event-log records
- core runtime modules that consume those types directly

This avoids inflating the codebase with extra service or repository layers.

## Project Layout Direction

The codebase should optimize for shared primitives and small internal seams:

- Keep transport, protocol, dispatch, and durability in a small core.
- Put each capability behind a narrow adapter module that emits shared internal events.
- Avoid per-capability runtimes, schedulers, or dispatch loops unless a concrete requirement forces it.
- Reuse the same nouns everywhere: `Worker`, `Function`, `Trigger`, `Command`, `Event`, `Execution`, and `FunctionRef`.
- Prefer one file per concrete wire or event shape over abstract handler hierarchies.

The result should feel like one execution engine with many inputs, not many products in one repository.

## High-Level Flow

1. An external capability adapter or worker-originated `send` or `rpc` command produces an internal event.
2. The event is appended to the ordered event log.
3. The dispatcher evaluates that event against function registrations, workflow state, concurrency limits, and rate limits.
4. If work is eligible, the dispatcher assigns an execution to an available worker that holds a matching `FunctionRef`.
5. The worker executes the callable inside its local SDK runtime and sends protocol messages back over the same websocket connection.
6. Each state transition is recorded as an event, updating the durable view of the execution.

## Open Constraints

- The architecture commits to an ordered internal event log, but not yet to a specific storage engine.
- Capability modules may have local logic, but they must not introduce a second execution model.
- The first implementation should prefer explicit simple modules over deep package trees or abstract plugin systems.
- Concrete types should carry most of the meaning; avoid a convention that forces every operation into a `SomethingCommand` name.
