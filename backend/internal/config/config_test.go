package config_test

import (
	"os"
	"testing"

	"github.com/meme-radar/radar/internal/config"
)

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "config-*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(content); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return f.Name()
}

func TestLoad_valid(t *testing.T) {
	path := writeConfig(t, `
database:
  url: postgres://user:pass@localhost:5432/testdb
http:
  addr: ":9090"
log:
  level: debug
`)
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v, want nil", err)
	}
	if got, want := cfg.Database.URL, "postgres://user:pass@localhost:5432/testdb"; got != want {
		t.Errorf("Database.URL = %q, want %q", got, want)
	}
	if got, want := cfg.HTTP.Addr, ":9090"; got != want {
		t.Errorf("HTTP.Addr = %q, want %q", got, want)
	}
	if got, want := cfg.Log.Level, "debug"; got != want {
		t.Errorf("Log.Level = %q, want %q", got, want)
	}
}

func TestLoad_defaults(t *testing.T) {
	path := writeConfig(t, `
database:
  url: postgres://user:pass@localhost:5432/testdb
`)
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v, want nil", err)
	}
	if got, want := cfg.HTTP.Addr, ":8080"; got != want {
		t.Errorf("HTTP.Addr default = %q, want %q", got, want)
	}
	if got, want := cfg.Log.Level, "info"; got != want {
		t.Errorf("Log.Level default = %q, want %q", got, want)
	}
}

func TestLoad_envOverride(t *testing.T) {
	path := writeConfig(t, `
database:
  url: postgres://original@localhost/db
http:
  addr: ":8080"
`)
	t.Setenv("RADAR_DATABASE_URL", "postgres://override@remotehost/prod")
	t.Setenv("RADAR_HTTP_ADDR", ":9999")

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v, want nil", err)
	}
	if got, want := cfg.Database.URL, "postgres://override@remotehost/prod"; got != want {
		t.Errorf("Database.URL after env override = %q, want %q", got, want)
	}
	if got, want := cfg.HTTP.Addr, ":9999"; got != want {
		t.Errorf("HTTP.Addr after env override = %q, want %q", got, want)
	}
}

func TestLoad_missingDatabaseURL(t *testing.T) {
	path := writeConfig(t, `
http:
  addr: ":8080"
`)
	_, err := config.Load(path)
	if err == nil {
		t.Fatal("Load() error = nil, want non-nil for missing database.url")
	}
}

func TestLoad_fileNotFound(t *testing.T) {
	_, err := config.Load("/nonexistent/path/config.yaml")
	if err == nil {
		t.Fatal("Load() error = nil, want non-nil for missing file")
	}
}

func TestLoad_invalidYAML(t *testing.T) {
	path := writeConfig(t, `{invalid yaml: [}`)
	_, err := config.Load(path)
	if err == nil {
		t.Fatal("Load() error = nil, want non-nil for invalid YAML")
	}
}
