package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abevz/dibs/internal/config"
	"github.com/abevz/dibs/internal/integration"
)

func TestInitSnippetPointsToCanonicalProtocolAndHandoff(t *testing.T) {
	block := formatBlock(initSnippet)
	if !strings.Contains(block, "`dibs protocol`") || !strings.Contains(block, "docs/agent-protocol-v1.md") {
		t.Fatalf("init block does not point to the canonical protocol: %q", block)
	}
	if !strings.Contains(block, "handoff/close") {
		t.Fatalf("init block does not describe the handoff/close session path: %q", block)
	}
}

func TestInitSnippetManualTokenLifecycle(t *testing.T) {
	block := formatBlock(initSnippet)
	for _, want := range []string{
		"<!-- BEGIN DIBS INTEGRATION v:3 -->",
		"only after close, handoff, or release exits 0",
		"`cmd && rm`, never `cmd; rm` or an always-on cleanup trap",
		"On failure keep the file, reread the version and lease generation",
		"check that the generation still matches your claim",
		"fix the cause, and retry within the lease window",
		"If the token is lost, stop and report. Never re-claim by holder name.",
		`dibs-op issue operator-release <id> --expected-version latest --reason "<why>"`,
		`dibs-op issue operator-close <id> --resolution <done|cancelled> --expected-version latest --reason "<why>" [--note …]`,
	} {
		if !strings.Contains(block, want) {
			t.Errorf("generated block missing %q", want)
		}
	}
}

func TestInitUpdatesV2InPlace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "AGENTS.md")
	oldBlock := integration.PreviousMarker + "\nold content\n" + endMarker
	block := formatBlock(initSnippet)
	before := "# Repo\n" + oldBlock + "\nFooter\n"
	if err := os.WriteFile(path, []byte(before), 0644); err != nil {
		t.Fatal(err)
	}
	for _, dryRun := range []bool{true, false} {
		action, err := applyBlock(path, block, dryRun, nil)
		if err != nil || action != initUpdated {
			t.Fatalf("dry-run %v: action = %s, err = %v", dryRun, action, err)
		}
		want := before
		if !dryRun {
			want = "# Repo\n" + block + "Footer\n"
		}
		got, err := os.ReadFile(path)
		if err != nil || string(got) != want {
			t.Fatalf("dry-run %v: file = %q, err = %v", dryRun, got, err)
		}
	}
}

func TestApplyBlock(t *testing.T) {
	t.Parallel()

	block := formatBlock("test snippet content")

	tests := []struct {
		name     string
		setup    func(t *testing.T) (path string, cleanup func())
		want     initAction
		wantFile bool
		wantBody string
	}{
		{
			name: "missing file creates",
			setup: func(t *testing.T) (string, func()) {
				dir := t.TempDir()
				return filepath.Join(dir, "AGENTS.md"), func() {}
			},
			want:     initCreated,
			wantFile: true,
			wantBody: block,
		},
		{
			name: "existing file without block appends",
			setup: func(t *testing.T) (string, func()) {
				dir := t.TempDir()
				p := filepath.Join(dir, "AGENTS.md")
				os.WriteFile(p, []byte("# Existing content\n"), 0644)
				return p, func() {}
			},
			want:     initUpdated,
			wantFile: true,
			wantBody: "# Existing content\n\n" + block,
		},
		{
			name: "legacy block migrated",
			setup: func(t *testing.T) (string, func()) {
				dir := t.TempDir()
				p := filepath.Join(dir, "AGENTS.md")
				staleBlock := "<!-- BEGIN AF-COORDINATOR INTEGRATION v:1 -->\nold content\n<!-- END AF-COORDINATOR INTEGRATION -->"
				os.WriteFile(p, []byte("# Repo\n"+staleBlock+"\nFooter"), 0644)
				return p, func() {}
			},
			want:     initMigrated,
			wantFile: true,
			wantBody: "# Repo\n" + block + "Footer",
		},
		{
			name: "current block unchanged",
			setup: func(t *testing.T) (string, func()) {
				dir := t.TempDir()
				p := filepath.Join(dir, "AGENTS.md")
				os.WriteFile(p, []byte("# Repo\n"+block), 0644)
				return p, func() {}
			},
			want:     initUnchanged,
			wantFile: true,
			wantBody: "# Repo\n" + block,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path, cleanup := tt.setup(t)
			defer cleanup()

			got, err := applyBlock(path, block, false, nil)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("applyBlock() = %v, want %v", got, tt.want)
			}

			if tt.wantFile {
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if string(data) != tt.wantBody {
					t.Errorf("file content mismatch\n got: %q\nwant: %q", string(data), tt.wantBody)
				}
			}
		})
	}
}

