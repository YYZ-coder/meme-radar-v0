// Package config loads and validates application configuration from a YAML file,
// with environment variable overrides for secrets.
package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Config holds all application configuration.
type Config struct {
	Log      LogConfig      `yaml:"log"`
	HTTP     HTTPConfig     `yaml:"http"`
	Database DatabaseConfig `yaml:"database"`
	RawStore RawStoreConfig `yaml:"raw_store"`
}

// LogConfig controls structured logging (docs/engineering/18-observability.md §2).
type LogConfig struct {
	// Level is one of: debug, info, warn, error.
	Level string `yaml:"level"`
	// File optionally duplicates stdout logs to a file that Grafana Alloy tails.
	File string `yaml:"file"`
	// Role labels log lines: all | ingest | parse | compute | api.
	Role string `yaml:"role"`
}

// HTTPConfig controls the REST API listener.
type HTTPConfig struct {
	Addr string `yaml:"addr"`
}

// DatabaseConfig holds PostgreSQL connection settings.
type DatabaseConfig struct {
	// URL is a full DSN, e.g. postgres://user:pass@host:5432/db?sslmode=disable
	URL string `yaml:"url"`
}

// RawStoreConfig controls where raw block files are stored.
type RawStoreConfig struct {
	// LocalDir is used in development (D75).
	// Leave empty and configure S3 credentials via environment for production.
	LocalDir string `yaml:"local_dir"`
}

// Load reads the YAML config file at path, applies environment variable overrides,
// fills in defaults for optional fields, and validates required fields.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %q: %w", path, err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	applyEnvOverrides(&cfg)
	applyDefaults(&cfg)

	if err := validate(&cfg); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	return &cfg, nil
}

// applyEnvOverrides lets secrets be injected at runtime without touching the YAML file.
func applyEnvOverrides(cfg *Config) {
	if v := os.Getenv("RADAR_DATABASE_URL"); v != "" {
		cfg.Database.URL = v
	}
	if v := os.Getenv("RADAR_HTTP_ADDR"); v != "" {
		cfg.HTTP.Addr = v
	}
	if v := os.Getenv("RADAR_LOG_LEVEL"); v != "" {
		cfg.Log.Level = v
	}
	if v := os.Getenv("RADAR_LOG_FILE"); v != "" {
		cfg.Log.File = v
	}
	if v := os.Getenv("RADAR_ROLE"); v != "" {
		cfg.Log.Role = v
	}
}

func applyDefaults(cfg *Config) {
	if cfg.Log.Level == "" {
		cfg.Log.Level = "info"
	}
	if cfg.Log.Role == "" {
		cfg.Log.Role = "all"
	}
	if cfg.HTTP.Addr == "" {
		cfg.HTTP.Addr = ":8080"
	}
}

func validate(cfg *Config) error {
	switch cfg.Log.Role {
	case "all", "ingest", "parse", "compute", "api":
	default:
		return fmt.Errorf("log.role %q must be one of all, ingest, parse, compute, api", cfg.Log.Role)
	}
	if cfg.Database.URL == "" {
		return fmt.Errorf("database.url is required (or set RADAR_DATABASE_URL)")
	}
	return nil
}
