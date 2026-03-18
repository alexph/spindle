# Spindle Functions

## Purpose

A `Function` is the core unit of executable work in Spindle. Functions are registered by workers, identified by a stable ID, configured with options and capability metadata, and executed locally by client SDKs when the server dispatches an execution.

## Function Definition

Each function registration should capture one JSON-serializable configuration object:

```json
{
  "id": "email.send",
  "label": "Send email to a user",
  "triggers": [],
  "concurrency": [],
  "rate_limit": [],
  "retries": {},
  "version": "sha256:..."
}
```

The core fields are:

- `id`: the stable logical identity used across workers and executions
- `label`: the operator-facing description of what the function does
- `triggers`: the event or ingress descriptors that can target the function
- `concurrency`: the configured concurrency rules
- `rate_limit`: the configured rate-limit rules
- `retries`: retry behavior and policy
- `version`: a hash of the canonicalized function configuration JSON

The callable implementation is local to the SDK host and does not exist inside the Spindle server. Worker ownership and `FunctionRef` identity are live runtime facts added by the server when the function is created on a connected worker.

The version should be computed from the canonical function configuration, not just from the function signature. That keeps versioning stable across Python, Go, TypeScript, and Rust SDKs and ensures that changes to trigger bindings or execution policy produce a new version.

The canonicalization rule should be explicit:

- hash only `id`, `label`, `triggers`, `concurrency`, `rate_limit`, and `retries`
- do not hash `version` itself or any worker-specific metadata
- sort JSON object keys lexicographically
- preserve list order
- use compact JSON encoding with no insignificant whitespace

That gives every SDK a deterministic version hash without relying on language reflection.

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
    spindle.Queue(
        my_function,
        id="my-function",
        label="Process inbound email",
        queue="emails",
        triggers=[spindle.queue("emails")],
        concurrency=[spindle.Concurrency(limit=1)],
        rate_limit=[spindle.RateLimit(key="emails", rate="10/s")],
        retries=spindle.Retries(max_attempts=3),
    )
)

spindle.register_function(
    spindle.Http(
        my_function,
        id="http-my-function",
        label="Handle inbound event webhook",
        method="POST",
        path="/events",
        triggers=[spindle.http(method="POST", path="/events")],
    )
)

spindle.send(
    "emails.received",
    payload={"message_id": "msg_123"},
)

response = await spindle.rpc(
    "user.lookup",
    payload={"user_id": "usr_123"},
)
```

The exact SDK naming may change, but the model should stay consistent:

- the SDK may provide ergonomic wrappers such as `Queue(...)` or `Http(...)`
- the worker still sends one normalized function configuration object
- the SDK should compute a canonical config hash and send it as the function version
- the trigger or ingress description is metadata on the function or on emitted events
- the server should not need separate registration lifecycles for queue functions versus HTTP functions
- event ingress should use simple verbs such as `send(...)` and `rpc(...)`

## Server Normalization

The SDK can expose several ergonomic entrypoints without forcing matching server-side resource types:

- `register_function(...)` advertises executable work on a live worker
- the underlying wire command may be named `create_function`
- `Queue(...)` and `Http(...)` are client-side constructors for function metadata
- `send(...)` emits an event into the shared model
- `rpc(...)` emits a request that expects a correlated response

Inside the server, these should collapse into a small normalized model:

- function registration becomes a `Function` plus a live `FunctionRef`
- `send` and `rpc` become concrete internal events
- dispatch, concurrency, and durability operate on normalized records rather than capability-specific code paths

## Function Placement

A single logical function may be advertised by many workers at once. This allows:

- horizontal scale for the same function across multiple app instances
- geographic or backend diversity for the same logical function
- worker churn without losing the logical function identity

Function placement should remain explicit in the worker registry through live `FunctionRef` records, rather than being inferred from static configuration.

## Trigger Relationship

Functions should be triggered by internal events, not by direct invocation paths that bypass the shared model. Trigger bindings may be explicit in function metadata or be resolved through capability configuration, but the outcome should still be “event enters log, dispatcher resolves function, execution is assigned.”

`Trigger` should be treated as event-shaping metadata, not a persistent worker-owned registration by default.
