package main

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/abevz/dibs/internal/client"
	"github.com/abevz/dibs/internal/core"
)

func TestParseIssueListArgs(t *testing.T) {
	tests := []struct {
		name        string
		args        []string
		want        core.IssueListParams
		wantColumns []string
		wantHelp    bool
		wantErr     string
	}{
		{
			name: "csv and repeated filters",
			args: []string{"--project", "afc,aion", "--type", "epic,chore", "--status", "open", "--status", "in_progress"},
			want: core.IssueListParams{
				Projects:   []string{"afc", "aion"},
				IssueTypes: []string{"epic", "chore"},
				Statuses:   []string{"open", "in_progress"},
			},
		},
		{
			name: "repeated tag filter",
			args: []string{"--tag", "area/frontend", "--tag", "theme/dark"},
			want: core.IssueListParams{
				Tags: []string{"area/frontend", "theme/dark"},
			},
		},
		{
			name:        "columns",
			args:        []string{"--columns", "short,status,title"},
			want:        core.IssueListParams{},
			wantColumns: []string{"short", "status", "title"},
		},
		{name: "help", args: []string{"--help"}, wantHelp: true},
		{name: "unknown flag", args: []string{"--wat"}, wantErr: "unknown flag"},
		{name: "missing value", args: []string{"--project"}, wantErr: "requires a value"},
		{name: "empty csv element", args: []string{"--type", "epic,"}, wantErr: "empty elements"},
		{name: "unknown column", args: []string{"--columns", "nope"}, wantErr: "unknown column"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, columns, help, err := parseIssueListArgs(tt.args)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if help != tt.wantHelp {
				t.Fatalf("help = %v, want %v", help, tt.wantHelp)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("params = %#v, want %#v", got, tt.want)
			}
			if !reflect.DeepEqual(columns, tt.wantColumns) {
				t.Fatalf("columns = %#v, want %#v", columns, tt.wantColumns)
			}
		})
	}
}

func TestIssueListHelpDoesNotRequireClient(t *testing.T) {
	if err := runIssueList(context.Background(), nil, []string{"--help"}); err != nil {
		t.Fatalf("issue list help: %v", err)
	}
	if err := runLs(context.Background(), nil, []string{"--help"}); err != nil {
		t.Fatalf("ls help: %v", err)
	}
}

func TestShouldCheckDaemonRevision(t *testing.T) {
	tests := []struct {
		args []string
		want bool
	}{
		{args: []string{"ls", "--help"}, want: false},
		{args: []string{"issue", "list", "--help"}, want: false},
		{args: []string{"init"}, want: false},
		{args: []string{"protocol"}, want: false},
		{args: []string{"ls", "--project", "afc"}, want: true},
	}
	for _, tt := range tests {
		if got := shouldCheckDaemonRevision(tt.args); got != tt.want {
			t.Errorf("shouldCheckDaemonRevision(%q) = %v, want %v", tt.args, got, tt.want)
		}
	}
}

