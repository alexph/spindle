package spindle

import (
	"context"

	"github.com/alexph/spindle/internal/db"
)

func Run(ctx context.Context) error {
	cfg, err := LoadConfig()
	if err != nil {
		return err
	}

	return RunWithConfig(ctx, cfg)
}

func RunWithConfig(ctx context.Context, cfg *Config) error {
	database, err := db.Open(ctx, cfg.DBPath)
	if err != nil {
		return err
	}
	defer database.Close()

	if err := db.Migrate(database.DB); err != nil {
		return err
	}

	_ = NewAppContext(cfg, database)

	<-ctx.Done()
	return nil
}
