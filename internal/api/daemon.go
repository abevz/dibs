package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/abevz/dibs/internal/build"
	"github.com/abevz/dibs/internal/config"
	"github.com/abevz/dibs/internal/core"
	"github.com/abevz/dibs/internal/store"
)

func RunDaemon(ctx context.Context, logger *slog.Logger, cfg config.Config, st store.CoordinatorStore) error {
	if err := config.ValidateSocketPath(cfg.SocketPath); err != nil {
		return err
	}
	if err := ensureRuntimeDirectory(filepath.Dir(cfg.SocketPath), cfg.UsesDefaultSocketPath()); err != nil {
		return fmt.Errorf("create socket directory: %w", err)
	}

	if err := ensureRuntimeDirectory(filepath.Dir(cfg.DBPath), cfg.UsesDefaultDBPath()); err != nil {
		return fmt.Errorf("create db directory: %w", err)
	}

	if err := removeStaleSocket(cfg.SocketPath); err != nil {
		return err
	}

	listener, err := net.Listen("unix", cfg.SocketPath)
	if err != nil {
		return fmt.Errorf("listen on unix socket: %w", err)
	}
	defer func() {
		_ = listener.Close()
		_ = os.Remove(cfg.SocketPath)
	}()
	pidPath := cfg.SocketPath + ".pid"
	if os.Getenv("DIBS_INTERNAL_AUTOSTART") == "1" {
		pid := strconv.Itoa(os.Getpid())
		if err := os.WriteFile(pidPath, []byte(pid+"\n"), 0o600); err != nil {
			return fmt.Errorf("write daemon pid file: %w", err)
		}
		defer func() {
			if data, err := os.ReadFile(pidPath); err == nil && strings.TrimSpace(string(data)) == pid {
				_ = os.Remove(pidPath)
			}
		}()
	} else if err := os.Remove(pidPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove stale self-start pid file: %w", err)
	}

	if err := os.Chmod(cfg.SocketPath, 0o660); err != nil {
		logger.Warn("failed to chmod socket", "path", cfg.SocketPath, "error", err)
	}

	mux := http.NewServeMux()
	mutationStats := &mutationCounters{}

	// Health endpoints.
	healthHandler := func(w http.ResponseWriter, r *http.Request) {
		operatorTokenConfigured := config.EnvOrDefault("DIBS_OPERATOR_TOKEN", "AF_OPERATOR_TOKEN", "") != ""
		h := core.Health{
			Name:                    "dibs",
			Status:                  "ok",
			DBPath:                  cfg.DBPath,
			SocketPath:              cfg.SocketPath,
			Time:                    time.Now().UTC(),
			Version:                 build.Version,
			Revision:                build.Revision,
			OperatorTokenConfigured: &operatorTokenConfigured,
		}

		if err := st.Ping(r.Context()); err != nil {
			h.Status = "degraded"
			logger.Warn("health check db ping failed", "error", err)
		}
		if safetyStore, ok := st.(interface {
			SafetySnapshot(context.Context, time.Time) (core.SafetySnapshot, error)
		}); ok {
			safety, err := safetyStore.SafetySnapshot(r.Context(), time.Now().UTC())
			if err != nil {
				h.Status = "degraded"
				logger.Warn("health check safety snapshot failed", "error", err)
			} else {
				h.Safety = &core.SafetyHealth{
					SingletonLockHeld:           cfg.SingletonLockHeld,
					MigrationsVerifiedAtStartup: cfg.MigrationsVerifiedAtStartup,
					IntegrityVerifiedAtStartup:  cfg.IntegrityVerifiedAtStartup,
					IntegrityPolicy:             "startup_integrity_check",
					ActiveLeases:                safety.ActiveLeases,
					ExpiredLeases:               safety.ExpiredLeases,
					StaleRejections:             safety.StaleRejections,
					DurableClaimConflicts:       safety.ClaimConflicts,
					LatestMigration:             safety.LatestMigration,
					MutationCounters:            mutationStats.snapshot(),
				}
			}
		}

		writeJSON(w, http.StatusOK, h)
	}

	mux.HandleFunc("/healthz", healthHandler)
	mux.HandleFunc("/v1/health", healthHandler)
	mux.HandleFunc("GET /v1/export/jsonl", handleExportJSONL(st, logger))
	mux.HandleFunc("GET /v1/stats", handleStats(st, logger))

	// Project registration.
	mux.HandleFunc("POST /v1/projects", handleCreateProject(st, logger))
	mux.HandleFunc("GET /v1/projects", handleListProjects(st, logger))

	// Repository registration.
	mux.HandleFunc("POST /v1/repos", handleCreateRepo(st, logger))
	mux.HandleFunc("GET /v1/repos", handleListRepos(st, logger))
	mux.HandleFunc("POST /v1/repos/{repo_id}/relocate", handleRelocateRepo(st, logger))

	// Worktree registration.
	mux.HandleFunc("POST /v1/worktrees", handleRegisterWorktree(st, logger))
	mux.HandleFunc("GET /v1/worktrees", handleListWorktrees(st, logger))
	mux.HandleFunc("DELETE /v1/worktrees/{worktree_id}", handleDeleteWorktree(st, logger))

	// Artifact root registration.
	mux.HandleFunc("POST /v1/artifact-roots", handleCreateArtifactRoot(st, logger))
	mux.HandleFunc("GET /v1/artifact-roots", handleListArtifactRoots(st, logger))

	// Artifact registration.
	mux.HandleFunc("POST /v1/artifacts", handleCreateArtifact(st, logger))
	mux.HandleFunc("GET /v1/artifacts", handleListArtifacts(st, logger))

	// Issue registration.
	mux.HandleFunc("POST /v1/issues", handleCreateIssue(st, logger))
	mux.HandleFunc("GET /v1/issues/ready", handleListReadyIssues(st, logger))
	mux.HandleFunc("GET /v1/issues/{issue_id}", handleGetIssue(st, logger))
	mux.HandleFunc("POST /v1/issues/{issue_id}/claim", handleClaimIssue(st, logger))
	mux.HandleFunc("POST /v1/issues/{issue_id}/heartbeat", handleHeartbeatLease(st, logger))
	mux.HandleFunc("POST /v1/issues/{issue_id}/release", handleReleaseLease(st, logger))
	mux.HandleFunc("POST /v1/issues/{issue_id}/handoff", handleHandoffLease(st, logger))
	mux.HandleFunc("PATCH /v1/issues/{issue_id}", handleUpdateIssue(st, logger))
	mux.HandleFunc("POST /v1/issues/{issue_id}/close", handleCloseIssue(st, logger))
	mux.HandleFunc("POST /v1/issues/{issue_id}/operator-close", handleOperatorCloseIssue(st, logger))
	mux.HandleFunc("POST /v1/issues/{issue_id}/operator-reopen", handleOperatorReopenIssue(st, logger))
	mux.HandleFunc("POST /v1/issues/{issue_id}/operator-release", handleOperatorReleaseIssue(st, logger))
	mux.HandleFunc("POST /v1/issues/{issue_id}/dependencies", handleAddDependency(st, logger))
	mux.HandleFunc("DELETE /v1/issues/{issue_id}/dependencies/{depends_on}", handleRemoveDependency(st, logger))
	mux.HandleFunc("POST /v1/issues/{issue_id}/links", handleLinkArtifact(st, logger))
	mux.HandleFunc("DELETE /v1/issues/{issue_id}/links", handleUnlinkArtifact(st, logger))
	mux.HandleFunc("GET /v1/issues/{issue_id}/links", handleListIssueLinks(st, logger))
	mux.HandleFunc("POST /v1/issues/{issue_id}/tags", handleAddTag(st, logger))
	mux.HandleFunc("DELETE /v1/issues/{issue_id}/tags", handleRemoveTag(st, logger))
	mux.HandleFunc("POST /v1/issues/{issue_id}/notes", handleCreateNote(st, logger))
	mux.HandleFunc("GET /v1/issues/{issue_id}/notes", handleListNotes(st, logger))
	mux.HandleFunc("GET /v1/issues/{issue_id}/events", handleListEvents(st, logger))
	mux.HandleFunc("GET /v1/events", handleWatchEvents(st, logger))
	mux.HandleFunc("GET /v1/events/recent", handleRecentEvents(st, logger))
	mux.HandleFunc("GET /v1/issues", handleListIssues(st, logger))

	server := &http.Server{
		Handler:           observeMutations(mux, logger, mutationStats),
		ReadHeaderTimeout: 5 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		logger.Info("daemon started", "socket", cfg.SocketPath, "db", cfg.DBPath)
		if serveErr := server.Serve(listener); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			errCh <- serveErr
			return
		}
		errCh <- nil
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		if err := server.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shutdown server: %w", err)
		}

		return nil
	case err := <-errCh:
		if err != nil {
			return fmt.Errorf("serve http: %w", err)
		}

		return nil
	}
}

func ensureRuntimeDirectory(path string, normalizeExisting bool) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	if normalizeExisting {
		if err := os.Chmod(path, 0o700); err != nil {
			return err
		}
	}
	return nil
}

func removeStaleSocket(path string) error {
	info, err := os.Stat(path)
	if err == nil {
		if info.Mode()&os.ModeSocket == 0 {
			return fmt.Errorf("path exists and is not a socket: %s", path)
		}
		conn, dialErr := net.DialTimeout("unix", path, 250*time.Millisecond)
		if dialErr == nil {
			_ = conn.Close()
			return fmt.Errorf("a daemon is already listening on socket: %s", path)
		}
		if !errors.Is(dialErr, syscall.ECONNREFUSED) && !errors.Is(dialErr, syscall.ENOENT) {
			return fmt.Errorf("refuse to remove unresponsive socket %s: %w", path, dialErr)
		}

		if err := os.Remove(path); err != nil {
			return fmt.Errorf("remove stale socket: %w", err)
		}

		return nil
	}

	if os.IsNotExist(err) {
		return nil
	}

	return fmt.Errorf("stat socket path: %w", err)
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