func TestRunIssueUnlinkRequiresFlagValues(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{
			name:    "path missing value",
			args:    []string{"afc-1", "--path"},
			wantErr: "--path requires a value",
		},
		{
			name:    "artifact missing value",
			args:    []string{"afc-1", "--artifact"},
			wantErr: "--artifact requires a value",
		},
		{
			name:    "relation missing value",
			args:    []string{"afc-1", "--artifact", "docs/spec.md", "--relation"},
			wantErr: "--relation requires a value",
		},
		{
			name:    "artifact value is another flag",
			args:    []string{"afc-1", "--artifact", "--relation", "implements"},
			wantErr: "--artifact requires a value",
		},
		{
			name:    "relation value is another flag",
			args:    []string{"afc-1", "--artifact", "docs/spec.md", "--relation", "--path"},
			wantErr: "--relation requires a value",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := runIssueUnlink(context.Background(), nil, tt.args)
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %q, want containing %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestOperatorCommandsRejectLeaseTokenFlag(t *testing.T) {
	t.Parallel()

	err := runIssue(context.Background(), nil, []string{
		"operator-close", "afc-50", "--resolution", "done", "--expected-version", "1",
		"--reason", "completed parent", "--lease-token", "fake",
	})
	if err == nil || !strings.Contains(err.Error(), "unknown flag") {
		t.Fatalf("operator-close error = %v, want unknown flag", err)
	}

	err = runIssue(context.Background(), nil, []string{
		"operator-reopen", "afc-50", "--expected-version", "2", "--reason", "needs work",
		"--lease-token", "fake",
	})
	if err == nil || !strings.Contains(err.Error(), "unknown flag") {
		t.Fatalf("operator-reopen error = %v, want unknown flag", err)
	}

	err = runIssue(context.Background(), nil, []string{
		"operator-release", "afc-50", "--expected-version", "3", "--reason", "lease token lost",
		"--lease-token", "fake",
	})
	if err == nil || !strings.Contains(err.Error(), "unknown flag") {
		t.Fatalf("operator-release error = %v, want unknown flag", err)
	}

	err = runIssue(context.Background(), nil, []string{
		"cancel", "afc-50", "--lease-token", "fake",
	})
	if err == nil || !strings.Contains(err.Error(), "unknown flag") {
		t.Fatalf("cancel error = %v, want unknown flag", err)
	}
}

// TestOperatorCloseAutoResolvePath verifies that when --expected-version is
// provided, the auto-resolve branch (ExpectedVersion == -1) is skipped, and
// that when it is omitted the command enters the auto-resolve path. Because
// c.GetIssue panics on a nil client, the auto-resolve-entered case cannot
// proceed past the GetIssue call; we validate the flag-parsing and
// sentinel-initialization contract instead.
func TestOperatorCloseAutoResolvePath(t *testing.T) {
	t.Parallel()

	// With --expected-version provided, the auto-resolve check
	// (ExpectedVersion == -1) is false, so c.GetIssue is never called.
	// The command proceeds to --reason validation.
	err := runIssue(context.Background(), nil, []string{
		"operator-close", "afc-1", "--resolution", "done", "--expected-version", "1",
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "--reason is required") {
		t.Fatalf("with --expected-version: error = %q, want containing '--reason is required'", err.Error())
	}

	// With --expected-version omitted, ExpectedVersion stays at the
	// initialised -1, so the auto-resolve branch is entered. On a nil
	// client c.GetIssue panics; we verify the command does NOT error with
	// "--expected-version is required" (proving the sentinel triggered the
	// auto-resolve path before the <= 0 guard).
	//
	// Because the nil-client panic prevents a clean error return, we use a
	// different signal: providing --resolution and --reason (but not
	// --expected-version) should reach auto-resolve, NOT hit the version
	// guard, and then panic on c.GetIssue. To distinguish this from the
	// version-guard error, we verify that the error (caught via recover)
	// mentions GetIssue / nil, not "expected-version is required".
	func() {
		defer func() {
			r := recover()
			if r == nil {
				// No panic means auto-resolve was NOT entered.
				// This would mean the sentinel is broken.
				t.Error("expected panic from auto-resolve GetIssue call with nil client; sentinel may not be -1")
			}
		}()
		_ = runIssue(context.Background(), nil, []string{
			"operator-close", "afc-1", "--resolution", "done", "--reason", "merged",
		})
	}()
}

func TestIssueHandoffValidatesRequiredHandoffNote(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{
			name:    "missing token",
			args:    []string{"handoff", "afc-52", "--note", "HANDOFF: next steps"},
			wantErr: "--lease-token is required",
		},
		{
			name:    "missing note",
			args:    []string{"handoff", "afc-52", "--lease-token", "token", "--lease-generation", "1"},
			wantErr: "note is required",
		},
		{
			name:    "malformed note",
			args:    []string{"handoff", "afc-52", "--lease-token", "token", "--lease-generation", "1", "--note", "continue later"},
			wantErr: "note must begin with HANDOFF:",
		},
		{
			name:    "unknown flag",
			args:    []string{"handoff", "afc-52", "--lease-token", "token", "--note", "HANDOFF: next steps", "--author", "agent"},
			wantErr: "unknown flag",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := runIssue(context.Background(), nil, tt.args)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %v, want %q", err, tt.wantErr)
			}
		})
	}
}

