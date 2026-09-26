package doctor

import (
	"context"
	"database/sql"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/abevz/dibs/internal/config"
	"github.com/abevz/dibs/internal/core"
	"github.com/abevz/dibs/internal/testsocket"
)

func TestEvaluateSocketPath(t *testing.T) {
	path := "/" + strings.Repeat("x", len(syscall.RawSockaddrUnix{}.Path))
	result := EvaluateSocketPath(config.Config{SocketPath: path})
	if result.Status != "WARN" || !strings.Contains(result.Message, path) || !strings.Contains(result.Message, "DIBS_SOCKET") {
		t.Fatalf("result = %+v, want actionable warning", result)
	}
}

type mockExec struct {
	cmdOut []byte
	cmdErr error
	env    map[string]string
}

func TestEvaluateOperatorTokenMigration(t *testing.T) {
	configured, missing := true, false
	for _, tc := range []struct {
		name, legacyPath, want string
		configured             *bool
	}{
		{"legacy drop-in with missing token", ".config/systemd/user/af-coordinatord.service.d/operator-token.conf", "WARN", &missing},
		{"legacy env file with missing token", ".config/af-coordinator/operator.env", "WARN", &missing},
		{"no legacy config", "", "ok", &missing},
		{"token configured", ".config/af-coordinator/operator.env", "ok", &configured},
		{"older daemon status unknown", ".config/af-coordinator/operator.env", "ok", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			if tc.legacyPath != "" {
				path := filepath.Join(home, tc.legacyPath)
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte("AF_OPERATOR_TOKEN=do-not-print"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			result := EvaluateOperatorTokenMigration(&core.Health{OperatorTokenConfigured: tc.configured}, home)
			if result.Status != tc.want {
				t.Fatalf("status = %s, want %s: %s", result.Status, tc.want, result.Message)
			}
			if strings.Contains(result.Message+result.Hint, "do-not-print") {
				t.Fatal("doctor exposed token content")
			}
		})
	}
}

func (m mockExec) Command(name string, arg ...string) ([]byte, error) {
	return m.cmdOut, m.cmdErr
}

func (m mockExec) LookupEnv(key string) (string, bool) {
	v, ok := m.env[key]
	return v, ok
}

func TestEvaluateBinaryRevision(t *testing.T) {
	goModOK := func() ([]byte, error) { return []byte("module github.com/abevz/dibs\n"), nil }
	goModMissing := func() ([]byte, error) { return nil, errors.New("no such file") }
	goModOther := func() ([]byte, error) { return []byte("module example.com/other\n"), nil }

	tests := []struct {
		name      string
		h         *core.Health
		e         mockExec
		readGoMod func() ([]byte, error)
		expected  string
	}{
		{
			name:      "nil health",
			h:         nil,
			e:         mockExec{},
			readGoMod: goModOK,
			expected:  "WARN",
		},
		{
			name:      "daemon revision unknown",
			h:         &core.Health{Revision: "unknown"},
			e:         mockExec{},
			readGoMod: goModOK,
			expected:  "ok",
		},
		{
			name:      "daemon revision empty",
			h:         &core.Health{Revision: ""},
			e:         mockExec{},
			readGoMod: goModOK,
			expected:  "ok",
		},
		{
			name:      "not run from the af-coordinator checkout",
			h:         &core.Health{Revision: "abc123"},
			e:         mockExec{cmdOut: []byte("abc123\n")},
			readGoMod: goModOther,
			expected:  "ok",
		},
		{
			name:      "no go.mod found",
			h:         &core.Health{Revision: "abc123"},
			e:         mockExec{cmdOut: []byte("abc123\n")},
			readGoMod: goModMissing,
			expected:  "ok",
		},
		{
			name:      "git rev-parse fails",
			h:         &core.Health{Revision: "abc123"},
			e:         mockExec{cmdErr: errors.New("not a git repository")},
			readGoMod: goModOK,
			expected:  "ok",
		},
		{
			name:      "revision mismatch",
			h:         &core.Health{Revision: "deadbeef"},
			e:         mockExec{cmdOut: []byte("abc123\n")},
			readGoMod: goModOK,
			expected:  "WARN",
		},
		{
			name:      "revision match",
			h:         &core.Health{Revision: "abc123"},
			e:         mockExec{cmdOut: []byte("abc123\n")},
			readGoMod: goModOK,
			expected:  "ok",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res := evaluateBinaryRevision(tc.h, tc.e, tc.readGoMod)
			if res.Status != tc.expected {
				t.Errorf("expected %s, got %s: %s", tc.expected, res.Status, res.Message)
			}
		})
	}
}

