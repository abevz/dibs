package doctor

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/abevz/dibs/internal/build"
	"github.com/abevz/dibs/internal/client"
	"github.com/abevz/dibs/internal/config"
	"github.com/abevz/dibs/internal/core"
	_ "modernc.org/sqlite"
)

type Result struct {
	Name    string `json:"name"`
	Status  string `json:"status"` // "ok" or "WARN"
	Message string `json:"message"`
	Hint    string `json:"hint,omitempty"`
}

// OSExec is an interface for mocking exec.Command and environment lookup
type OSExec interface {
	Command(name string, arg ...string) ([]byte, error)
	LookupEnv(key string) (string, bool)
}

type realExec struct{}

func (realExec) Command(name string, arg ...string) ([]byte, error) {
	var cmd *exec.Cmd
	if name == "gh" {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		cmd = exec.CommandContext(ctx, name, arg...)
	} else {
		cmd = exec.Command(name, arg...)
	}
	if runtime.GOOS == "linux" && name == "systemctl" && isSystemctlUserCommand(arg) {
		cmd.Env = append(os.Environ(), systemdUserEnv(os.LookupEnv, os.Stat, os.Getuid())...)
	}
	return cmd.CombinedOutput()
}

// EvaluateGitHubCLI checks each prerequisite in order. GitHub is optional, so
// every failure is a warning and local dibs operation remains available.
func EvaluateGitHubCLI(e OSExec) Result {
	const name = "GitHub CLI"
	version, err := e.Command("gh", "--version")
	if err != nil {
		return Result{Name: name, Status: "WARN", Message: "gh not found", Hint: "Install GitHub CLI to use import and publish"}
	}
	firstLine := strings.TrimSpace(strings.SplitN(string(version), "\n", 2)[0])
	if firstLine == "" {
		return Result{Name: name, Status: "WARN", Message: "gh version unavailable", Hint: "Check the GitHub CLI installation"}
	}
	if !githubCLIVersionSupported(firstLine) {
		return Result{Name: name, Status: "WARN", Message: firstLine + " is too old for gh api --slurp", Hint: "Install GitHub CLI 2.48.0 or newer to use import and publish"}
	}
	if _, err := e.Command("gh", "auth", "status", "--hostname", "github.com"); err != nil {
		return Result{Name: name, Status: "WARN", Message: "not logged in to github.com", Hint: "Run gh auth login"}
	}
	out, err := e.Command("gh", "api", "rate_limit")
	if err != nil {
		return Result{Name: name, Status: "WARN", Message: "authenticated GitHub API check failed", Hint: "Check network, proxy, or token; gh api rate_limit: " + strings.Join(strings.Fields(string(out)), " ")}
	}
	var rate struct {
		Resources struct {
			Core *struct {
				Remaining int `json:"remaining"`
			} `json:"core"`
		} `json:"resources"`
	}
	if err := json.Unmarshal(out, &rate); err != nil || rate.Resources.Core == nil {
		return Result{Name: name, Status: "WARN", Message: "invalid GitHub API response", Hint: "Check gh api rate_limit"}
	}
	return Result{Name: name, Status: "ok", Message: fmt.Sprintf("%s; %d core API requests remaining", firstLine, rate.Resources.Core.Remaining)}
}

func githubCLIVersionSupported(line string) bool {
	fields := strings.Fields(line)
	if len(fields) < 3 || fields[0] != "gh" || fields[1] != "version" {
		return false
	}
	parts := strings.SplitN(fields[2], ".", 3)
	if len(parts) != 3 {
		return false
	}
	major, errMajor := strconv.Atoi(parts[0])
	minor, errMinor := strconv.Atoi(parts[1])
	patch, errPatch := strconv.Atoi(parts[2])
	if errMajor != nil || errMinor != nil || errPatch != nil || major < 0 || minor < 0 || patch < 0 {
		return false
	}
	return major > 2 || (major == 2 && minor >= 48)
}

func (realExec) LookupEnv(key string) (string, bool) {
	return os.LookupEnv(key)
}

func isSystemctlUserCommand(args []string) bool {
	for _, arg := range args {
		if arg == "--user" {
			return true
		}
	}
	return false
}

func systemdUserEnv(lookup func(string) (string, bool), stat func(string) (os.FileInfo, error), uid int) []string {
	var env []string

	runtimeDir, hasRuntimeDir := lookup("XDG_RUNTIME_DIR")
	if !hasRuntimeDir || runtimeDir == "" {
		candidate := fmt.Sprintf("/run/user/%d", uid)
		if info, err := stat(candidate); err == nil && info.IsDir() {
			runtimeDir = candidate
			env = append(env, "XDG_RUNTIME_DIR="+runtimeDir)
		}
	}

	if runtimeDir == "" {
		return env
	}

	if busAddress, hasBusAddress := lookup("DBUS_SESSION_BUS_ADDRESS"); hasBusAddress && busAddress != "" {
		return env
	}

	busPath := filepath.Join(runtimeDir, "bus")
	if info, err := stat(busPath); err == nil && info.Mode()&os.ModeSocket != 0 {
		env = append(env, "DBUS_SESSION_BUS_ADDRESS=unix:path="+busPath)
	}

	return env
}

