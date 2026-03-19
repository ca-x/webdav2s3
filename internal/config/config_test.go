package config

import (
	"strings"
	"testing"
)

func TestLoadDatabaseURLTakesPrecedence(t *testing.T) {
	t.Setenv("DATABASE_URL", "file:./url.db?cache=shared&_fk=1")
	t.Setenv("DATABASE_PATH", "./path.db")
	t.Setenv("JWT_SECRET", "secret")
	t.Setenv("S3_BUCKET", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if !cfg.IsDatabaseMode() {
		t.Fatal("expected database mode to be enabled")
	}

	if got := cfg.DatabaseConnectionString(); got != "file:./url.db?cache=shared&_fk=1" {
		t.Fatalf("DatabaseConnectionString() = %q, want DATABASE_URL value", got)
	}
}

func TestLoadWithOnlyJWTSecretUsesDefaultDatabasePath(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	t.Setenv("DATABASE_PATH", "")
	t.Setenv("JWT_SECRET", "secret")
	t.Setenv("S3_BUCKET", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if !cfg.IsDatabaseMode() {
		t.Fatal("expected database mode to be enabled")
	}
	if cfg.DatabasePath == "" {
		t.Fatal("expected default DATABASE_PATH to be set")
	}
	if !strings.Contains(cfg.DatabaseConnectionString(), cfg.DatabasePath) {
		t.Fatalf("DatabaseConnectionString() should contain DatabasePath %q, got %q", cfg.DatabasePath, cfg.DatabaseConnectionString())
	}
}
