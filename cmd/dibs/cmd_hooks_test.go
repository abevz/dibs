package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallSessionStartPreservesConfigAndIsIdempotent(t *testing.T) {
	for _, agent := range []string{"claude", "codex"} {
		t.Run(agent, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "hooks.json")
			original := `{"custom": {"keep": true}, "hooks": {"Stop": [{"hooks": [{"type":"command","command":"echo stay"}]}]}}`
			if err := os.WriteFile(path, []byte(original), 0600); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 2; i++ {
				if err := writeSessionStartHook(path, agent); err != nil {
					t.Fatal(err)
				}
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var doc struct {
				Custom map[string]bool              `json:"custom"`
				Hooks  map[string][]json.RawMessage `json:"hooks"`
			}
			if err := json.Unmarshal(data, &doc); err != nil {
				t.Fatal(err)
			}
			if !doc.Custom["keep"] || len(doc.Hooks["Stop"]) != 1 || len(doc.Hooks["SessionStart"]) != 1 {
				t.Fatalf("config lost or duplicated data: %s", data)
			}
			bin, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(doc.Hooks["SessionStart"][0]), bin) {
				t.Fatalf("hook does not pin this dibs executable: %s", data)
			}
		})
	}
}

func TestInstallSessionStartRejectsSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target.json")
	link := filepath.Join(dir, "link.json")
	if err := os.WriteFile(target, []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := writeSessionStartHook(link, "codex"); err == nil {
		t.Fatal("expected symlink refusal")
	}
}

func TestIssueRunRequiresExplicitCompletion(t *testing.T) {
	bin := buildAfctlForRunTest(t)
	for _, tc := range []struct {
		name  string
		child []string
		close bool
	}{
		{"normal exit hands off", []string{"sh", "-c", "exit 0"}, false},
		{"explicit completion closes", []string{bin, "hooks", "complete"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := &mockCoordinator{claimVersion: 3}
			sock := startMockCoordinator(t, mock)
			args := append([]string{"issue", "run", "afc-2", "--actor", "tester", "--ttl", "60", "--require-complete", "--"}, tc.child...)
			cmd := exec.Command(bin, args...)
			cmd.Env = append(os.Environ(), "AF_COORDINATOR_SOCKET="+sock)
			out, err := cmd.CombinedOutput()
			if tc.close && err != nil {
				t.Fatalf("run failed: %v %s", err, out)
			}
			if !tc.close && err == nil {
				t.Fatalf("run unexpectedly succeeded: %s", out)
			}
			mock.mu.Lock()
			defer mock.mu.Unlock()
			if tc.close {
				if len(mock.closeReqs) != 1 || len(mock.handoffReqs) != 0 {
					t.Fatalf("close=%d handoff=%d", len(mock.closeReqs), len(mock.handoffReqs))
				}
			}
			if !tc.close {
				if len(mock.closeReqs) != 0 || len(mock.handoffReqs) != 1 {
					t.Fatalf("close=%d handoff=%d", len(mock.closeReqs), len(mock.handoffReqs))
				}
				if note, _ := mock.handoffReqs[0]["note"].(string); !strings.HasPrefix(note, "HANDOFF:") {
					t.Fatalf("note=%q", note)
				}
			}
		})
	}
}

func TestHooksCompleteMetadataOverridesRunFlags(t *testing.T) {
	bin := buildAfctlForRunTest(t)
	mock := &mockCoordinator{claimVersion: 3}
	sock := startMockCoordinator(t, mock)
	cmd := exec.Command(bin, "issue", "run", "afc-2", "--actor", "tester", "--ttl", "60", "--require-complete", "--pr-url", "https://github.com/o/r/pull/old", "--branch", "old", "--note", "old", "--", bin, "hooks", "complete", "--pr-url", "https://github.com/o/r/pull/new", "--branch", "new", "--note", "new")
	cmd.Env = append(os.Environ(), "AF_COORDINATOR_SOCKET="+sock)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run failed: %v %s", err, out)
	}
	mock.mu.Lock()
	defer mock.mu.Unlock()
	if len(mock.closeReqs) != 1 {
		t.Fatalf("close requests = %d", len(mock.closeReqs))
	}
	for key, want := range map[string]string{"pr_url": "https://github.com/o/r/pull/new", "branch": "new", "note": "new"} {
		if got := mock.closeReqs[0][key]; got != want {
			t.Fatalf("%s = %v, want %q", key, got, want)
		}
	}
}

func TestCompletionMarkerLegacyAndJSON(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want completionMarker
	}{
		{"done\n", completionMarker{}},
		{`{"pr_url":"https://github.com/o/r/pull/2","branch":"work"}` + "\n", completionMarker{PRURL: "https://github.com/o/r/pull/2", Branch: "work"}},
	} {
		got, err := parseCompletionMarker([]byte(tc.raw))
		if err != nil || got != tc.want {
			t.Fatalf("parse %q = %+v, %v", tc.raw, got, err)
		}
	}
	if _, err := parseCompletionMarker([]byte("no")); err == nil {
		t.Fatal("accepted invalid completion")
	}
}

func TestInstallPreservesWrappedSessionStartCommand(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hooks.json")
	wrapped := "cleanup.sh && '/old/dibs' hooks session-start --agent codex"
	config := `{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"cleanup.sh && '/old/dibs' hooks session-start --agent codex"}]}]}}`
	if err := os.WriteFile(path, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	if err := writeSessionStartHook(path, "codex"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Hooks map[string][]json.RawMessage `json:"hooks"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	var first struct {
		Hooks []struct {
			Command string `json:"command"`
		} `json:"hooks"`
	}
	if len(doc.Hooks["SessionStart"]) > 0 {
		if err := json.Unmarshal(doc.Hooks["SessionStart"][0], &first); err != nil {
			t.Fatal(err)
		}
	}
	if len(doc.Hooks["SessionStart"]) != 2 || len(first.Hooks) != 1 || first.Hooks[0].Command != wrapped {
		t.Fatalf("custom hook changed: %s", data)
	}
}

func TestSessionStartNeedsDaemon(t *testing.T) {
	if !commandNeedsDaemon([]string{"hooks", "session-start"}) {
		t.Fatal("session-start must start the daemon")
	}
	if commandNeedsDaemon([]string{"hooks", "install"}) || commandNeedsDaemon([]string{"hooks", "complete"}) {
		t.Fatal("local hook commands must not start the daemon")
	}
}