// TestIssueLifecycleCommandsShowFullUsageOnError verifies that claim/close-
// family commands print the complete Usage: line plus a pointer to `dibs
// protocol` on any validation failure, instead of only the single missing
// flag, and that -h/--help short-circuits to the same usage without
// touching the (nil) client.
func TestIssueLifecycleCommandsShowFullUsageOnError(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "claim missing id", args: []string{"claim"}},
		{name: "heartbeat missing lease token", args: []string{"heartbeat", "afc-1"}},
		{name: "release missing lease token", args: []string{"release", "afc-1"}},
		{name: "handoff missing lease token", args: []string{"handoff", "afc-1"}},
		{name: "close missing everything", args: []string{"close", "afc-1"}},
		{name: "close missing expected-version", args: []string{"close", "afc-1", "--resolution", "done", "--lease-token", "t"}},
		{name: "close missing lease-token", args: []string{"close", "afc-1", "--resolution", "done", "--expected-version", "2"}},
		{name: "operator-close missing everything", args: []string{"operator-close", "afc-1"}},
		{name: "operator-reopen missing everything", args: []string{"operator-reopen", "afc-1"}},
		{name: "operator-release missing everything", args: []string{"operator-release", "afc-1"}},
		{name: "cancel missing id", args: []string{"cancel"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := runIssue(context.Background(), nil, tt.args)
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), "Usage: dibs issue "+tt.args[0]) {
				t.Errorf("error = %q, want it to contain the full Usage: line for %q", err.Error(), tt.args[0])
			}
			if !strings.Contains(err.Error(), "run: dibs protocol") {
				t.Errorf("error = %q, want it to point at `dibs protocol`", err.Error())
			}
		})
	}
}

func TestIssueLifecycleCommandsHelpFlagShortCircuits(t *testing.T) {
	subcommands := []string{"claim", "heartbeat", "release", "handoff", "close", "operator-close", "operator-reopen", "operator-release", "cancel"}
	for _, sub := range subcommands {
		t.Run(sub, func(t *testing.T) {
			// A nil client would panic if the command tried to reach the
			// daemon; a nil-error return proves -h short-circuited first.
			if err := runIssue(context.Background(), nil, []string{sub, "-h"}); err != nil {
				t.Errorf("runIssue(%q, -h) = %v, want nil", sub, err)
			}
			if err := runIssue(context.Background(), nil, []string{sub, "--help"}); err != nil {
				t.Errorf("runIssue(%q, --help) = %v, want nil", sub, err)
			}
		})
	}
}

// TestNonLifecycleIssueCommandsShowFullUsageOnError extends the afc-76
// full-usage-on-error treatment (originally only claim/close-family
// commands) to the remaining issue subcommands. These don't carry
// lifecycleHint -- it's specific to the claim/close lease lifecycle -- so
// only the full Usage: line is asserted, not the protocol pointer.
func TestIssueCancelAutoResolvePath(t *testing.T) {
	t.Parallel()

	// Cancel always auto-resolves (no --expected-version flag).
	// With a nil client, the GetIssue call panics.
	func() {
		defer func() {
			r := recover()
			if r == nil {
				t.Error("expected panic from auto-resolve GetIssue call with nil client")
			}
		}()
		_ = runIssue(context.Background(), nil, []string{
			"cancel", "afc-1", "--note", "no longer needed",
		})
	}()
}

