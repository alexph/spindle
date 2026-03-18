# Spindle First Migration

## Purpose

This document proposes the first concrete migration layout for Spindle v1. It is written as a doc-first draft that should translate directly into a `goose` migration later.

The goal is to create the minimum durable schema needed to support:

- versioned function registration
- connection-derived worker sessions
- universal runs
- append-only run chunks

## Migration Strategy

The first migration should create the smallest useful durable set first:

1. `functions`
2. `function_versions`
3. `worker_sessions`
4. `runs`
5. `run_chunks`

`function_refs` and `leases` should stay out of the first migration by default unless implementation pressure makes them necessary.

## SQLite Pragmas

The runtime should enable these at database open rather than encode them into schema DDL:

```sql
PRAGMA journal_mode = WAL;
PRAGMA foreign_keys = ON;
```

## Proposed Up Migration

```sql
CREATE TABLE functions (
  id TEXT PRIMARY KEY,
  current_version_hash TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);

CREATE INDEX functions_current_version_hash_idx
  ON functions (current_version_hash);


CREATE TABLE function_versions (
  function_id TEXT NOT NULL,
  version_hash TEXT NOT NULL,
  config_json TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  PRIMARY KEY (function_id, version_hash),
  FOREIGN KEY (function_id) REFERENCES functions(id)
);

CREATE INDEX function_versions_version_hash_idx
  ON function_versions (version_hash);


CREATE TABLE worker_sessions (
  id TEXT PRIMARY KEY,
  worker_name TEXT,
  metadata_json TEXT,
  connected_at INTEGER NOT NULL,
  disconnected_at INTEGER,
  last_seen_at INTEGER NOT NULL
);

CREATE INDEX worker_sessions_connected_at_idx
  ON worker_sessions (connected_at);

CREATE INDEX worker_sessions_last_seen_at_idx
  ON worker_sessions (last_seen_at);

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


CREATE TABLE run_chunks (
  run_id TEXT NOT NULL,
  position INTEGER NOT NULL,
  chunk_type TEXT NOT NULL,
  payload_json TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  PRIMARY KEY (run_id, position),
  FOREIGN KEY (run_id) REFERENCES runs(id)
);

CREATE INDEX run_chunks_chunk_type_idx
  ON run_chunks (chunk_type);

CREATE INDEX run_chunks_created_at_idx
  ON run_chunks (created_at);
```

## Proposed Down Migration

```sql
DROP INDEX IF EXISTS run_chunks_created_at_idx;
DROP INDEX IF EXISTS run_chunks_chunk_type_idx;
DROP TABLE IF EXISTS run_chunks;

DROP INDEX IF EXISTS runs_created_at_idx;
DROP INDEX IF EXISTS runs_correlation_id_idx;
DROP INDEX IF EXISTS runs_idempotency_id_idx;
DROP INDEX IF EXISTS runs_status_idx;
DROP INDEX IF EXISTS runs_function_id_idx;
DROP INDEX IF EXISTS runs_kind_idx;
DROP TABLE IF EXISTS runs;

DROP INDEX IF EXISTS worker_sessions_last_seen_at_idx;
DROP INDEX IF EXISTS worker_sessions_connected_at_idx;
DROP TABLE IF EXISTS worker_sessions;

DROP INDEX IF EXISTS function_versions_version_hash_idx;
DROP TABLE IF EXISTS function_versions;

DROP INDEX IF EXISTS functions_current_version_hash_idx;
DROP TABLE IF EXISTS functions;
```

## Notes

- This migration intentionally keeps JSON blobs as `TEXT`
- `function_refs` is intentionally omitted from the default first migration because current v1 liveness is connection-derived
- `runs.status` is a convenience field; `run_chunks` remains the durable source of truth
- timestamps are unix seconds or millis as `INTEGER`; the implementation should pick one and stay consistent

## Likely Follow-Up Migration

The next migration is likely to add one of:

- a `function_refs` table if durable registration bookkeeping proves useful
- a derived `leases` table
- uniqueness constraints for scoped idempotency
- more targeted indexes once real query patterns are known
