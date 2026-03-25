-- +goose Up

CREATE TABLE apps (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL
);

CREATE TABLE functions (
  id TEXT NOT NULL,
  app_id TEXT NOT NULL,
  kind TEXT NOT NULL,
  name TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  PRIMARY KEY (app_id, id),
  FOREIGN KEY (app_id) REFERENCES apps(id) ON DELETE CASCADE
);

CREATE INDEX functions_app_id_idx
  ON functions (app_id);

CREATE TABLE runs (
  id TEXT PRIMARY KEY,
  app_id TEXT NOT NULL,
  function_id TEXT NOT NULL,
  status TEXT NOT NULL,
  source_name TEXT,
  idempotency_id TEXT,
  correlation_id TEXT,
  event_count INTEGER NOT NULL,
  attempt_count INTEGER NOT NULL,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  FOREIGN KEY (app_id) REFERENCES apps(id) ON DELETE CASCADE,
  FOREIGN KEY (app_id, function_id) REFERENCES functions(app_id, id) ON DELETE CASCADE
);

CREATE INDEX runs_app_id_status_idx
  ON runs (app_id, status);

CREATE INDEX runs_function_id_status_idx
  ON runs (function_id, status);

CREATE TABLE run_events (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  run_id TEXT NOT NULL,
  app_id TEXT NOT NULL,
  function_id TEXT NOT NULL,
  position INTEGER NOT NULL,
  topic TEXT NOT NULL,
  payload_json TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  FOREIGN KEY (run_id) REFERENCES runs(id) ON DELETE CASCADE,
  FOREIGN KEY (app_id) REFERENCES apps(id) ON DELETE CASCADE,
  FOREIGN KEY (app_id, function_id) REFERENCES functions(app_id, id) ON DELETE CASCADE,
  UNIQUE (run_id, position)
);

CREATE INDEX run_events_run_id_idx
  ON run_events (run_id, position);
