# Spindle Wire Shapes

## Purpose

This document proposes a concrete starting wire appendix for Spindle v1. It is intentionally narrow and mirrors the current protocol decisions without adding extra abstraction.

All websocket messages are normal JSON objects with a top-level `kind` field.

## Common Rules

- every websocket frame is a single JSON object
- every frame includes `kind`
- fields not listed here are out of scope for v1 unless added intentionally later
- server-generated IDs may be returned in acknowledgements or execution messages

## `hello`

Sent first after websocket connect.

```json
{
  "kind": "hello",
  "auth": "extra secret key"
}
```

Optional future fields:

- `worker_name`
- `protocol_version`
- `metadata`

V1 rule:

- connect websocket
- send `hello`
- wait for acceptance
- only then send other messages

## `registered`

Server acknowledgement for accepted worker or function registration.

```json
{
  "kind": "registered",
  "id": "fn_123"
}
```

V1 can keep this minimal. The `id` should identify what was accepted, or the accepted live reference created by the server.

## `create_function`

```json
{
  "kind": "create_function",
  "id": "email.send",
  "label": "Send email to a user",
  "triggers": [
    { "kind": "event", "name": "emails.received" }
  ],
  "concurrency": [
    { "kind": "limit", "limit": 1 }
  ],
  "rate_limit": [
    { "kind": "time", "limit": 10, "period": "second" }
  ],
  "retries": [
    { "kind": "simple", "max_attempts": 3, "delay": "5s" }
  ],
  "version": "sha256:..."
}
```

V1 shapes:

- trigger: `{ "kind": "event", "name": string }`
- concurrency: `{ "kind": "limit", "limit": int }`
- rate limit: `{ "kind": "time", "limit": int, "period": "second" | "minute" | "hour" | "day" }`
- retry: `{ "kind": "simple", "max_attempts": int, "delay": duration }`

## `send`

```json
{
  "kind": "send",
  "name": "emails.received",
  "payload": {},
  "idempotency_id": "evt_123"
}
```

Notes:

- `name` is the caller-facing event name
- `payload` is arbitrary JSON
- `idempotency_id` is optional but recommended where deduplication matters

## `rpc`

```json
{
  "kind": "rpc",
  "name": "user.lookup",
  "payload": {},
  "idempotency_id": "rpc_123",
  "timeout": "5s"
}
```

Notes:

- `rpc` uses the same transport model as `send`
- `timeout` is a caller willingness-to-wait value
- the server attaches internal correlation data

## `execution_request`

Server-to-worker request to execute work.

```json
{
  "kind": "execution_request",
  "id": "run_123",
  "function_id": "email.send",
  "input": {
    "id": "run_123"
  }
}
```

V1 should still keep this small, but it is useful to include:

- `id`: the run ID
- `function_id`: the logical function being invoked
- `input`: the serialized execution snapshot for the worker

The `input` object can remain minimal in v1. It only needs enough data for the worker SDK to dispatch the call and correlate updates.

## `execution_update`

Worker-to-server execution update.

```json
{
  "kind": "execution_update",
  "id": "run_123",
  "state": "started",
  "payload": {
    "id": "run_123"
  }
}
```

Example states:

- `accepted`
- `started`
- `progress`
- `deferred`
- `completed`
- `failed`

V1 should include:

- `id`: the run ID
- `state`: the execution state name
- `payload`: the semantic snapshot for that state

The server should map these states into run chunks:

- `accepted` -> `accepted`
- `started` -> `started`
- `progress` -> `progress`
- `deferred` -> `deferred`
- `completed` -> `completed`
- `failed` -> `failed`

If needed later, richer payloads can be added without changing the top-level frame shape.

## `ack`

```json
{
  "kind": "ack",
  "id": "run_123"
}
```

## `nack`

```json
{
  "kind": "nack",
  "id": "run_123"
}
```

## `disconnect_notice`

Optional graceful disconnect signal.

```json
{
  "kind": "disconnect_notice",
  "id": "worker_123"
}
```

## Reader Notes

Workers should consume these frames through a reader over the websocket connection:

- read next frame
- inspect `kind`
- decode into the matching concrete struct
- hand execution work to the local executor
- write updates back as concrete structs

This appendix is intentionally minimal. It should be treated as a starting wire contract, not a finished RFC.