func TestEvaluateConfigMismatch(t *testing.T) {
	cfg := config.Config{
		SocketPath: "/tmp/sock",
		DBPath:     "/tmp/db",
	}

	tests := []struct {
		name     string
		h        *core.Health
		expected string
	}{
		{
			name:     "match",
			h:        &core.Health{SocketPath: "/tmp/sock", DBPath: "/tmp/db"},
			expected: "ok",
		},
		{
			name:     "socket mismatch",
			h:        &core.Health{SocketPath: "/tmp/other", DBPath: "/tmp/db"},
			expected: "WARN",
		},
		{
			name:     "db mismatch",
			h:        &core.Health{SocketPath: "/tmp/sock", DBPath: "/tmp/other"},
			expected: "WARN",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res := EvaluateConfigMismatch(tc.h, cfg)
			if res.Status != tc.expected {
				t.Errorf("expected %s, got %s: %s", tc.expected, res.Status, res.Message)
			}
		})
	}
}

func TestEvaluateDuplicates(t *testing.T) {
	dir1 := t.TempDir()
	dir2 := t.TempDir()

	dibs1 := filepath.Join(dir1, "dibs")
	os.WriteFile(dibs1, []byte("dummy1"), 0755)

	dibs2 := filepath.Join(dir2, "dibs")
	os.WriteFile(dibs2, []byte("dummy2"), 0755)

	daemon := filepath.Join(dir1, "dibsd")
	os.WriteFile(daemon, []byte("dummy"), 0644) // not executable

	e := mockExec{
		env: map[string]string{
			"PATH": dir1 + string(os.PathListSeparator) + dir2,
		},
	}

	res := EvaluateDuplicates(e)
	if res.Status != "WARN" {
		t.Errorf("expected WARN, got %s: %s", res.Status, res.Message)
	}
}

func TestEvaluateBackup(t *testing.T) {
	ctx := context.Background()
	e := mockExec{
		cmdOut: []byte("enabled\n"),
	}

	dir1 := t.TempDir()
	res := EvaluateBackup(ctx, e, filepath.Join(dir1, "not-exist"), time.Now())
	if res.Status != "WARN" {
		t.Errorf("expected WARN for missing dir")
	}

	res = EvaluateBackup(ctx, e, dir1, time.Now())
	if res.Status != "WARN" {
		t.Errorf("expected WARN for empty dir")
	}
}

func TestEvaluateBackupDarwinLaunchAgent(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	missing := evaluateBackup(ctx, mockExec{cmdErr: errors.New("not loaded")}, dir, time.Now(), "darwin", 1000)
	if missing.Status != "WARN" {
		t.Fatalf("expected WARN for missing LaunchAgent, got %s: %s", missing.Status, missing.Message)
	}

	backupPath := filepath.Join(dir, "af-coordinator-20260709-0317.db")
	db, err := sql.Open("sqlite", backupPath)
	if err != nil {
		t.Fatalf("open backup db: %v", err)
	}
	if _, err := db.ExecContext(ctx, `CREATE TABLE sanity (id integer primary key)`); err != nil {
		t.Fatalf("seed backup db: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close backup db: %v", err)
	}

	ok := evaluateBackup(ctx, mockExec{cmdOut: []byte("loaded\n")}, dir, time.Now(), "darwin", 1000)
	if ok.Status != "ok" {
		t.Fatalf("expected ok for loaded LaunchAgent and valid backup, got %s: %s", ok.Status, ok.Message)
	}
}

func TestSystemdUserEnv(t *testing.T) {
	// runtimeDir simulates XDG_RUNTIME_DIR, and busPath must stay exactly
	// runtimeDir+"/bus" for the assertions below. t.TempDir() embeds the full
	// test name and can push that join over the sun_path limit under a
	// sandboxed TMPDIR (afc-104, afc-105, afc-117), so use testsocket.Dir's
	// short root instead.
	runtimeDir := testsocket.Dir(t)
	busPath := filepath.Join(runtimeDir, "bus")
	listener, err := netListenUnix(busPath)
	if err != nil {
		t.Fatalf("listen unix socket: %v", err)
	}
	defer listener.Close()

	env := systemdUserEnv(func(key string) (string, bool) {
		switch key {
		case "XDG_RUNTIME_DIR":
			return runtimeDir, true
		default:
			return "", false
		}
	}, os.Stat, 1000)

	if !containsEnv(env, "DBUS_SESSION_BUS_ADDRESS=unix:path="+busPath) {
		t.Fatalf("expected DBUS_SESSION_BUS_ADDRESS for user bus socket, got %v", env)
	}

	env = systemdUserEnv(func(key string) (string, bool) {
		switch key {
		case "XDG_RUNTIME_DIR":
			return runtimeDir, true
		case "DBUS_SESSION_BUS_ADDRESS":
			return "unix:path=/custom/bus", true
		default:
			return "", false
		}
	}, os.Stat, 1000)

	if containsEnvPrefix(env, "DBUS_SESSION_BUS_ADDRESS=") {
		t.Fatalf("expected existing DBUS_SESSION_BUS_ADDRESS to be preserved, got %v", env)
	}
}

func containsEnv(env []string, want string) bool {
	for _, entry := range env {
		if entry == want {
			return true
		}
	}
	return false
}

func containsEnvPrefix(env []string, prefix string) bool {
	for _, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			return true
		}
	}
	return false
}

func netListenUnix(path string) (net.Listener, error) {
	return net.Listen("unix", path)
}
