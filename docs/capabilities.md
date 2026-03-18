# Spindle Capabilities

## Purpose

Spindle exposes many capabilities, but they should all reduce to the same internal primitives. This document defines how each product-facing capability maps back to the ordered internal event log and shared execution flow.

## Shared Capability Model

Every capability follows the same pattern:

1. Receive input from an external source or internal state transition.
2. Normalize that input into an internal `Event`.
3. Append the event to the ordered event log.
4. Let the dispatcher decide whether that event creates an `Execution`.
5. Track resulting state transitions durably through the same execution model.

Capabilities are adapters over the shared core, not isolated engines.

## HTTP And Webhooks

- Input source: inbound HTTP requests and third-party webhook deliveries.
- Internal mapping: ingress events that capture route, headers, payload, source metadata, and delivery time.
- Execution pattern: dispatch one or more functions based on route or trigger metadata, then record delivery and execution outcomes back into the event log.

HTTP and webhooks should not have their own execution runtime. They are just event producers with HTTP-specific ingress concerns.

## Cron And Schedules

- Input source: time-based schedules maintained by the server.
- Internal mapping: scheduled events appended when a time boundary is reached.
- Execution pattern: create dispatchable executions for schedule-bound functions or workflow steps, with concurrency and rate limits checked before assignment.

Scheduling should be modeled as event creation, not a separate job system.

## Queues And Events

- Input source: explicit enqueue operations, ephemeral user-land triggers, and application-generated events.
- Internal mapping: queued or emitted events in the shared log.
- Execution pattern: route events to matching functions, workflow transitions, or downstream streams while preserving ordering where required by the queue or stream semantics.

Queues and events are the most direct expression of the shared model and should not invent different lifecycle terms.

## Streaming

- Input source: continuous event producers or append-only data sources.
- Internal mapping: ordered sequences of internal events, potentially partitioned by stream key.
- Execution pattern: dispatch functions incrementally as stream records arrive, with backpressure, concurrency, and retry behavior still enforced by the shared dispatcher.

Streaming is a high-throughput form of the same event model, not a second messaging subsystem.

## Workflows

- Input source: workflow starts, step completions, timers, and branch conditions.
- Internal mapping: workflow state transition events appended to the same log.
- Execution pattern: each workflow step resolves into one or more normal executions, and workflow progress is reconstructed from durable execution and state transition events.

Workflows should be the most structured consumer of the shared execution system, not a distinct orchestration engine.

## Design Rule

If a new capability cannot be explained as “input becomes event, event becomes execution, execution produces more events,” it should be treated as a sign that the shared model is incomplete or that the capability design is drifting.
