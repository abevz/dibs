package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/abevz/dibs/internal/integration"
)

//go:embed init-snippet.md
var initSnippet string

const (
	beginMarker = integration.BeginMarker
	endMarker   = integration.EndMarker
	// Keep recognizing older versions so worktrees can migrate in place.
	legacyBeginMarker = integration.LegacyBeginMarker
	legacyEndMarker   = integration.LegacyEndMarker
)

type initAction int

const (
	initCreated initAction = iota
	initUpdated
	initUnchanged
	initMigrated
)

func (action initAction) String() string {
	return []string{"created", "updated", "unchanged", "migrated"}[action]
}

func runInit(args []string) error {
	targetPath := "AGENTS.md"
	flags := make(map[string]string)
	dryRun := false

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--path":
			if i+1 < len(args) {
				targetPath = args[i+1]
				i++
			}
		case "--dry-run":
			dryRun = true
		case "--json":
			// Already parsed globally, ignore
		default:
			return fmt.Errorf("unknown flag: %s", args[i])
		}
	}

	action, err := applyBlock(targetPath, formatBlock(initSnippet), dryRun, flags)
	if err != nil {
		fail(fmt.Errorf("init: %w", err))
	}

	if jsonOutput {
		resp := map[string]interface{}{
			"action": action.String(),
			"path":   targetPath,
		}
		if dryRun {
			resp["dry_run"] = true
		}
		json.NewEncoder(os.Stdout).Encode(resp)
		return nil
	}

	prefix := ""
	if dryRun {
		prefix = "would "
	}
	switch action {
	case initCreated:
		fmt.Printf("%screated: %s\n", prefix, targetPath)
	case initUpdated:
		fmt.Printf("%supdated: %s\n", prefix, targetPath)
	case initUnchanged:
		fmt.Printf("%sunchanged: %s\n", prefix, targetPath)
	case initMigrated:
		fmt.Printf("%smigrated: %s\n", prefix, targetPath)
	}
	return nil
}

func formatBlock(content string) string {
	return beginMarker + "\n" +
		strings.TrimSpace(content) + "\n" +
		endMarker + "\n"
}

func applyBlock(path, block string, dryRun bool, flags map[string]string) (initAction, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return 0, err
		}
		// State 1: file doesn't exist → create
		if !dryRun {
			if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
				return 0, err
			}
			if err := os.WriteFile(path, []byte(block), 0644); err != nil {
				return 0, err
			}
		}
		return initCreated, nil
	}

	content := string(data)
	startIdx, endIdx, err := markerRange(content, endMarker, beginMarker, integration.PreviousMarker)
	if err != nil {
		return 0, err
	}
	legacyStart, legacyEnd, err := markerRange(content, legacyEndMarker, legacyBeginMarker)
	if err != nil {
		return 0, err
	}
	if startIdx >= 0 && legacyStart >= 0 {
		line := func(index int) int { return strings.Count(content[:index], "\n") + 1 }
		return 0, fmt.Errorf("both DIBS (lines %d-%d) and AF-COORDINATOR (lines %d-%d) integration blocks exist; resolve the duplicate blocks before running init", line(startIdx), line(endIdx), line(legacyStart), line(legacyEnd))
	}
	action := initUpdated
	marker := endMarker
	if startIdx < 0 && legacyStart >= 0 {
		startIdx, endIdx, marker, action = legacyStart, legacyEnd, legacyEndMarker, initMigrated
	}

	if startIdx >= 0 && endIdx > startIdx {
		// Block exists. Check if content matches.
		existingBlock := content[startIdx : endIdx+len(marker)]
		if normalizeBlock(existingBlock) == normalizeBlock(block) {
			// State 4: current block → no-op
			return initUnchanged, nil
		}
		// State 3: stale block → replace in place
		// The separator after the end marker belongs to surrounding content.
		newContent := content[:startIdx] + strings.TrimSuffix(block, "\n") + content[endIdx+len(marker):]
		if !dryRun {
			if err := os.WriteFile(path, []byte(newContent), 0644); err != nil {
				return 0, err
			}
		}
		return action, nil
	}

	// State 2: file exists, no block → append
	if !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	content += "\n" + block
	if !dryRun {
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			return 0, err
		}
	}
	return initUpdated, nil
}

func normalizeBlock(block string) string {
	return strings.TrimSpace(block)
}

func markerRange(content, end string, begins ...string) (int, int, error) {
	starts, ends := 0, strings.Count(content, end)
	start := -1
	for _, begin := range begins {
		if count := strings.Count(content, begin); count != 0 {
			starts += count
			start = strings.Index(content, begin)
		}
	}
	if starts == 0 && ends == 0 {
		return -1, -1, nil
	}
	finish := strings.Index(content, end)
	if starts != 1 || ends != 1 || finish < start {
		return -1, -1, fmt.Errorf("malformed or duplicate integration markers %q / %q; file left unchanged", begins, end)
	}
	return start, finish, nil
}