func EvaluateDaemon(ctx context.Context, c *client.Client) (Result, *core.Health) {
	h, err := c.Health(ctx)
	if err != nil {
		return Result{
			Name:    "Daemon reachable",
			Status:  "WARN",
			Message: "Daemon is unreachable",
			Hint:    "Start or restart dibsd: " + restartDaemonHint(),
		}, nil
	}
	return Result{
		Name:    "Daemon reachable",
		Status:  "ok",
		Message: "Daemon is reachable and responding",
	}, &h
}

// EvaluateBinaryVersion compares the installed CLI and responding daemon. An
// older daemon without a version field should be restarted after an upgrade.
func EvaluateBinaryVersion(h *core.Health) Result {
	return evaluateBinaryVersion(h, build.Version)
}

func evaluateBinaryVersion(h *core.Health, cliVersion string) Result {
	const name = "Binary version"
	if h == nil {
		return Result{Name: name, Status: "WARN", Message: "Daemon version unavailable"}
	}
	if h.Version == "" {
		return Result{Name: name, Status: "WARN", Message: "Daemon does not report a version", Hint: "Rebuild and restart dibsd"}
	}
	if h.Version != cliVersion {
		return Result{
			Name: name, Status: "WARN",
			Message: fmt.Sprintf("CLI version %s differs from daemon version %s", cliVersion, h.Version),
			Hint:    "Install matching dibs and dibsd binaries, then restart dibsd",
		}
	}
	return Result{Name: name, Status: "ok", Message: "CLI and daemon versions match (" + cliVersion + ")"}
}

// EvaluateBinaryRevision compares the daemon's build.Revision (the git SHA
// it was compiled from, set by `make build`/`make build-install`) against
// `git rev-parse HEAD` in the current working directory. It catches a
// daemon still running an older commit after a merge that was never
// followed by a rebuild+restart — the exact class of staleness this check
// exists to close.
//
// It is best-effort and never fails hard: if it can't determine either side
// (not run from inside a git checkout, not run from the af-coordinator
// checkout, or the daemon wasn't built with revision embedding), it reports
// "ok" with an explanatory message rather than a false WARN.
func EvaluateBinaryRevision(h *core.Health, e OSExec) Result {
	return evaluateBinaryRevision(h, e, func() ([]byte, error) { return os.ReadFile("go.mod") })
}

func evaluateBinaryRevision(h *core.Health, e OSExec, readGoMod func() ([]byte, error)) Result {
	const name = "Binary revision"
	if h == nil {
		return Result{Name: name, Status: "WARN", Message: "Daemon health data unavailable"}
	}
	if h.Revision == "" || h.Revision == "unknown" {
		return Result{
			Name:    name,
			Status:  "ok",
			Message: "Daemon revision unknown (not built via `make build-install`); skipping staleness check",
		}
	}
	modData, err := readGoMod()
	if err != nil || !strings.Contains(string(modData), "module github.com/abevz/dibs") {
		return Result{
			Name:    name,
			Status:  "ok",
			Message: "Not run from inside the af-coordinator checkout; skipping staleness check",
		}
	}
	out, err := e.Command("git", "rev-parse", "HEAD")
	if err != nil {
		return Result{
			Name:    name,
			Status:  "ok",
			Message: "Could not resolve local git HEAD; skipping staleness check",
		}
	}
	localHEAD := strings.TrimSpace(string(out))
	if localHEAD == "" {
		return Result{
			Name:    name,
			Status:  "ok",
			Message: "Could not resolve local git HEAD; skipping staleness check",
		}
	}
	if h.Revision != localHEAD {
		return Result{
			Name:   name,
			Status: "WARN",
			Message: fmt.Sprintf("daemon binary is running revision %s, but HEAD here is %s",
				shortRev(h.Revision), shortRev(localHEAD)),
			Hint: "Rebuild and restart: make restart-service",
		}
	}
	return Result{
		Name:    name,
		Status:  "ok",
		Message: "Daemon binary matches local HEAD revision",
	}
}

func shortRev(rev string) string {
	if len(rev) > 12 {
		return rev[:12]
	}
	return rev
}

