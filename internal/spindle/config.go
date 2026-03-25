package spindle

import "github.com/kelseyhightower/envconfig"

type Config struct {
	Addr   string `env:"SPINDLE_ADDR" envDefault:"localhost:8080"`
	DBPath string `env:"SPINDLE_DB_PATH" envDefault:"spindle.db"`
}

func LoadConfig() (*Config, error) {
	cfg := &Config{}
	if err := envconfig.Process("", cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}
