package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/abevz/dibs/internal/core"
	"github.com/abevz/dibs/internal/evidence"
)

// Persist privately before reporting recovery paths. Tokens never appear in
// notes or diagnostics. A failed run never auto-releases ownership.
func saveRunRecovery(id string, claim core.ClaimResponse) (string, error) {
	root, err := evidence.Root()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(root, "recovery")
	if err = os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	dir, err = os.MkdirTemp(dir, "run-")
	if err != nil {
		return "", err
	}
	tokenPath := filepath.Join(dir, "lease-token")
	if err = os.WriteFile(tokenPath, []byte(claim.LeaseToken), 0600); err != nil {
		return "", err
	}
	data, err := json.MarshalIndent(struct {
		IssueID    string `json:"issue_id"`
		Generation int64  `json:"lease_generation"`
		Version    int    `json:"version"`
		TokenFile  string `json:"token_file"`
	}{id, claim.LeaseGeneration, claim.Version, tokenPath}, "", "  ")
	if err != nil {
		return "", err
	}
	if err = os.WriteFile(filepath.Join(dir, "recovery.json"), data, 0600); err != nil {
		return "", err
	}
	return tokenPath, nil
}
func runWorkContext() string {
	cwd, _ := os.Getwd()
	branch := ""
	cmd := exec.Command("git", "branch", "--show-current")
	if out, err := cmd.Output(); err == nil {
		branch = string(out)
	}
	return fmt.Sprintf("\nWorktree: %s\nBranch: %s", cwd, branch)
}