// EvaluateOperatorTokenMigration checks only for legacy configuration files,
// never their contents. Older daemons omit the health flag, so their token
// status is unknown and must not be reported as absent.
func EvaluateOperatorTokenMigration(h *core.Health, home string) Result {
	result := Result{Name: "Operator token migration", Status: "ok", Message: "No legacy operator token migration warning"}
	if h == nil || h.OperatorTokenConfigured == nil || *h.OperatorTokenConfigured {
		return result
	}
	for _, path := range []string{
		filepath.Join(home, ".config/systemd/user/af-coordinatord.service.d/operator-token.conf"),
		filepath.Join(home, ".config/af-coordinator/operator.env"),
	} {
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return Result{
				Name: "Operator token migration", Status: "WARN",
				Message: "Daemon has no operator token configured, but legacy operator token configuration exists",
				Hint:    "Point dibsd.service.d/operator-token.conf at the existing ~/.config/af-coordinator/operator.env and reload the user manager; see docs/operations.md",
			}
		}
	}
	return result
}

func EvaluateBackup(ctx context.Context, e OSExec, backupDir string, now time.Time) Result {
	return evaluateBackup(ctx, e, backupDir, now, runtime.GOOS, os.Getuid())
}

func evaluateBackup(ctx context.Context, e OSExec, backupDir string, now time.Time, goos string, uid int) Result {
	switch goos {
	case "darwin":
		out, err := e.Command("launchctl", "print", fmt.Sprintf("gui/%d/com.abevz.af-coordinator-backup", uid))
		if err != nil || strings.TrimSpace(string(out)) == "" {
			return Result{
				Name:    "Backup agent",
				Status:  "WARN",
				Message: "Backup LaunchAgent is not loaded",
				Hint:    "Run: make install-backup",
			}
		}
	case "linux":
		out, err := e.Command("systemctl", "--user", "is-enabled", "af-coordinator-backup.timer")
		if err != nil || strings.TrimSpace(string(out)) != "enabled" {
			return Result{
				Name:    "Backup timer",
				Status:  "WARN",
				Message: "Backup timer is not enabled",
				Hint:    "Run: systemctl --user enable --now af-coordinator-backup.timer",
			}
		}
	default:
		return Result{
			Name:    "Backup",
			Status:  "WARN",
			Message: "Automated backup is not supported for this OS",
			Hint:    "Use manual SQLite VACUUM INTO backups",
		}
	}

	return evaluateBackupFiles(ctx, backupDir, now)
}

func evaluateBackupFiles(ctx context.Context, backupDir string, now time.Time) Result {
	entries, err := os.ReadDir(backupDir)
	if err != nil {
		if os.IsNotExist(err) {
			return Result{
				Name:    "Backup files",
				Status:  "WARN",
				Message: "Backup directory does not exist",
				Hint:    "Wait for the backup job to run or trigger it manually",
			}
		}
		return Result{
			Name:    "Backup files",
			Status:  "WARN",
			Message: fmt.Sprintf("Cannot read backup directory: %v", err),
			Hint:    "Check permissions of " + backupDir,
		}
	}

	var latestBackup string
	var latestModTime time.Time
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".db") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if info.ModTime().After(latestModTime) {
			latestModTime = info.ModTime()
			latestBackup = filepath.Join(backupDir, entry.Name())
		}
	}

	if latestBackup == "" {
		return Result{
			Name:    "Backup files",
			Status:  "WARN",
			Message: "No backup databases found",
			Hint:    "Run the backup job manually",
		}
	}

	if now.Sub(latestModTime) > 48*time.Hour {
		return Result{
			Name:    "Backup files",
			Status:  "WARN",
			Message: "Last backup is older than 48 hours",
			Hint:    "Check backup job logs",
		}
	}

	db, err := sql.Open("sqlite", latestBackup)
	if err != nil {
		return Result{
			Name:    "Backup integrity",
			Status:  "WARN",
			Message: fmt.Sprintf("Failed to open latest backup: %v", err),
			Hint:    "Check backup file: " + latestBackup,
		}
	}
	defer db.Close()

	var integrity string
	if err := db.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&integrity); err != nil {
		return Result{
			Name:    "Backup integrity",
			Status:  "WARN",
			Message: fmt.Sprintf("Integrity check query failed: %v", err),
			Hint:    "Check backup file: " + latestBackup,
		}
	}

	if integrity != "ok" {
		return Result{
			Name:    "Backup integrity",
			Status:  "WARN",
			Message: fmt.Sprintf("Integrity check failed: %s", integrity),
			Hint:    "Investigate the corruption in " + latestBackup,
		}
	}

	return Result{
		Name:    "Backup",
		Status:  "ok",
		Message: "Backup automation enabled, recent backup present and intact",
	}
}

func restartDaemonHint() string {
	switch runtime.GOOS {
	case "darwin":
		return "launchctl kickstart -k gui/$(id -u)/com.abevz.dibsd"
	case "linux":
		return "systemctl --user restart dibsd"
	default:
		return "run dibsd in the foreground or restart your local service manager"
	}
}

