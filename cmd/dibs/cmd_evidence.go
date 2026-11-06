package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/abevz/dibs/internal/client"
	"github.com/abevz/dibs/internal/core"
	"github.com/abevz/dibs/internal/evidence"
)

func evidenceFlags(args []string) map[string]string {
	result := map[string]string{}
	for i := 0; i < len(args); i++ {
		if args[i] == "--apply" {
			result[args[i]] = "true"
		} else if i+1 < len(args) {
			result[args[i]] = args[i+1]
			i++
		}
	}
	return result
}
func registerIssueEvidence(ctx context.Context, c *client.Client, args []string) error {
	f := evidenceFlags(args)
	path, err := filepath.Abs(f["--file"])
	if err != nil {
		return err
	}
	if f["--file"] == "" {
		return fmt.Errorf("--file is required with --issue")
	}
	req := core.RegisterEvidenceRequest{SourcePath: path, Kind: f["--kind"], CommitSHA: f["--commit"], DiffHash: f["--diff-hash"], Status: f["--status"]}
	if err = core.ValidateRegisterEvidence(req); err != nil {
		return err
	}
	e, err := c.RegisterEvidence(ctx, f["--issue"], req)
	if err != nil {
		return err
	}
	return outputEvidence([]core.Evidence{e})
}
func outputEvidence(items []core.Evidence) error {
	if jsonOutput {
		return json.NewEncoder(os.Stdout).Encode(items)
	}
	printEvidence(items)
	return nil
}
func printEvidence(items []core.Evidence) {
	for _, e := range items {
		fmt.Fprintf(os.Stdout, "Evidence: %s %s %s\n  SHA256: %s  diff: %s  commit: %s  status: %s  pruned: %s\n", e.ID, e.Kind, e.Path, e.SHA256, e.DiffHash, e.CommitSHA, e.Status, e.PrunedAt)
	}
}
func printRecoveryContext(note *core.Note, items []core.Evidence) {
	if note != nil {
		fmt.Fprintf(os.Stderr, "Prior handoff (%s, %s):\n%s\n", note.Author, note.CreatedAt, note.Body)
	}
	// Use stderr: issue run stdout belongs to the child or its JSON result.
	for _, e := range items {
		fmt.Fprintf(os.Stderr, "Evidence: %s %s %s (sha256 %s, diff %s, commit %s, status %s, pruned %s)\n", e.ID, e.Kind, e.Path, e.SHA256, e.DiffHash, e.CommitSHA, e.Status, e.PrunedAt)
	}
}
func runEvidenceMaintenance(ctx context.Context, c *client.Client, command string, args []string) error {
	f := evidenceFlags(args)
	apply := f["--apply"] == "true"
	if command == "prune" {
		days := 30
		var err error
		if f["--days"] != "" {
			days, err = strconv.Atoi(f["--days"])
			if err != nil || days < 1 {
				return fmt.Errorf("--days must be a positive integer")
			}
		}
		items, err := c.PruneEvidence(ctx, core.EvidenceMaintenanceRequest{IssueID: f["--issue"], Days: days, Apply: apply})
		if err != nil {
			return err
		}
		if !jsonOutput && !apply {
			fmt.Println("Preview only; use --apply to prune file bytes and retain metadata.")
		}
		return outputEvidence(items)
	}
	items, err := c.ListEvidence(ctx, f["--issue"])
	if err != nil {
		return err
	}
	selected := []core.Evidence{}
	terminal := map[string]bool{}
	for _, e := range items {
		if _, seen := terminal[e.IssueID]; seen {
			continue
		}
		issue, _, err := c.GetIssue(ctx, e.IssueID)
		if err != nil {
			return err
		}
		terminal[e.IssueID] = issue.Status == "done" || issue.Status == "cancelled"
	}
	for _, e := range items {
		if !terminal[e.IssueID] {
			continue
		}
		ok, err := evidence.CleanupSource(e, apply)
		if err != nil {
			return err
		}
		if ok {
			selected = append(selected, e)
		}
	}
	if !jsonOutput && !apply {
		fmt.Println("Preview only; use --apply to remove verified temporary source copies. Unknown files are preserved.")
	}
	return outputEvidence(selected)
}
func checkIssueEvidence(ctx context.Context, c *client.Client, id, kindList, diff string) error {
	if kindList == "" {
		return nil
	}
	kinds := strings.Split(kindList, ",")
	if err := core.ValidateEvidenceRequirement(kinds, diff); err != nil {
		return err
	}
	items, err := c.ListEvidence(ctx, id)
	if err != nil {
		return err
	}
	return evidence.Check(items, kinds, diff)
}
