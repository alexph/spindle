# Concurrency And Limits

## Purpose

Spindle server is the source of truth for coordination limits. Concurrency and rate limiting must be enforced centrally so that polyglot workers and horizontally scaled applications behave as one coherent system.

## Concurrency Model

The minimum required concurrency scope is the logical `Function` ID. If a function is registered by multiple workers, Spindle still enforces the configured concurrency policy across the whole cluster rather than per worker.

For v1, concurrency should be configured as an array of policy objects:

```json
[
  { "kind": "limit", "limit": 1 }
]
```

The array shape is deliberate even though v1 only needs the `limit` variant. It leaves room for future keyed or field-based concurrency rules without changing the top-level field.

Initial rule:

- if function `email.send` is registered by three worker instances
- and concurrency for `email.send` is `1`
- then only one `Execution` for `email.send` may be active at a time across all three workers combined

This guarantees that scaling out workers increases availability and capacity only when the function’s policy allows it.

## Dispatch Path

Concurrency and limits should sit directly in the dispatch path:

1. An internal event becomes eligible for execution.
2. The dispatcher resolves candidate functions.
3. Before assigning a worker, the dispatcher checks active executions and limit state.
4. If limits allow execution, the dispatcher assigns an available `FunctionRef`.
5. If limits do not allow execution, the event or execution remains pending, deferred, or rescheduled under policy.

Limits should not be best-effort hints handled only by workers or SDKs.

## Rate Limiting

The initial design should support rate limits at least at these scopes:

- per queue or event source where ingress must be smoothed
- per function ID where downstream work must be throttled

For v1, `rate_limit` should be configured as an array of objects shaped like:

```json
[
  { "kind": "time", "limit": 10, "period": "second" }
]
```

`period` should be treated as an enum with these v1 values:

- `second`
- `minute`
- `hour`
- `day`

The shape should still leave room for future field-based rate-limit variants.

## Retries

Retries should follow the same list-based design as rate limits so the policy shape can grow without a wire break.

For v1, `retries` should be an array of retry-policy objects shaped like:

```json
[
  { "kind": "simple", "max_attempts": 3, "delay": "5s" }
]
```

This keeps retries semantically separate from rate limits:

- rate limits describe work per time window
- retries describe retry strategy after failure

Future variants such as backoff should be added as new `kind` values rather than overloading the v1 shape.

Rate limits should be evaluated centrally by the server before dispatch. Worker SDKs may expose local backpressure signals, but those do not replace server-side enforcement.

## Interaction With Worker Topology

Worker count and concurrency policy are independent:

- adding more workers increases the pool of available function references
- concurrency policy controls how much work Spindle may actually activate
- disconnecting a worker reduces available placements, but it does not change the configured function concurrency target

This separation is important because worker topology is dynamic while policy should remain stable.

## Open Constraints

- The docs lock in server-side enforcement, but not yet the exact internal limiter data structures.
- Queue-level ordering guarantees may require narrower dispatch rules than the general function-level concurrency model.
- Future scopes such as tenant, key, workflow, or partition concurrency can be added later if the shared dispatch model remains intact.
