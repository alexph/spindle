package spindle

import (
	"github.com/jmoiron/sqlx"
)

type AppContext struct {
	Config *Config
	DB     *sqlx.DB
}

func NewAppContext(config *Config, database *sqlx.DB) *AppContext {
	return &AppContext{
		Config: config,
		DB:     database,
	}
}
