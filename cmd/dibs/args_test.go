package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCommandArgumentsFailClosed(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"health positional", []string{"health", "extra"}},
		{"doctor flag", []string{"doctor", "--typo"}},
		{"protocol positional", []string{"protocol", "extra"}},
		{"init missing value", []string{"init", "--path"}},
		{"project unknown", []string{"project", "add", "--key", "p", "--name", "P", "--typo"}},
		{"project required flag", []string{"project", "add", "--key", "p"}},
		{"repo unknown", []string{"repo", "list", "--typo"}},
		{"worktree positional", []string{"worktree", "prune", "extra"}},
		{"artifact root missing", []string{"artifact-root", "list", "--repo"}},
		{"artifact flag", []string{"artifact", "list", "--typo"}},
		{"export flag", []string{"export", "jsonl", "--typo"}},
		{"stats positional", []string{"stats", "extra"}},
		{"issue create malformed integer", []string{"issue", "create", "--priority", "2oops"}},
		{"issue create conflicting retry flags", []string{"issue", "create", "--project", "p", "--scope-kind", "project", "--title", "T", "--retry-last", "--operation-id", "create-op-0001"}},
		{"issue create weak operation ID", []string{"issue", "create", "--project", "p", "--scope-kind", "project", "--title", "T", "--operation-id", "short"}},
		{"issue create required flag", []string{"issue", "create", "--project", "p"}},
		{"issue claim malformed ttl", []string{"issue", "claim", "afc-1", "--ttl", "3oops"}},
		{"issue claim misplaced ID", []string{"issue", "claim", "--ttl", "900", "afc-1"}},
		{"issue claim empty ID", []string{"issue", "claim", ""}},
		{"issue claim conflicting retry flags", []string{"issue", "claim", "afc-1", "--retry-last", "--operation-id", "x"}},
		{"issue heartbeat malformed generation", []string{"issue", "heartbeat", "afc-1", "--lease-generation", "1oops"}},
		{"issue release missing value", []string{"issue", "release", "afc-1", "--lease-token"}},
		{"issue close invalid resolution", []string{"issue", "close", "afc-1", "--resolution", "finished"}},
		{"issue close latest unsupported", []string{"issue", "close", "afc-1", "--expected-version", "latest"}},
		{"issue update incomplete lease", []string{"issue", "update", "afc-1", "--lease-token", "t"}},
		{"issue link empty path", []string{"issue", "link", "afc-1", "--path", ""}},
		{"issue list invalid type", []string{"issue", "list", "--type", "nonsense"}},
		{"issue list invalid column", []string{"issue", "list", "--columns", "magic"}},
		{"issue ready unknown", []string{"issue", "ready", "--typo"}},
		{"issue note positional", []string{"issue", "note", "list", "afc-1", "extra"}},
		{"issue tag unknown", []string{"issue", "tag", "add", "afc-1", "--typo"}},
		{"issue events unknown", []string{"issue", "events", "list", "afc-1", "--typo"}},
		{"dependency invalid kind", []string{"dependency", "add", "afc-1", "--kind", "magic"}},
		{"dependency remove invalid kind", []string{"dependency", "remove", "afc-1", "--kind", "parent"}},
		{"dependency conflicting flags", []string{"dependency", "add", "afc-1", "--blocked-by", "afc-2", "--kind", "blocks"}},
		{"dependency empty target", []string{"dependency", "add", "afc-1", "--blocked-by", ""}},
		{"ls malformed limit", []string{"ls", "--limit", "oops"}},
		{"show positional", []string{"show", "afc-1", "extra"}},
		{"version positional", []string{"version", "extra"}},
		{"run missing separator", []string{"issue", "run", "afc-1", "echo"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := validateCommandArgs(tt.args); err == nil {
				t.Fatalf("accepted %q", tt.args)
			}
		})
	}
}

