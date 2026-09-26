package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConfigRecognizesOnlyOwnedDefaultRuntimePaths(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("DIBS_SOCKET", "")
	t.Setenv("DIBS_DB", "")
	t.Setenv("AF_COORDINATOR_SOCKET", "")
	t.Setenv("AF_COORDINATOR_DB", "")

	defaults := Default()
	if !defaults.UsesDefaultSocketPath() || !defaults.UsesDefaultDBPath() {
		t.Fatalf("default paths not recognized as daemon-owned: %+v", defaults)
	}

	custom := Config{
		SocketPath: filepath.Join(t.TempDir(), "custom.sock"),
		DBPath:     filepath.Join(t.TempDir(), "custom.db"),
	}
	if custom.UsesDefaultSocketPath() || custom.UsesDefaultDBPath() {
		t.Fatalf("custom paths recognized as daemon-owned: %+v", custom)
	}
}

func TestCanonicalEnvironmentWinsOverLegacy(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	// Env distinguishes an absent variable from an explicitly empty one.
	// Restore the caller's environment after testing the absent case.
	for _, key := range []string{"DIBS_OPERATOR_TOKEN", "AF_OPERATOR_TOKEN"} {
		t.Setenv(key, "")
		if err := os.Unsetenv(key); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("DIBS_DB", "/tmp/dibs.db")
	t.Setenv("AF_COORDINATOR_DB", "/tmp/legacy.db")
	t.Setenv("DIBS_SOCKET", "/tmp/dibs.sock")
	t.Setenv("AF_COORDINATOR_SOCKET", "/tmp/legacy.sock")
	t.Setenv("DIBS_LOG_LEVEL", "debug")
	t.Setenv("AF_COORDINATOR_LOG_LEVEL", "error")
	got := Default()
	if got.DBPath != "/tmp/dibs.db" || got.SocketPath != "/tmp/dibs.sock" || got.LogLevel != "debug" {
		t.Fatalf("canonical env did not win: %+v", got)
	}
	if value, ok := Env("DIBS_OPERATOR_TOKEN", "AF_OPERATOR_TOKEN"); ok || value != "" {
		t.Fatalf("unset env returned %q, %v", value, ok)
	}
	t.Setenv("DIBS_OPERATOR_TOKEN", "")
	t.Setenv("AF_OPERATOR_TOKEN", "legacy-secret")
	if value, ok := Env("DIBS_OPERATOR_TOKEN", "AF_OPERATOR_TOKEN"); !ok || value != "" {
		t.Fatalf("explicitly empty canonical env fell through: %q, %v", value, ok)
	}
}

func TestLegacyPathsRemainCanonicalUntilMigration(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, key := range []string{"DIBS_DB", "DIBS_SOCKET", "AF_COORDINATOR_DB", "AF_COORDINATOR_SOCKET"} {
		t.Setenv(key, "")
	}
	legacyDB := filepath.Join(home, ".local/share/af-coordinator/af-coordinator.db")
	newDB := filepath.Join(home, ".local/share/dibs/dibs.db")
	if err := os.MkdirAll(filepath.Dir(legacyDB), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacyDB, []byte("existing"), 0o600); err != nil {
		t.Fatal(err)
	}
	got := Default()
	if got.DBPath != legacyDB || got.SocketPath != filepath.Join(home, ".local/state/af-coordinator/af-coordinator.sock") {
		t.Fatalf("legacy state not selected: %+v", got)
	}
	if _, err := os.Stat(newDB); !os.IsNotExist(err) {
		t.Fatalf("default config created second DB: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(newDB), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(newDB, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := Default(); got.DBPath != legacyDB {
		t.Fatalf("legacy DB lost authority when both paths exist: %+v", got)
	}
}
