package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInitDryRunJSONOutput(t *testing.T) {
	oldJSON := jsonOutput
	jsonOutput = true
	defer func() { jsonOutput = oldJSON }()

	dir := t.TempDir()
	p := filepath.Join(dir, "AGENTS.md")
	before := []byte("# Repo\n")
	if err := os.WriteFile(p, before, 0644); err != nil {
		t.Fatal(err)
	}

	// Capture dry-run output
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	runInit([]string{"--path", p, "--dry-run"})
	w.Close()
	var dryBuf bytes.Buffer
	dryBuf.ReadFrom(r)
	os.Stdout = oldStdout

	// Verify file unchanged
	after, _ := os.ReadFile(p)
	if !bytes.Equal(before, after) {
		t.Error("dry-run modified the file")
	}

	// Verify JSON has dry_run=true
	var dryResp map[string]interface{}
	if err := json.Unmarshal(dryBuf.Bytes(), &dryResp); err != nil {
		t.Fatal(err)
	}
	if dryResp["dry_run"] != true {
		t.Errorf("dry_run=%v, want true", dryResp["dry_run"])
	}
	if dryResp["action"] != "updated" {
		t.Errorf("action=%v, want updated", dryResp["action"])
	}

	// Now real run
	var realBuf bytes.Buffer
	r2, w2, _ := os.Pipe()
	os.Stdout = w2
	runInit([]string{"--path", p})
	w2.Close()
	realBuf.ReadFrom(r2)
	os.Stdout = oldStdout

	// Verify file was modified
	afterReal, _ := os.ReadFile(p)
	if bytes.Equal(before, afterReal) {
		t.Error("real run did NOT modify the file")
	}

	// Verify real JSON has NO dry_run
	var realResp map[string]interface{}
	if err := json.Unmarshal(realBuf.Bytes(), &realResp); err != nil {
		t.Fatal(err)
	}
	_, hasDryRun := realResp["dry_run"]
	if hasDryRun {
		t.Error("real output should not have dry_run")
	}
}

func TestInitDryRunTextOutput(t *testing.T) {
	oldJSON := jsonOutput
	jsonOutput = false
	defer func() { jsonOutput = oldJSON }()

	dir := t.TempDir()
	p := filepath.Join(dir, "AGENTS.md")
	os.WriteFile(p, []byte{}, 0644)

	var buf bytes.Buffer
	r, w, _ := os.Pipe()
	os.Stdout = w
	runInit([]string{"--path", p, "--dry-run"})
	w.Close()
	buf.ReadFrom(r)
	os.Stdout = os.Stderr

	out := strings.TrimSpace(buf.String())
	if !strings.HasPrefix(out, "would ") {
		t.Errorf("text output %q does not start with 'would '", out)
	}
}