func TestNonLifecycleIssueCommandsShowFullUsageOnError(t *testing.T) {
	tests := []struct {
		name      string
		args      []string
		wantUsage string
	}{
		{name: "create missing everything", args: []string{"create"}, wantUsage: "Usage: dibs issue create"},
		{name: "get missing id", args: []string{"get"}, wantUsage: "Usage: dibs issue get"},
		{name: "link missing everything", args: []string{"link", "afc-1"}, wantUsage: "Usage: dibs issue link"},
		{name: "unlink missing everything", args: []string{"unlink", "afc-1"}, wantUsage: "Usage: dibs issue unlink"},
		{name: "dependency missing subcommand", args: []string{"dependency"}, wantUsage: "Usage: dibs issue dependency "},
		{name: "dependency add missing everything", args: []string{"dependency", "add", "afc-1"}, wantUsage: "Usage: dibs issue dependency add"},
		{name: "dependency remove missing everything", args: []string{"dependency", "remove", "afc-1"}, wantUsage: "Usage: dibs issue dependency remove"},
		{name: "note missing subcommand", args: []string{"note"}, wantUsage: "Usage: dibs issue note "},
		{name: "note add missing body", args: []string{"note", "add", "afc-1"}, wantUsage: "Usage: dibs issue note add"},
		{name: "note list missing id", args: []string{"note", "list"}, wantUsage: "Usage: dibs issue note list"},
		{name: "events missing subcommand", args: []string{"events"}, wantUsage: "Usage: dibs issue events"},
		{name: "events list missing id", args: []string{"events", "list"}, wantUsage: "Usage: dibs issue events list"},
		{name: "tag missing subcommand", args: []string{"tag"}, wantUsage: "Usage: dibs issue tag "},
		{name: "tag add missing tag flag", args: []string{"tag", "add", "afc-1"}, wantUsage: "Usage: dibs issue tag add"},
		{name: "tag remove missing tag flag", args: []string{"tag", "remove", "afc-1"}, wantUsage: "Usage: dibs issue tag remove"},
		{name: "tag list missing id", args: []string{"tag", "list"}, wantUsage: "Usage: dibs issue tag list"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := runIssue(context.Background(), nil, tt.args)
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.wantUsage) {
				t.Errorf("error = %q, want it to contain %q", err.Error(), tt.wantUsage)
			}
		})
	}
}

func TestNonLifecycleIssueCommandsHelpFlagShortCircuits(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "create", args: []string{"create", "-h"}},
		{name: "get", args: []string{"get", "-h"}},
		{name: "ready", args: []string{"ready", "-h"}},
		{name: "link", args: []string{"link", "-h"}},
		{name: "unlink", args: []string{"unlink", "-h"}},
		{name: "dependency", args: []string{"dependency", "-h"}},
		{name: "dependency add", args: []string{"dependency", "add", "-h"}},
		{name: "dependency remove", args: []string{"dependency", "remove", "-h"}},
		{name: "note", args: []string{"note", "-h"}},
		{name: "note add", args: []string{"note", "add", "-h"}},
		{name: "note list", args: []string{"note", "list", "-h"}},
		{name: "events", args: []string{"events", "-h"}},
		{name: "events list", args: []string{"events", "list", "-h"}},
		{name: "tag", args: []string{"tag", "-h"}},
		{name: "tag add", args: []string{"tag", "add", "-h"}},
		{name: "tag remove", args: []string{"tag", "remove", "-h"}},
		{name: "tag list", args: []string{"tag", "list", "-h"}},
		{name: "create-form", args: []string{"create-form", "-h"}},
		{name: "create with allow-duplicate", args: []string{"create", "-h", "--allow-duplicate"}},
		{name: "create-form with allow-duplicate", args: []string{"create-form", "-h", "--allow-duplicate"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// A nil client would panic if the command tried to reach the
			// daemon; a nil-error return proves -h short-circuited first.
			if err := runIssue(context.Background(), nil, tt.args); err != nil {
				t.Errorf("runIssue(%v) = %v, want nil", tt.args, err)
			}
		})
	}
}

