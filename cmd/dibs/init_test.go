package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
			name: "stale block replaced",
			setup: func(t *testing.T) (string, func()) {
				dir := t.TempDir()
				p := filepath.Join(dir, "AGENTS.md")
				staleBlock := "<!-- BEGIN AF-COORDINATOR INTEGRATION v:1 -->\nold content\n<!-- END AF-COORDINATOR INTEGRATION -->"
				os.WriteFile(p, []byte("# Repo\n"+staleBlock+"\nFooter"), 0644)
				return p, func() {}
			},
			want:     initUpdated,
			wantFile: true,
			wantBody: "# Repo\n" + block + "\nFooter",
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
