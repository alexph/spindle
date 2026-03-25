package db

import (
	"context"
	"database/sql"
	"embed"
	"fmt"

	"github.com/jmoiron/sqlx"
	_ "github.com/mattn/go-sqlite3"
	"github.com/pressly/goose/v3"
)

const driverName = "sqlite3"

//go:embed migrations/*.sql
var migrationsFS embed.FS

func Open(ctx context.Context, path string) (*sqlx.DB, error) {
	db, err := sqlx.ConnectContext(ctx, driverName, path)
	if err != nil {
		return nil, fmt.Errorf("connect sqlite: %w", err)
	}

	if _, err := db.ExecContext(ctx, "PRAGMA journal_mode = WAL;"); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("enable wal mode: %w", err)
	}

	if _, err := db.ExecContext(ctx, "PRAGMA foreign_keys = ON;"); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("enable foreign keys: %w", err)
	}

	return db, nil
}

func Migrate(db *sql.DB) error {
	goose.SetBaseFS(migrationsFS)
	if err := goose.SetDialect(driverName); err != nil {
		return fmt.Errorf("set goose dialect: %w", err)
	}
	if err := goose.Up(db, "migrations"); err != nil {
		return fmt.Errorf("run goose migrations: %w", err)
	}
	return nil
}