func TestRunIssueReadyEncodesRepeatedTagQuery(t *testing.T) {
	socketPath := filepath.Join(testSocketDir(t), "ready.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	var gotTags []string
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/issues/ready" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		gotTags = r.URL.Query()["tag"]
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"issues":[]}`))
	}))
	server.Listener.Close()
	server.Listener = listener
	server.Start()
	defer server.Close()

	if err := runIssueReady(context.Background(), client.New(socketPath), []string{
		"--project", "afc", "--tag", "exec/auto", "--tag", "area/frontend",
	}); err != nil {
		t.Fatal(err)
	}
	if want := []string{"exec/auto", "area/frontend"}; !reflect.DeepEqual(gotTags, want) {
		t.Fatalf("tag query = %q, want %q", gotTags, want)
	}
}

func TestRunIssueReadySplitsCommaSeparatedTagQuery(t *testing.T) {
	socketPath := filepath.Join(testSocketDir(t), "ready-comma.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	var gotTags []string
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/issues/ready" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		gotTags = r.URL.Query()["tag"]
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"issues":[]}`))
	}))
	server.Listener.Close()
	server.Listener = listener
	server.Start()
	defer server.Close()

	if err := runIssueReady(context.Background(), client.New(socketPath), []string{
		"--project", "afc", "--tag", "exec/auto,area/frontend",
	}); err != nil {
		t.Fatal(err)
	}
	if want := []string{"exec/auto", "area/frontend"}; !reflect.DeepEqual(gotTags, want) {
		t.Fatalf("tag query = %q, want %q", gotTags, want)
	}
}

func TestRunIssueReadyRejectsUnknownColumn(t *testing.T) {
	err := runIssueReady(context.Background(), client.New("/nonexistent.sock"), []string{
		"--columns", "nope",
	})
	if err == nil || !strings.Contains(err.Error(), "unknown column") {
		t.Fatalf("error = %v, want unknown column", err)
	}
}

// TestRunIssueUnknownSubcommandShowsNamespaceUsage verifies that an unknown
// issue subcommand prints the full issue namespace usage (naming every valid
// alternative, including dependency/note/tag/events/link/unlink) instead of a
// bare error line, so an agent can find the working path.
func TestRunIssueUnknownSubcommandShowsNamespaceUsage(t *testing.T) {
	err := runIssue(context.Background(), nil, []string{"dependancy"})
	if err == nil {
		t.Fatal("expected error")
	}
	for _, want := range []string{
		"unknown issue subcommand: dependancy",
		"Usage: dibs issue ",
		"dependency",
		"note",
		"tag",
		"events",
		"link",
		"unlink",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to contain %q", err.Error(), want)
		}
	}
}

