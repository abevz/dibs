package api

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/abevz/dibs/internal/config"
	"github.com/abevz/dibs/internal/store/sqlite"
	"github.com/abevz/dibs/internal/testsocket"
	"github.com/abevz/dibs/migrations"
)

type daemonHelperProcess struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	done   chan error
	stderr bytes.Buffer
}

func startDaemonHelperProcess(t *testing.T, dbPath, socketPath string) *daemonHelperProcess {
	t.Helper()
	process := &daemonHelperProcess{done: make(chan error, 1)}
	process.cmd = exec.Command(os.Args[0], "-test.run=^TestDaemonSingletonHelperProcess$")
	process.cmd.Env = append(os.Environ(),
		"AF_TEST_DAEMON_SINGLETON_HELPER=1",
		"AF_TEST_DAEMON_DB="+dbPath,
		"AF_TEST_DAEMON_SOCKET="+socketPath,
	)
	stdin, err := process.cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	process.stdin = stdin
	process.cmd.Stdout = &process.stderr
	process.cmd.Stderr = &process.stderr
	if err := process.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { process.done <- process.cmd.Wait() }()
	t.Cleanup(func() {
		if process.cmd.ProcessState != nil {
			return
		}
		_ = process.stdin.Close()
		_ = process.cmd.Process.Kill()
		select {
		case <-process.done:
		case <-time.After(5 * time.Second):
		}
	})
	return process
}

func waitForDaemonSocket(t *testing.T, process *daemonHelperProcess, socketPath string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-process.done:
			t.Fatalf("daemon helper exited before listening: %v\n%s", err, process.stderr.String())
		default:
		}
		conn, err := net.DialTimeout("unix", socketPath, 50*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("daemon helper did not listen on %s: %s", socketPath, process.stderr.String())
}

func stopDaemonHelperProcess(t *testing.T, process *daemonHelperProcess, abrupt bool) error {
	t.Helper()
	if abrupt {
		if err := process.cmd.Process.Kill(); err != nil {
			t.Fatal(err)
		}
	} else if err := process.stdin.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-process.done:
		return err
	case <-time.After(5 * time.Second):
		_ = process.cmd.Process.Kill()
		t.Fatalf("daemon helper did not exit: %s", process.stderr.String())
		return nil
	}
}

func TestDaemonSingletonAcrossDatabaseFileSymlinkAndCrashRecovery(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "coordinator.db")
	aliasPath := filepath.Join(t.TempDir(), "coordinator-alias.db")
	firstSocket := testsocket.PathNamed(t, "singleton-first")
	secondSocket := testsocket.PathNamed(t, "singleton-second")

	first := startDaemonHelperProcess(t, dbPath, firstSocket)
	waitForDaemonSocket(t, first, firstSocket)
	if err := os.Symlink(dbPath, aliasPath); err != nil {
		t.Fatal(err)
	}

	second := startDaemonHelperProcess(t, aliasPath, secondSocket)
	select {
	case err := <-second.done:
		if err == nil || !strings.Contains(second.stderr.String(), "already owned") {
			t.Fatalf("second daemon exit = %v, stderr = %q, want already owned", err, second.stderr.String())
		}
	case <-time.After(5 * time.Second):
		_ = second.cmd.Process.Kill()
		t.Fatal("second daemon served the same database through a file symlink")
	}
	conn, err := net.DialTimeout("unix", firstSocket, time.Second)
	if err != nil {
		t.Fatalf("first daemon socket was disturbed: %v", err)
	}
	_ = conn.Close()

	if err := stopDaemonHelperProcess(t, first, true); err == nil {
		t.Fatal("abruptly killed daemon exited successfully")
	}
	if _, err := os.Stat(firstSocket); err != nil {
		t.Fatalf("abrupt shutdown did not leave the expected stale socket: %v", err)
	}

	replacement := startDaemonHelperProcess(t, aliasPath, firstSocket)
	waitForDaemonSocket(t, replacement, firstSocket)
	if err := stopDaemonHelperProcess(t, replacement, false); err != nil {
		t.Fatalf("replacement shutdown: %v\n%s", err, replacement.stderr.String())
	}
}

func TestDaemonSingletonHelperProcess(t *testing.T) {
	if os.Getenv("AF_TEST_DAEMON_SINGLETON_HELPER") != "1" {
		return
	}
	dbPath := os.Getenv("AF_TEST_DAEMON_DB")
	socketPath := os.Getenv("AF_TEST_DAEMON_SOCKET")
	lock, err := AcquireDatabaseLock(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	db, err := sqlite.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := sqlite.Migrate(context.Background(), db, migrations.FS); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		_, _ = io.Copy(io.Discard, os.Stdin)
		cancel()
	}()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := RunDaemon(ctx, logger, config.Config{DBPath: dbPath, SocketPath: socketPath}, sqlite.NewStore(db)); err != nil {
		t.Fatal(err)
	}
}

