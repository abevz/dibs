package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildInstallLegacyAliasesPreserveStdout(t *testing.T) {
	bindir := t.TempDir()
	build := exec.Command("make", "build-install", "BINDIR="+bindir)
	build.Dir = filepath.Join("..", "..")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build-install: %v\n%s", err, out)
	}
	for legacy, current := range map[string]string{
		"afctl": "dibs", "af-coordinatord": "dibsd", "afc-mcp": "dibs-mcp",
	} {
		got, err := os.Readlink(filepath.Join(bindir, legacy))
		if err != nil || got != current {
			t.Fatalf("%s alias = %q, %v; want %s", legacy, got, err, current)
		}
	}
	current := exec.Command(filepath.Join(bindir, "dibs"), "--version")
	legacy := exec.Command(filepath.Join(bindir, "afctl"), "--version")
	var currentOut, legacyOut, legacyErr bytes.Buffer
	current.Stdout = &currentOut
	legacy.Stdout, legacy.Stderr = &legacyOut, &legacyErr
	if err := current.Run(); err != nil {
		t.Fatal(err)
	}
	if err := legacy.Run(); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(currentOut.Bytes(), legacyOut.Bytes()) {
		t.Fatalf("alias stdout differs: current=%q legacy=%q", currentOut.String(), legacyOut.String())
	}
	if !strings.Contains(legacyErr.String(), "afctl is deprecated; use dibs") {
		t.Fatalf("missing stderr deprecation: %q", legacyErr.String())
	}
	input := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}` + "\n"
	var mcpOut, aliasMCPOut, aliasMCPErr bytes.Buffer
	for _, tt := range []struct {
		name   string
		stdout *bytes.Buffer
		stderr *bytes.Buffer
	}{
		{"dibs-mcp", &mcpOut, nil},
		{"afc-mcp", &aliasMCPOut, &aliasMCPErr},
	} {
		cmd := exec.Command(filepath.Join(bindir, tt.name))
		cmd.Stdin = strings.NewReader(input)
		cmd.Stdout = tt.stdout
		if tt.stderr != nil {
			cmd.Stderr = tt.stderr
		}
		if err := cmd.Run(); err != nil {
			t.Fatalf("%s initialize: %v", tt.name, err)
		}
	}
	if !bytes.Equal(mcpOut.Bytes(), aliasMCPOut.Bytes()) {
		t.Fatalf("MCP alias stdout differs: current=%q legacy=%q", mcpOut.String(), aliasMCPOut.String())
	}
	if !strings.Contains(aliasMCPErr.String(), "afc-mcp is deprecated; use dibs-mcp") {
		t.Fatalf("missing MCP stderr deprecation: %q", aliasMCPErr.String())
	}
}
