# Spindle SQL Schema

## Purpose

This document proposes a concrete starting SQLite schema for Spindle v1. It is intentionally narrow and derived from the design docs, not from implementation constraints that do not exist yet.

Use this document as the conceptual schema overview. For the concrete first-cut DDL, use [first-migration.md](/Users/alex/Projects/spindle/docs/first-migration.md).

The target environment is:

- SQLite
- WAL mode
- `sqlx`
- `goose` migrations written in Go

## Schema Direction

The schema should reflect the current durable model:

- logical functions
- versioned function configuration
- runs as the universal durable unit
- append-only run chunks
- connection-derived worker sessions
- optional derived lease rows if the implementation wants them

SQLite types should stay simple in v1:

- use `TEXT` for IDs, enums, hashes, durations, and JSON blobs
- use `INTEGER` for counters, positions, boolean flags, and unix timestamps
- keep JSON as text payloads instead of attempting deep relational decomposition too early

## Recommended V1 Default

The simplest recommended v1 schema is:

- `functions`
- `function_versions`
- `worker_sessions`
- `runs`
- `run_chunks`

Optional tables that can be deferred:

- `function_refs`
- `leases`

This keeps the first implementation aligned with the current connection-derived liveness model and avoids persisting bookkeeping that may still be easier to keep in memory.

## `functions`

```sql
CREATE TABLE functions (
  id TEXT PRIMARY KEY,
  current_version_hash TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);
```

Suggested indexes:

```sql
CREATE INDEX functions_current_version_hash_idx
  ON functions (current_version_hash);
```

Notes:

- `id` is the logical function ID
- `current_version_hash` points to the latest accepted version for that function
- function identity should survive worker churn

## `function_versions`

```sql
CREATE TABLE function_versions (
  function_id TEXT NOT NULL,
  version_hash TEXT NOT NULL,
  config_json TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  PRIMARY KEY (function_id, version_hash),
  FOREIGN KEY (function_id) REFERENCES functions(id)
);
```

Suggested indexes:

```sql
CREATE INDEX function_versions_version_hash_idx
  ON function_versions (version_hash);
```

Notes:

- `config_json` stores the canonical function config snapshot
- `version_hash` should be derived from that canonical config
- the server should be able to recompute and verify the hash before insert

## `worker_sessions`

```sql
CREATE TABLE worker_sessions (
  id TEXT PRIMARY KEY,
  worker_name TEXT,
  metadata_json TEXT,
  connected_at INTEGER NOT NULL,
  disconnected_at INTEGER,
  last_seen_at INTEGER NOT NULL
);
```

Suggested indexes:

```sql
CREATE INDEX worker_sessions_connected_at_idx
  ON worker_sessions (connected_at);

CREATE INDEX worker_sessions_last_seen_at_idx
  ON worker_sessions (last_seen_at);
```

Notes:

- v1 liveness is connection-derived
- `metadata_json` is optional worker metadata from `hello`
- a session is live while `disconnected_at` is null and connection state is valid in memory

## `function_refs`

`FunctionRef` is connection-derived in v1, so this table should be treated as optional. It is useful only if the implementation wants explicit restart visibility or durable cleanup bookkeeping early.

```sql
CREATE TABLE function_refs (
  ref_id TEXT PRIMARY KEY,
  function_id TEXT NOT NULL,
  function_version_hash TEXT NOT NULL,
  worker_session_id TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  disconnected_at INTEGER,
  FOREIGN KEY (function_id) REFERENCES functions(id),
  FOREIGN KEY (function_id, function_version_hash)
    REFERENCES function_versions(function_id, version_hash),
  FOREIGN KEY (worker_session_id) REFERENCES worker_sessions(id)
);
```

Suggested indexes:

```sql
CREATE INDEX function_refs_function_id_idx
  ON function_refs (function_id);

CREATE INDEX function_refs_worker_session_id_idx
  ON function_refs (worker_session_id);
```

Notes:

- recommended default: defer this table in v1
- include it only if the implementation genuinely benefits from persisting live registration bookkeeping

## `runs`

```sql
CREATE TABLE runs (
  id TEXT PRIMARY KEY,
  kind TEXT NOT NULL,
  function_id TEXT,
  function_version_hash TEXT,
  source_name TEXT,
  idempotency_id TEXT,
  correlation_id TEXT,
  status TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  FOREIGN KEY (function_id) REFERENCES functions(id),
  FOREIGN KEY (function_id, function_version_hash)
    REFERENCES function_versions(function_id, version_hash)
);
```

Suggested indexes:

```sql
CREATE INDEX runs_kind_idx
  ON runs (kind);

CREATE INDEX runs_function_id_idx
  ON runs (function_id);

CREATE INDEX runs_status_idx
  ON runs (status);

CREATE INDEX runs_idempotency_id_idx
  ON runs (idempotency_id);

CREATE INDEX runs_correlation_id_idx
  ON runs (correlation_id);

CREATE INDEX runs_created_at_idx
  ON runs (created_at);
```

Notes:

- `kind` is a coarse origin such as `event`, `rpc`, `workflow`, or `schedule`
- `status` is a derived current summary, not the source of truth
- `idempotency_id` and `correlation_id` may be null when not relevant

## `run_chunks`

```sql
CREATE TABLE run_chunks (
  run_id TEXT NOT NULL,
  position INTEGER NOT NULL,
  chunk_type TEXT NOT NULL,
  payload_json TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  PRIMARY KEY (run_id, position),
  FOREIGN KEY (run_id) REFERENCES runs(id)
);
```

Suggested indexes:

```sql
CREATE INDEX run_chunks_chunk_type_idx
  ON run_chunks (chunk_type);

CREATE INDEX run_chunks_created_at_idx
  ON run_chunks (created_at);
```

Notes:

- `chunk_type` is the durable event name on the run timeline
- `payload_json` is the semantic snapshot for that chunk
- ordering is per-run via `position`

## `leases`

If the implementation wants an explicit derived lease table, keep it narrow and connection-derived:

```sql
CREATE TABLE leases (
  holder_type TEXT NOT NULL,
  holder_id TEXT NOT NULL,
  lease_key TEXT NOT NULL,
  expires_at INTEGER NOT NULL,
  PRIMARY KEY (holder_type, holder_id, lease_key)
);
```

Suggested indexes:

```sql
CREATE INDEX leases_expires_at_idx
  ON leases (expires_at);
```

Notes:

- v1 does not require a separate lease protocol
- this table is optional and should remain derived from connection state

## Operational Notes

- enable WAL mode on database open
- prefer unix timestamps in integer columns for simplicity
- append chunks and update `runs.status` / `runs.updated_at` in one transaction
- treat `run_chunks` as the durable source of truth and `runs.status` as an optimization

## Relationship To First Migration

[first-migration.md](/Users/alex/Projects/spindle/docs/first-migration.md) should be treated as the implementation-oriented default. This document exists to explain the durable model and optional tables without forcing every possible table into the first migration.
3. `worker_sessions`
4. `function_refs`
5. `runs`
6. `run_chunks`
7. optional `leases`

## Open Questions

- whether `function_refs` should exist as a table in v1 or stay entirely in memory
- whether `metadata_json` on `worker_sessions` is worth persisting immediately
- whether `idempotency_id` should become unique per `kind` or per `source_name` later
- whether a `run_subscribers` or `cursors` table becomes necessary once streaming matures
