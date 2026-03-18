# Concurrency And Limits

## Purpose

Spindle server is the source of truth for coordination limits. Concurrency and rate limiting must be enforced centrally so that polyglot workers and horizontally scaled applications behave as one coherent system.

## Concurrency Model

The minimum required concurrency scope is the logical `Function` ID. If a function is registered by multiple workers, Spindle still enforces the configured concurrency policy across the whole cluster rather than per worker.

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