func EvaluateDuplicates(e OSExec) Result {
	pathEnv, ok := e.LookupEnv("PATH")
	if !ok {
		return Result{Name: "Duplicate binaries", Status: "WARN", Message: "PATH environment variable not set"}
	}
	dirs := filepath.SplitList(pathEnv)

	findDups := func(bin string) []string {
		var found []string
		seenPath := make(map[string]bool)
		for _, dir := range dirs {
			if dir == "" {
				continue
			}
			full := filepath.Join(dir, bin)
			if info, err := os.Stat(full); err == nil && !info.IsDir() {
				if !seenPath[full] {
					if info.Mode()&0111 != 0 { // is executable
						found = append(found, full)
						seenPath[full] = true
					}
				}
			}
		}

		if len(found) <= 1 {
			return nil
		}

		// check if they differ in content
		firstHash := ""
		differ := false
		for _, f := range found {
			data, err := os.ReadFile(f)
			if err != nil {
				continue
			}
			hash := fmt.Sprintf("%x", sha256.Sum256(data))
			if firstHash == "" {
				firstHash = hash
			} else if hash != firstHash {
				differ = true
				break
			}
		}

		if differ {
			return found
		}
		return nil
	}

	dibsDups := findDups("dibs")
	daemonDups := findDups("dibsd")

	var msgs []string
	if len(dibsDups) > 1 {
		msgs = append(msgs, fmt.Sprintf("Multiple dibs found: %s", strings.Join(dibsDups, ", ")))
	}
	if len(daemonDups) > 1 {
		msgs = append(msgs, fmt.Sprintf("Multiple dibsd found: %s", strings.Join(daemonDups, ", ")))
	}

	if len(msgs) > 0 {
		return Result{
			Name:    "Duplicate binaries",
			Status:  "WARN",
			Message: strings.Join(msgs, "; "),
			Hint:    "Remove stale binaries from PATH, or ensure Makefile targets are correct (make build-install targets ~/.local/bin)",
		}
	}

	return Result{
		Name:    "Duplicate binaries",
		Status:  "ok",
		Message: "No duplicate binaries in PATH",
	}
}

func EvaluateConfigMismatch(h *core.Health, cfg config.Config) Result {
	if h == nil {
		return Result{Name: "Config mismatch", Status: "WARN", Message: "Daemon health data unavailable"}
	}

	if h.SocketPath != cfg.SocketPath {
		return Result{
			Name:    "Socket path mismatch",
			Status:  "WARN",
			Message: fmt.Sprintf("Client socket (%s) != Daemon socket (%s)", cfg.SocketPath, h.SocketPath),
			Hint:    "Check DIBS_SOCKET env var consistency",
		}
	}
	if h.DBPath != cfg.DBPath {
		return Result{
			Name:    "DB path mismatch",
			Status:  "WARN",
			Message: fmt.Sprintf("Client expected DB (%s) != Daemon DB (%s)", cfg.DBPath, h.DBPath),
			Hint:    "Check DIBS_DB env var consistency",
		}
	}

	return Result{
		Name:    "Config match",
		Status:  "ok",
		Message: "Client config matches daemon runtime state",
	}
}

func EvaluateSocketPath(cfg config.Config) Result {
	const name = "Socket path"
	if err := config.ValidateSocketPath(cfg.SocketPath); err != nil {
		return Result{Name: name, Status: "WARN", Message: err.Error(), Hint: "Set DIBS_SOCKET to a shorter path"}
	}
	return Result{Name: name, Status: "ok", Message: "Configured Unix socket path fits the platform limit"}
}

func RunAll(ctx context.Context, c *client.Client, cfg config.Config) []Result {
	results := []Result{EvaluateSocketPath(cfg)}

	resDaemon, h := EvaluateDaemon(ctx, c)
	results = append(results, resDaemon)
	results = append(results, EvaluateBinaryVersion(h))

	e := realExec{}
	results = append(results, EvaluateBinaryRevision(h, e))
	home, err := os.UserHomeDir()
	if err == nil {
		results = append(results, EvaluateOperatorTokenMigration(h, home))
		backupDir := filepath.Join(home, "backups", "dibs")
		if _, err := os.Stat(backupDir); os.IsNotExist(err) {
			legacyDir := filepath.Join(home, "backups", "af-coordinator")
			if _, err := os.Stat(legacyDir); err == nil {
				backupDir = legacyDir
			}
		}
		results = append(results, EvaluateBackup(ctx, e, backupDir, time.Now()))
	} else {
		results = append(results, Result{Name: "Backup", Status: "WARN", Message: "Cannot get user home dir"})
	}

	results = append(results, EvaluateDuplicates(e))
	results = append(results, EvaluateGitHubCLI(e))
	results = append(results, EvaluateConfigMismatch(h, cfg))

	return results
}
