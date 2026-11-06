package core

import (
	"fmt"
	"path/filepath"
	"regexp"
)

// Evidence is an immutable, issue-scoped file and revision binding.
type Evidence struct {
	ID         string `json:"id"`
	IssueID    string `json:"issue_id"`
	Kind       string `json:"kind"`
	Path       string `json:"path"`
	SourcePath string `json:"source_path"`
	SHA256     string `json:"sha256"`
	Size       int64  `json:"size"`
	CommitSHA  string `json:"commit_sha,omitempty"`
	DiffHash   string `json:"diff_hash,omitempty"`
	Status     string `json:"status,omitempty"`
	CreatedAt  string `json:"created_at"`
	PrunedAt   string `json:"pruned_at,omitempty"`
}

type RegisterEvidenceRequest struct {
	SourcePath string `json:"source_path"`
	Kind       string `json:"kind"`
	CommitSHA  string `json:"commit_sha,omitempty"`
	DiffHash   string `json:"diff_hash,omitempty"`
	Status     string `json:"status,omitempty"`
}

type EvidenceMaintenanceRequest struct {
	IssueID string `json:"issue_id,omitempty"`
	Days    int    `json:"days"`
	Apply   bool   `json:"apply"`
}

var sha256Pattern = regexp.MustCompile(`^[a-f0-9]{64}$`)
var commitPattern = regexp.MustCompile(`^[a-f0-9]{40}([a-f0-9]{24})?$`)

func ValidEvidenceKind(kind string) bool {
	switch kind {
	case "screenshot", "log", "review", "integration":
		return true
	}
	return false
}
func ValidateRegisterEvidence(r RegisterEvidenceRequest) error {
	if !filepath.IsAbs(r.SourcePath) || !ValidEvidenceKind(r.Kind) {
		return fmt.Errorf("absolute source_path and kind screenshot|log|review|integration are required")
	}
	if r.DiffHash == "" && r.CommitSHA == "" {
		return fmt.Errorf("commit_sha or diff_hash is required")
	}
	if r.DiffHash != "" && !sha256Pattern.MatchString(r.DiffHash) {
		return fmt.Errorf("diff_hash must be a lowercase SHA-256")
	}
	if r.CommitSHA != "" && !commitPattern.MatchString(r.CommitSHA) {
		return fmt.Errorf("commit_sha must be a full lowercase Git object ID")
	}
	if r.Status != "" && (r.Kind != "review" || (r.Status != "PASS" && r.Status != "FAIL")) {
		return fmt.Errorf("status must be PASS or FAIL and only applies to review evidence")
	}
	return nil
}
func ValidateEvidenceRequirement(kinds []string, diff string) error {
	if len(kinds) == 0 {
		return nil
	}
	if !sha256Pattern.MatchString(diff) {
		return fmt.Errorf("required evidence needs a final diff_hash (SHA-256)")
	}
	for _, kind := range kinds {
		if !ValidEvidenceKind(kind) {
			return fmt.Errorf("unknown evidence kind %q", kind)
		}
	}
	return nil
}
