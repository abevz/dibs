package config

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

const (
	defaultSocketPath = "~/.local/state/dibs/dibsd.sock"
	defaultDBPath     = "~/.local/share/dibs/dibs.db"
	legacySocketPath  = "~/.local/state/af-coordinator/af-coordinator.sock"
	legacyDBPath      = "~/.local/share/af-coordinator/af-coordinator.db"
)

type Config struct {
	SocketPath string
	DBPath     string
	LogLevel   string
}

func Default() Config {
	dbPath, socketPath := defaultPaths()
	if value, ok := Env("DIBS_DB", "AF_COORDINATOR_DB"); ok && strings.TrimSpace(value) != "" {
		dbPath = value
		if filepath.Clean(expandHome(value)) == filepath.Clean(expandHome(legacyDBPath)) {
			socketPath = legacySocketPath
		}
	}
	if value, ok := Env("DIBS_SOCKET", "AF_COORDINATOR_SOCKET"); ok && strings.TrimSpace(value) != "" {
		socketPath = value
	}
	return Config{
		SocketPath: expandHome(socketPath),
		DBPath:     expandHome(dbPath),
		LogLevel:   EnvOrDefault("DIBS_LOG_LEVEL", "AF_COORDINATOR_LOG_LEVEL", "info"),
	}
}

func defaultPaths() (dbPath, socketPath string) {
	// Existing coordinator state is authoritative until an explicit migration.
	// Prefer it even if a new-path file also exists; never silently create a
	// second database next to a live one.
	if info, err := os.Stat(expandHome(legacyDBPath)); err == nil && !info.IsDir() {
		return legacyDBPath, legacySocketPath
	}
	return defaultDBPath, defaultSocketPath
}

// Env resolves the canonical name before its legacy alias. Presence matters:
// an explicitly empty DIBS_* value must not fall through to an AF_* secret.
func Env(canonical, legacy string) (string, bool) {
	if value, ok := os.LookupEnv(canonical); ok {
		return value, true
	}
	return os.LookupEnv(legacy)
}

func EnvOrDefault(canonical, legacy, fallback string) string {
	value, ok := Env(canonical, legacy)
	if !ok || strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func (c Config) SlogLevel() slog.Level {
	switch strings.ToLower(c.LogLevel) {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// UsesDefaultSocketPath reports whether the daemon owns the socket directory
// layout and may safely normalize its permissions. Custom path parents remain
// operator-managed because they may intentionally be shared.
func (c Config) UsesDefaultSocketPath() bool {
	path := filepath.Clean(c.SocketPath)
	return path == filepath.Clean(expandHome(defaultSocketPath)) || path == filepath.Clean(expandHome(legacySocketPath))
}

// UsesDefaultDBPath reports whether the daemon owns the database directory
// layout and may safely normalize its permissions. Custom path parents remain
// operator-managed because chmodding an arbitrary configured directory could
// affect unrelated files.
func (c Config) UsesDefaultDBPath() bool {
	path := filepath.Clean(c.DBPath)
	return path == filepath.Clean(expandHome(defaultDBPath)) || path == filepath.Clean(expandHome(legacyDBPath))
}

func (c Config) UsesLegacyDBPath() bool {
	return filepath.Clean(c.DBPath) == filepath.Clean(expandHome(legacyDBPath))
}

func expandHome(path string) string {
	if path == "" || path[0] != '~' {
		return path
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}

	if path == "~" {
		return home
	}

	return filepath.Join(home, strings.TrimPrefix(path, "~/"))
}