func TestApplyBlockDryRun(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := filepath.Join(dir, "AGENTS.md")
	block := formatBlock("test")

	got, err := applyBlock(p, block, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != initCreated {
		t.Errorf("dry-run: got %v, want created", got)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Errorf("dry-run should not create file")
	}
}

func TestIntegrationMigrationPreservesSurroundings(t *testing.T) {
	block := formatBlock("new content")
	legacy := legacyBeginMarker + "\nold content\n" + legacyEndMarker
	for _, suffix := range []string{"", "\nFooter\n", "\r\nFooter", "\n\nFooter"} {
		for _, dry := range []bool{false, true} {
			t.Run(fmt.Sprintf("suffix=%q/dry=%v", suffix, dry), func(t *testing.T) {
				p := filepath.Join(t.TempDir(), "AGENTS.md")
				before := "# Repo\r\n\n" + legacy + suffix
				if err := os.WriteFile(p, []byte(before), 0644); err != nil {
					t.Fatal(err)
				}
				action, err := applyBlock(p, block, dry, nil)
				if err != nil || action != initMigrated {
					t.Fatalf("action=%v err=%v", action, err)
				}
				want := "# Repo\r\n\n" + strings.TrimSuffix(block, "\n") + suffix
				if dry {
					want = before
				}
				got, err := os.ReadFile(p)
				if err != nil || string(got) != want {
					t.Fatalf("got=%q want=%q err=%v", got, want, err)
				}
			})
		}
	}
}

func TestIntegrationConflictsDoNotWrite(t *testing.T) {
	block := formatBlock("new")
	legacy := legacyBeginMarker + "\nold\n" + legacyEndMarker + "\n"
	for _, before := range []string{block + legacy, legacy + block, beginMarker, endMarker + beginMarker, block + block, legacy + legacy, integration.PreviousMarker + "\n" + block} {
		for _, dry := range []bool{false, true} {
			p := filepath.Join(t.TempDir(), "AGENTS.md")
			if err := os.WriteFile(p, []byte(before), 0644); err != nil {
				t.Fatal(err)
			}
			_, err := applyBlock(p, block, dry, nil)
			if err == nil {
				t.Fatalf("accepted conflicting content %q", before)
			}
			if strings.Contains(before, beginMarker) && strings.Contains(before, legacyBeginMarker) && (!strings.Contains(err.Error(), "lines 1-3") || !strings.Contains(err.Error(), "lines 4-6")) {
				t.Fatalf("missing block ranges: %v", err)
			}
			got, readErr := os.ReadFile(p)
			if readErr != nil || string(got) != before {
				t.Fatalf("file changed after conflict: %q", got)
			}
		}
	}
}

func TestCurrentIntegrationUpdatedWithoutExtraNewline(t *testing.T) {
	p := filepath.Join(t.TempDir(), "AGENTS.md")
	old := formatBlock("old")
	if err := os.WriteFile(p, []byte("Prefix\n"+old+"Suffix"), 0644); err != nil {
		t.Fatal(err)
	}
	action, err := applyBlock(p, formatBlock("new"), false, nil)
	if err != nil || action != initUpdated {
		t.Fatalf("action=%v err=%v", action, err)
	}
	got, err := os.ReadFile(p)
	if err != nil || string(got) != "Prefix\n"+formatBlock("new")+"Suffix" {
		t.Fatalf("unexpected replacement: %q", got)
	}
}

func TestInitSetupDryRunReportsMigration(t *testing.T) {
	repoDir := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", "-b", "main", repoDir).CombinedOutput(); err != nil {
		t.Fatalf("initialize test repository: %v: %s", err, out)
	}
	t.Chdir(repoDir)
	oldJSON, oldStdout := jsonOutput, os.Stdout
	defer func() { jsonOutput, os.Stdout = oldJSON, oldStdout }()
	before := legacyBeginMarker + "\nold\n" + legacyEndMarker + "\n"
	for _, jsonMode := range []bool{false, true} {
		p := filepath.Join(t.TempDir(), "AGENTS.md")
		if err := os.WriteFile(p, []byte(before), 0644); err != nil {
			t.Fatal(err)
		}
		jsonOutput = jsonMode
		r, w, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		os.Stdout = w
		runErr := runInitSetup(context.Background(), nil, config.Config{}, []string{"--path", p, "--dry-run", "--project", "afc", "--repo", "af-coordinator"})
		w.Close()
		out, err := io.ReadAll(r)
		r.Close()
		os.Stdout = oldStdout
		if runErr != nil || err != nil {
			t.Fatalf("run=%v read=%v", runErr, err)
		}
		if !strings.Contains(string(out), "migrated") {
			t.Fatalf("migration omitted: %s", out)
		}
		if jsonMode {
			var resp map[string]any
			if err := json.Unmarshal(out, &resp); err != nil {
				t.Fatal(err)
			}
			if resp["action"] != "migrated" || resp["dry_run"] != true {
				t.Fatalf("bad response: %s", out)
			}
		} else if !strings.Contains(string(out), "would migrated") {
			t.Fatalf("missing dry-run prefix: %s", out)
		}
		after, err := os.ReadFile(p)
		if err != nil || string(after) != before {
			t.Fatal("dry-run modified file")
		}
	}
}
