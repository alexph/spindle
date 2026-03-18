# Spindle Functions

## Purpose

A `Function` is the core unit of executable work in Spindle. Functions are registered by workers, identified by a stable ID, configured with options and capability metadata, and executed locally by client SDKs when the server dispatches an execution.

## Function Definition

Each function registration should capture:

- `function_id`: the stable logical identity used across workers and executions
- `ref_id`: the worker-scoped function reference for this live registration
- `options`: execution metadata such as concurrency hints, timeout expectations, retry hints, tags, or routing metadata
- `capability metadata`: the ingress or matching information that describes how events may target the function
- `worker ownership`: the worker session currently advertising the function reference

The callable implementation is local to the SDK host and does not exist inside the Spindle server.

## Logical Function Versus Function Reference

The design should consistently separate:

- `Function`: the logical unit of work identified by function ID
- `FunctionRef`: the concrete live registration of that function on a specific connected worker

This allows the same function to exist across multiple worker processes, applications, or deployment backends. The server dispatches to function references, but concurrency and policy should generally be reasoned about at the logical function level unless a narrower scope is explicitly introduced later.

## Dispatch Responsibility

Spindle is responsible for:

- deciding whether an event should produce an execution
- choosing an eligible function for that execution
- selecting an available live function reference
- enforcing server-side concurrency and rate limits before dispatch

The SDK is responsible for:

- holding the callable
- invoking the callable in the local runtime
- translating local outcomes into protocol execution updates

This split keeps coordination centralized while allowing polyglot execution.

## SDK Shape

The SDK should be allowed to expose capability-specific wrappers, but those wrappers should normalize to the same server-side function model. A client might look conceptually like:

```python
import spindle


async def my_function(ctx):
    ...


spindle.register_function(
    spindle.function(
        my_function,
        id="my-function",
        trigger=spindle.queue("emails"),
        concurrency=[spindle.concurrency(limit=1)],
        rate_limits=[spindle.rate_limit(key="emails", rate="10/s")],
    )
)

spindle.register_function(
    spindle.function(
        my_function,
        id="http-my-function",
        trigger=spindle.http(method="POST", path="/events"),
    )
)
```

The exact SDK naming may change, but the model should stay consistent:

- the SDK may provide ergonomic wrappers such as queue or HTTP helpers
- the worker still registers a normalized `Function`
- the trigger or ingress description is metadata on the function or on emitted events
- the server should not need separate registration lifecycles for queue functions versus HTTP functions

## Function Placement

A single logical function may be advertised by many workers at once. This allows:

- horizontal scale for the same function across multiple app instances
- geographic or backend diversity for the same logical function
- worker churn without losing the logical function identity

Function placement should remain explicit in the worker registry through live `FunctionRef` records, rather than being inferred from static configuration.

## Trigger Relationship

Functions should be triggered by internal events, not by direct invocation paths that bypass the shared model. Trigger bindings may be explicit in function metadata or be resolved through capability configuration, but the outcome should still be “event enters log, dispatcher resolves function, execution is assigned.”

`Trigger` should be treated as event-shaping metadata, not a persistent worker-owned registration by default.