func TestDatabaseLockExcludesSecondProcess(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "coordinator.db")
	cmd := exec.Command(os.Args[0], "-test.run=^TestDatabaseLockHelperProcess$")
	cmd.Env = append(os.Environ(),
		"AF_TEST_DATABASE_LOCK_HELPER=1",
		"AF_TEST_DATABASE_LOCK_PATH="+dbPath,
	)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = stdin.Close()
		_ = cmd.Wait()
	})
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil || line != "locked\n" {
		t.Fatalf("helper readiness = %q, %v", line, err)
	}

	if _, err := AcquireDatabaseLock(dbPath); err == nil || !strings.Contains(err.Error(), "already owned") {
		t.Fatalf("second-process lock error = %v, want already owned", err)
	}
	if err := stdin.Close(); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatal(err)
	}

	replacement, err := AcquireDatabaseLock(dbPath)
	if err != nil {
		t.Fatalf("lock after helper exit: %v", err)
	}
	if err := replacement.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestDatabaseLockHelperProcess(t *testing.T) {
	if os.Getenv("AF_TEST_DATABASE_LOCK_HELPER") != "1" {
		return
	}
	lock, err := AcquireDatabaseLock(os.Getenv("AF_TEST_DATABASE_LOCK_PATH"))
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if _, err := os.Stdout.WriteString("locked\n"); err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, os.Stdin)
}

func TestDatabaseLockUsesCanonicalPathAndSurvivesRelease(t *testing.T) {
	realDir := t.TempDir()
	aliasRoot := t.TempDir()
	aliasDir := filepath.Join(aliasRoot, "db-alias")
	if err := os.Symlink(realDir, aliasDir); err != nil {
		t.Fatal(err)
	}
	realDB := filepath.Join(realDir, "coordinator.db")
	aliasDB := filepath.Join(aliasDir, "coordinator.db")

	first, err := AcquireDatabaseLock(realDB)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = first.Close() })
	if _, err := AcquireDatabaseLock(aliasDB); err == nil || !strings.Contains(err.Error(), "already owned") {
		t.Fatalf("alias lock error = %v, want already owned", err)
	}
	info, err := os.Stat(realDB + ".lock")
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("lock mode = %o, want 600", info.Mode().Perm())
	}

	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	replacement, err := AcquireDatabaseLock(aliasDB)
	if err != nil {
		t.Fatalf("replacement lock after release: %v", err)
	}
	if err := replacement.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(realDB + ".lock"); err != nil {
		t.Fatalf("persistent lock file missing after release: %v", err)
	}
}

func TestDatabaseLockUsesCanonicalDatabaseFileSymlink(t *testing.T) {
	realDir := t.TempDir()
	realDB := filepath.Join(realDir, "coordinator.db")
	if err := os.WriteFile(realDB, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	aliasDB := filepath.Join(t.TempDir(), "coordinator-alias.db")
	if err := os.Symlink(realDB, aliasDB); err != nil {
		t.Fatal(err)
	}

	first, err := AcquireDatabaseLock(realDB)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = first.Close() })

	if _, err := AcquireDatabaseLock(aliasDB); err == nil || !strings.Contains(err.Error(), "already owned") {
		t.Fatalf("database-file alias lock error = %v, want already owned", err)
	}
	if _, err := os.Stat(aliasDB + ".lock"); !os.IsNotExist(err) {
		t.Fatalf("alias lock domain was created: %v", err)
	}
}

func TestDatabaseLockUsesDanglingDatabaseFileSymlinkTarget(t *testing.T) {
	realDir := t.TempDir()
	realDB := filepath.Join(realDir, "not-created-yet.db")
	aliasDir := t.TempDir()
	aliasDB := filepath.Join(aliasDir, "coordinator-alias.db")
	if err := os.Symlink(realDB, aliasDB); err != nil {
		t.Fatal(err)
	}

	first, err := AcquireDatabaseLock(aliasDB)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = first.Close() })

	if _, err := AcquireDatabaseLock(realDB); err == nil || !strings.Contains(err.Error(), "already owned") {
		t.Fatalf("dangling database-file alias lock error = %v, want already owned", err)
	}
	if _, err := os.Stat(aliasDB + ".lock"); !os.IsNotExist(err) {
		t.Fatalf("dangling alias lock domain was created: %v", err)
	}
}

func TestRemoveStaleSocketProbesBeforeUnlink(t *testing.T) {
	socketPath := testsocket.PathNamed(t, "coordinator")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}

	if err := removeStaleSocket(socketPath); err == nil || !strings.Contains(err.Error(), "already listening") {
		t.Fatalf("live socket probe error = %v, want already listening", err)
	}
	if _, err := os.Stat(socketPath); err != nil {
		t.Fatalf("live socket was removed: %v", err)
	}
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("live listener became unreachable: %v", err)
	}
	_ = conn.Close()

	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	if err := removeStaleSocket(socketPath); err != nil {
		t.Fatalf("remove stale socket: %v", err)
	}
	if _, err := os.Stat(socketPath); !os.IsNotExist(err) {
		t.Fatalf("stale socket still exists: %v", err)
	}
}

func TestEnsureRuntimeDirectoryNormalizesOnlyDaemonOwnedPaths(t *testing.T) {
	t.Run("daemon owned", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "owned")
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := ensureRuntimeDirectory(dir, true); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(dir)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o700 {
			t.Fatalf("owned runtime directory mode = %o, want 700", info.Mode().Perm())
		}
	})

	t.Run("custom parent", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "custom")
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := ensureRuntimeDirectory(dir, false); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(dir)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o755 {
			t.Fatalf("custom runtime directory mode = %o, want unchanged 755", info.Mode().Perm())
		}
	})
}