// TestIssueExpectedVersionAutoResolve verifies the CLI-side auto-resolution
// contract for update and the operator-* family: --expected-version may be
// omitted, or spelled as "latest" or as --force, and the CLI then fetches the
// issue's current version via GetIssue and sends that concrete version to the
// API. An explicit numeric --expected-version is still forwarded verbatim and
// must not trigger the extra GET.
func TestIssueExpectedVersionAutoResolve(t *testing.T) {
	tests := []struct {
		name        string
		args        []string // args after "issue"
		method      string
		path        string // mutation endpoint
		wantGet     bool   // true when auto-resolve should issue a GET
		explicitVer int    // expected_version forwarded when wantGet is false
	}{
		{
			name:    "update --force",
			args:    []string{"update", "afc-1", "--force"},
			method:  http.MethodPatch,
			path:    "/v1/issues/afc-1",
			wantGet: true,
		},
		{
			name:    "update --expected-version latest",
			args:    []string{"update", "afc-1", "--expected-version", "latest"},
			method:  http.MethodPatch,
			path:    "/v1/issues/afc-1",
			wantGet: true,
		},
		{
			name:    "update omitted version auto-resolves",
			args:    []string{"update", "afc-1", "--title", "renamed"},
			method:  http.MethodPatch,
			path:    "/v1/issues/afc-1",
			wantGet: true,
		},
		{
			name:        "update explicit version is forwarded verbatim",
			args:        []string{"update", "afc-1", "--expected-version", "4", "--title", "renamed"},
			method:      http.MethodPatch,
			path:        "/v1/issues/afc-1",
			explicitVer: 4,
		},
		{
			name:    "operator-close --force",
			args:    []string{"operator-close", "afc-1", "--resolution", "done", "--force", "--reason", "parent completed"},
			method:  http.MethodPost,
			path:    "/v1/issues/afc-1/operator-close",
			wantGet: true,
		},
		{
			name:    "operator-close --expected-version latest",
			args:    []string{"operator-close", "afc-1", "--resolution", "done", "--expected-version", "latest", "--reason", "parent completed"},
			method:  http.MethodPost,
			path:    "/v1/issues/afc-1/operator-close",
			wantGet: true,
		},
		{
			name:    "operator-close omitted version auto-resolves",
			args:    []string{"operator-close", "afc-1", "--resolution", "done", "--reason", "parent completed"},
			method:  http.MethodPost,
			path:    "/v1/issues/afc-1/operator-close",
			wantGet: true,
		},
		{
			name:        "operator-close explicit version is forwarded verbatim",
			args:        []string{"operator-close", "afc-1", "--resolution", "done", "--expected-version", "4", "--reason", "parent completed"},
			method:      http.MethodPost,
			path:        "/v1/issues/afc-1/operator-close",
			explicitVer: 4,
		},
		{
			name:    "operator-reopen --force",
			args:    []string{"operator-reopen", "afc-1", "--force", "--reason", "needs more work"},
			method:  http.MethodPost,
			path:    "/v1/issues/afc-1/operator-reopen",
			wantGet: true,
		},
		{
			name:    "operator-reopen --expected-version latest",
			args:    []string{"operator-reopen", "afc-1", "--expected-version", "latest", "--reason", "needs more work"},
			method:  http.MethodPost,
			path:    "/v1/issues/afc-1/operator-reopen",
			wantGet: true,
		},
		{
			name:    "operator-reopen omitted version auto-resolves",
			args:    []string{"operator-reopen", "afc-1", "--reason", "needs more work"},
			method:  http.MethodPost,
			path:    "/v1/issues/afc-1/operator-reopen",
			wantGet: true,
		},
		{
			name:        "operator-reopen explicit version is forwarded verbatim",
			args:        []string{"operator-reopen", "afc-1", "--expected-version", "4", "--reason", "needs more work"},
			method:      http.MethodPost,
			path:        "/v1/issues/afc-1/operator-reopen",
			explicitVer: 4,
		},
		{
			name:    "operator-release --force",
			args:    []string{"operator-release", "afc-1", "--force", "--reason", "lease token lost"},
			method:  http.MethodPost,
			path:    "/v1/issues/afc-1/operator-release",
			wantGet: true,
		},
		{
			name:    "operator-release --expected-version latest",
			args:    []string{"operator-release", "afc-1", "--expected-version", "latest", "--reason", "lease token lost"},
			method:  http.MethodPost,
			path:    "/v1/issues/afc-1/operator-release",
			wantGet: true,
		},
		{
			name:    "operator-release omitted version auto-resolves",
			args:    []string{"operator-release", "afc-1", "--reason", "lease token lost"},
			method:  http.MethodPost,
			path:    "/v1/issues/afc-1/operator-release",
			wantGet: true,
		},
		{
			name:        "operator-release explicit version is forwarded verbatim",
			args:        []string{"operator-release", "afc-1", "--expected-version", "4", "--reason", "lease token lost"},
			method:      http.MethodPost,
			path:        "/v1/issues/afc-1/operator-release",
			explicitVer: 4,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("AF_OPERATOR_TOKEN", "test-operator-token")
			oldActor := defaultActor
			defaultActor = "test-actor"
			defer func() { defaultActor = oldActor }()

			sockPath := filepath.Join(testSocketDir(t), "expected-version.sock")
			listener, err := net.Listen("unix", sockPath)
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()

			var (
				mu          sync.Mutex
				gotGet      bool
				gotExpected int
			)
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet {
					if r.URL.Path != "/v1/issues/afc-1" {
						t.Errorf("unexpected GET path: %s", r.URL.Path)
					}
					mu.Lock()
					gotGet = true
					mu.Unlock()
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`{"issue":{"id":"afc-1","status":"open","version":7}}`))
					return
				}
				if r.Method != tt.method || r.URL.Path != tt.path {
					t.Errorf("unexpected mutation request: %s %s", r.Method, r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
					return
				}
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Errorf("decode mutation body: %v", err)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				if v, ok := body["expected_version"].(float64); ok {
					mu.Lock()
					gotExpected = int(v)
					mu.Unlock()
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"issue":{"id":"afc-1","status":"open","version":8}}`))
			}))
			server.Listener.Close()
			server.Listener = listener
			server.Start()
			defer server.Close()

			if err := runIssue(context.Background(), client.New(sockPath), tt.args); err != nil {
				t.Fatalf("runIssue(%v): %v", tt.args, err)
			}

			mu.Lock()
			defer mu.Unlock()
			if tt.wantGet && !gotGet {
				t.Fatal("auto-resolve should fetch the issue version via GET /v1/issues/afc-1")
			}
			if !tt.wantGet && gotGet {
				t.Fatal("explicit --expected-version must not trigger the auto-resolve GET")
			}
			want := 7
			if !tt.wantGet {
				want = tt.explicitVer
			}
			if gotExpected != want {
				t.Fatalf("mutation expected_version = %d, want %d", gotExpected, want)
			}
		})
	}
}

// TestAutoResolveVersionStillSurfacesConcurrentConflict verifies that the
// CLI-side auto-resolution does not weaken optimistic concurrency: after the
// CLI has fetched the current version via GetIssue, a concurrent edit by
// another actor bumps the version, so a mutation carrying the now-stale
// resolved version must still surface the API's version_conflict error (and
// the exit-code mapping used by fail()).
func TestAutoResolveVersionStillSurfacesConcurrentConflict(t *testing.T) {
	sockPath := filepath.Join(testSocketDir(t), "version-conflict.sock")
	listener, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/issues/afc-1" && r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"issue":{"id":"afc-1","status":"open","version":5}}`))
			return
		}
		// A second actor bumped the issue between the CLI's fetch and this
		// mutation, so the resolved version is stale.
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"error":{"code":"version_conflict","message":"issue changed concurrently"}}`))
	}))
	server.Listener.Close()
	server.Listener = listener
	server.Start()
	defer server.Close()

	c := client.New(sockPath)
	version := -1
	if err := resolveExpectedVersion(context.Background(), c, "usage", "afc-1", &version); err != nil {
		t.Fatalf("resolveExpectedVersion: %v", err)
	}
	if version != 5 {
		t.Fatalf("resolved version = %d, want 5", version)
	}

	_, err = c.UpdateIssue(context.Background(), "afc-1", core.UpdateIssueRequest{ExpectedVersion: version})
	var clientErr *client.ClientError
	if !errors.As(err, &clientErr) {
		t.Fatalf("UpdateIssue error = %v, want *client.ClientError", err)
	}
	if clientErr.Code != core.ErrConflict {
		t.Fatalf("error code = %q, want %q", clientErr.Code, core.ErrConflict)
	}
	if got := mapExitCodeErr(err); got != 2 {
		t.Fatalf("mapExitCodeErr = %d, want 2 (version_conflict)", got)
	}
}

// TestIssueCloseAndHandoffRequireLeaseGeneration is the afc-118 regression.
//
// afc-105 made the daemon fence close, handoff and update on the lease
// generation but did not give the CLI a way to send it, so the documented
// ready -> claim -> heartbeat -> close cycle could not complete: a real close
// returned "lease_expired: lease_generation does not match the active lease".
// Nothing caught it because the store-level tests call CloseIssue directly
// with the field already filled in, and the CLI tests only exercised argument
// validation. These assert the flag is demanded before any request is sent.
func TestIssueCloseAndHandoffRequireLeaseGeneration(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{
			name: "close without generation",
			args: []string{"close", "afc-52", "--resolution", "done", "--expected-version", "2",
				"--lease-token", "token", "--note", "done"},
			wantErr: "--lease-generation is required",
		},
		{
			name:    "handoff without generation",
			args:    []string{"handoff", "afc-52", "--lease-token", "token", "--note", "HANDOFF: next steps"},
			wantErr: "--lease-generation is required",
		},
		{
			name: "update with a token but no generation",
			args: []string{"update", "afc-52", "--status", "blocked", "--expected-version", "2",
				"--lease-token", "token"},
			wantErr: "--lease-generation is required with --lease-token",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := runIssue(context.Background(), nil, tt.args)
			if err == nil {
				t.Fatalf("expected a usage error naming --lease-generation, got nil")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %q, want it to contain %q", err.Error(), tt.wantErr)
			}
		})
	}
}
