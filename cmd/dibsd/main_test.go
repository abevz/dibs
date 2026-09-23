package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/abevz/dibs/internal/client"
	"github.com/abevz/dibs/internal/store/sqlite"
	"github.com/abevz/dibs/migrations"
)

func TestDaemonUsesExistingLegacyDatabaseWithoutCreatingSecondDB(t *testing.T) {
	home, err := os.MkdirTemp("", "dibs-home-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	legacyDB := filepath.Join(home, ".local/share/af-coordinator/af-coordinator.db")
	legacySocket := filepath.Join(home, ".local/state/af-coordinator/af-coordinator.sock")
	newDB := filepath.Join(home, ".local/share/dibs/dibs.db")
	if err := os.MkdirAll(filepath.Dir(legacyDB), 0o700); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(legacyDB)
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlite.Migrate(context.Background(), db, migrations.FS); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	bin := filepath.Join(t.TempDir(), "dibsd")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build dibsd: %v\n%s", err, out)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := exec.CommandContext(ctx, bin)
	cmd.Env = []string{"HOME=" + home, "PATH=" + os.Getenv("PATH")}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { cancel(); _ = cmd.Wait() }()

	c := client.New(legacySocket)
	deadline := time.Now().Add(5 * time.Second)
	for {
		probe, stop := context.WithTimeout(context.Background(), 100*time.Millisecond)
		health, err := c.Health(probe)
		stop()
		if err == nil {
			if health.Name != "dibs" {
				t.Fatalf("daemon health name = %q, want dibs", health.Name)
			}
			if health.DBPath != legacyDB {
				t.Fatalf("daemon DB = %q, want %q", health.DBPath, legacyDB)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("legacy daemon did not start: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, err := os.Stat(newDB); !os.IsNotExist(err) {
		t.Fatalf("new DB created beside legacy DB: %v", err)
	}
	if entries, err := os.ReadDir(filepath.Dir(newDB)); err == nil && len(entries) > 0 {
		t.Fatalf("unexpected new DB directory contents: %v", entries)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
}
