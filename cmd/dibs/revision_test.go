package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abevz/dibs/internal/testsocket"
)

func TestRevisionSkew(t *testing.T) {
	// Build dibs with a known, fixed Revision so the test can control both
	// sides of the comparison deterministically (a plain `go build` here
	// would leave build.Revision at its "unknown" default, which the
	// pre-command warning intentionally never flags).
	tmpDir := t.TempDir()
	binPath := filepath.Join(tmpDir, "dibs")
	const localRevision = "test-revision-abc"
	cmd := exec.Command("go", "build", "-buildvcs=false",
		"-ldflags", "-X github.com/abevz/dibs/internal/build.Revision="+localRevision,
		"-o", binPath, ".")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("failed to build dibs: %v\noutput: %s", err, out)
	}

	tests := []struct {
		name           string
		daemonRevision string
		args           []string
		wantStderr     bool
	}{
		{
			name:           "mismatch prints warning",
			daemonRevision: "old-revision",
			args:           []string{"ls"},
			wantStderr:     true,
		},
		{
			name:           "match is silent",
			daemonRevision: localRevision,
			args:           []string{"ls"},
			wantStderr:     false,
		},
		{
			name:           "unknown daemon revision is silent",
			daemonRevision: "unknown",
			args:           []string{"ls"},
			wantStderr:     false,
		},
		{
			name:           "init ignores skew",
			daemonRevision: "old-revision",
			args:           []string{"init"},
			wantStderr:     false,
		},
	}

	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sockPath := testsocket.PathNamed(t, fmt.Sprintf("s%d", i))
			dbPath := filepath.Join(t.TempDir(), "dibs.db")
			os.Remove(sockPath)

			l, err := net.Listen("unix", sockPath)
			if err != nil {
				t.Fatalf("listen: %v", err)
			}
			defer l.Close()

			mux := http.NewServeMux()
			mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
				json.NewEncoder(w).Encode(map[string]any{"status": "ok", "revision": tt.daemonRevision, "db_path": dbPath})
			})
			// Mock /v1/projects for ls
			mux.HandleFunc("/v1/projects", func(w http.ResponseWriter, r *http.Request) {
				w.Write([]byte(`{"projects":[]}`))
			})
			go http.Serve(l, mux)

			runCmd := exec.Command(binPath, tt.args...)
			// Run in a temp dir: `init` without --path writes AGENTS.md
			// into the current directory.
			runCmd.Dir = t.TempDir()
			runCmd.Env = append(os.Environ(), "DIBS_SOCKET="+sockPath, "DIBS_DB="+dbPath)
			var stderr bytes.Buffer
			runCmd.Stderr = &stderr

			_ = runCmd.Run() // exit code might be non-zero for some commands if mock is incomplete, that's fine

			out := stderr.String()
			hasWarning := strings.Contains(out, "restart dibsd")

			if tt.wantStderr && !hasWarning {
				t.Errorf("expected warning in stderr, got: %q", out)
			}
			if !tt.wantStderr && hasWarning {
				t.Errorf("expected no warning in stderr, got: %q", out)
			}
		})
	}
}

// TestVersionCommandReportsBuildRevision verifies that dibs can report the
// build revision embedded via Makefile ldflags, so an installed binary can be
// compared against the source checkout (`git rev-parse HEAD`). Both the
// `version` command and the `--version` flag must work without a daemon.
func TestVersionCommandReportsBuildRevision(t *testing.T) {
	tmpDir := t.TempDir()
	binPath := filepath.Join(tmpDir, "dibs")
	const localRevision = "test-revision-xyz"
	cmd := exec.Command("go", "build", "-buildvcs=false",
		"-ldflags", "-X github.com/abevz/dibs/internal/build.Revision="+localRevision,
		"-o", binPath, ".")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("failed to build dibs: %v\noutput: %s", err, out)
	}

	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "version command", args: []string{"version"}, want: "dibs dev (test-re)\n"},
		{name: "version flag", args: []string{"--version"}, want: "dibs dev (test-re)\n"},
		{name: "JSON version command", args: []string{"--json", "version"}, want: "{\"version\":\"dev\",\"revision\":\"test-revision-xyz\"}\n"},
		{name: "JSON version flag", args: []string{"--json", "--version"}, want: "{\"version\":\"dev\",\"revision\":\"test-revision-xyz\"}\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runCmd := exec.Command(binPath, tt.args...)
			runCmd.Dir = t.TempDir()
			// Point at a socket that does not exist: `version` must never
			// require a daemon, so any accidental health probe would fail.
			runCmd.Env = append(os.Environ(), "AF_COORDINATOR_SOCKET="+filepath.Join(tmpDir, "does-not-exist.sock"))
			var stdout, stderr bytes.Buffer
			runCmd.Stdout = &stdout
			runCmd.Stderr = &stderr
			if err := runCmd.Run(); err != nil {
				t.Fatalf("dibs %v failed: %v\nstderr: %s", tt.args, err, stderr.String())
			}
			if stdout.String() != tt.want {
				t.Errorf("stdout = %q, want %q", stdout.String(), tt.want)
			}
		})
	}
}

func TestVersionCommandReportsReleaseTag(t *testing.T) {
	binPath := filepath.Join(t.TempDir(), "dibs")
	cmd := exec.Command("go", "build", "-buildvcs=false", "-ldflags",
		"-X github.com/abevz/dibs/internal/build.Version=v0.1.0-rc.3 -X github.com/abevz/dibs/internal/build.Revision=abcdef0123456789",
		"-o", binPath, ".")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build release binary: %v\n%s", err, out)
	}
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"version"}, "dibs v0.1.0-rc.3 (abcdef0)\n"},
		{[]string{"--json", "version"}, "{\"version\":\"v0.1.0-rc.3\",\"revision\":\"abcdef0123456789\"}\n"},
	} {
		out, err := exec.Command(binPath, tc.args...).CombinedOutput()
		if err != nil || string(out) != tc.want {
			t.Fatalf("dibs %v: output %q, error %v; want %q", tc.args, out, err, tc.want)
		}
	}
}