func TestDocumentedCommandArgumentsRemainAccepted(t *testing.T) {
	tests := [][]string{
		{"health"}, {"doctor"}, {"protocol"}, {"version"},
		{"init", "--dry-run", "--path", "AGENTS.md"},
		{"project", "add", "--key", "p", "--name", "P"},
		{"repo", "add", "--project", "p", "--logical-name", "r", "--canonical-git-dir", "/tmp/r"},
		{"worktree", "register", "--repo", "r", "--absolute-path", "/tmp/r", "--main"},
		{"artifact-root", "add", "--repo", "r", "--root-path", "docs", "--primary"},
		{"artifact", "register", "--repo", "r", "--relative-path", "docs/x", "--kind", "spec"},
		{"export", "jsonl"}, {"stats", "--since", "24h"},
		{"issue", "create", "--project", "p", "--scope-kind", "project", "--title", "T", "--priority", "3"},
		{"issue", "create", "--project", "p", "--scope-kind", "project", "--title", "T", "--operation-id", "create-op-0001"},
		{"issue", "create", "--project", "p", "--scope-kind", "project", "--title", "T", "--retry-last"},
		{"issue", "list", "--type", "task,bug", "--status", "open,deferred", "--limit", "10"},
		{"issue", "ready", "--project", "p"},
		{"issue", "claim", "afc-1", "--ttl", "900", "--invocation-mode", "interactive"},
		{"issue", "heartbeat", "afc-1", "--lease-token", "t", "--lease-generation", "1"},
		{"issue", "heartbeat", "afc-1", "--lease-token", "t", "--lease-generation", "1", "--operation-id", "heartbeat-op-0001"},
		{"issue", "release", "afc-1", "--lease-token", "t", "--lease-generation", "1"},
		{"issue", "release", "afc-1", "--lease-token", "t", "--lease-generation", "1", "--operation-id", "release-op-0001"},
		{"issue", "handoff", "afc-1", "--lease-token", "t", "--lease-generation", "1", "--note", "HANDOFF: review", "--operation-id", "handoff-op-0001"},
		{"issue", "update", "afc-1", "--title", "New", "--expected-version", "2", "--operation-id", "update-op-0001"},
		{"issue", "close", "afc-1", "--resolution", "done", "--expected-version", "2", "--lease-token", "t", "--lease-generation", "1"},
		{"issue", "close", "afc-1", "--resolution", "done", "--expected-version", "2", "--lease-token", "t", "--lease-generation", "1", "--operation-id", "close-op-0001"},
		{"issue", "run", "afc-1", "--ttl", "900", "--", "echo", "--json", "--unknown"},
		{"issue", "dependency", "add", "afc-1", "--depends-on", "afc-2", "--kind", "blocks"},
		{"issue", "note", "add", "afc-1", "--body", "hello"},
		{"issue", "tag", "list", "afc-1"}, {"issue", "events", "list", "afc-1"},
		{"ls", "--project", "p"}, {"show", "afc-1", "--full"},
	}
	for _, args := range tests {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			if err := validateCommandArgs(args); err != nil {
				t.Fatalf("rejected documented invocation: %v", err)
			}
		})
	}
}

func TestMalformedJSONCommandNeverContactsDaemon(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "dibs")
	build := exec.Command("go", "build", "-o", bin, ".")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, output)
	}
	// The nonexistent socket makes a daemon request observable as an internal
	// transport error; argument validation must win before the revision probe.
	for _, args := range [][]string{
		{"--json", "issue", "claim", "afc-1", "--ttl", "90bad"},
		{"issue", "close", "afc-1", "--expected-version", "bad", "--json"},
		{"--json", "artifact", "list", "--unknown"},
		{"--json", "project", "add", "--key", "p"},
		{"--json", "issue", "claim", "--ttl", "900", "afc-1"},
		{"--json", "issue", "claim", "afc-1", "--retry-last", "--operation-id", "x"},
		{"--json", "issue", "claim", ""},
		{"--json", "issue", "link", "afc-1", "--path", ""},
		{"--actor", "", "--json", "issue", "claim", "afc-1"},
	} {
		cmd := exec.Command(bin, args...)
		home := t.TempDir()
		cmd.Env = append(os.Environ(), "HOME="+home, "DIBS_SOCKET="+filepath.Join(home, "absent.sock"))
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		if err := cmd.Run(); err == nil {
			t.Fatalf("%q succeeded", args)
		}
		if stdout.Len() != 0 {
			t.Fatalf("%q stdout: %q", args, stdout.String())
		}
		var response struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		if err := json.Unmarshal(stderr.Bytes(), &response); err != nil {
			t.Fatalf("%q stderr %q: %v", args, stderr.String(), err)
		}
		if response.Error.Code != "validation_failed" {
			t.Fatalf("%q code = %q", args, response.Error.Code)
		}
		for _, dir := range []string{"dibs", "af-coordinator"} {
			journaled, err := filepath.Glob(filepath.Join(home, ".local", "state", dir, "operations", "*.op"))
			if err != nil {
				t.Fatal(err)
			}
			if len(journaled) != 0 {
				t.Fatalf("%q wrote a claim journal: %v", args, journaled)
			}
		}
	}
}
