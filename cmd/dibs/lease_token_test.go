package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLifecycleTokenSourcesAvoidArgv(t *testing.T) {
	args := []string{"issue", "heartbeat", "afc-1", "--lease-generation", "7"}
	t.Setenv("DIBS_LEASE_TOKEN", "canonical-secret")
	t.Setenv("AF_LEASE_TOKEN", "legacy-secret")
	if err := validateCommandArgs(args); err != nil {
		t.Fatal(err)
	}
	token, err := leaseTokenFromEnvironment()
	if err != nil || token != "canonical-secret" {
		t.Fatalf("canonical token precedence = %q, %v", token, err)
	}
	if strings.Contains(strings.Join(args, " "), token) {
		t.Fatal("token entered argv")
	}

	t.Setenv("DIBS_LEASE_TOKEN", "")
	t.Setenv("AF_LEASE_TOKEN", "")
	path := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(path, []byte("file-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DIBS_LEASE_TOKEN_FILE", path)
	if err := validateCommandArgs(args); err != nil {
		t.Fatal(err)
	}
	token, err = leaseTokenFromEnvironment()
	if err != nil || token != "file-secret" {
		t.Fatalf("file token = %q, %v", token, err)
	}
	if strings.Contains(strings.Join(args, " "), token) {
		t.Fatal("file token entered argv")
	}
}

func TestLifecycleTokenSourceMissingFailsClosed(t *testing.T) {
	t.Setenv("DIBS_LEASE_TOKEN", "")
	t.Setenv("AF_LEASE_TOKEN", "legacy-secret")
	t.Setenv("DIBS_LEASE_TOKEN_FILE", "")
	if err := validateCommandArgs([]string{"issue", "close", "afc-1", "--resolution", "done", "--expected-version", "2", "--lease-generation", "1"}); err == nil || !strings.Contains(err.Error(), "--lease-token") {
		t.Fatalf("missing token validation = %v", err)
	}
	if token, err := leaseTokenFromEnvironment(); err == nil || token != "" {
		t.Fatalf("empty canonical source fell through to legacy: token=%q err=%v", token, err)
	}
}
