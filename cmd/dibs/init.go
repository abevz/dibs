package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

//go:embed init-snippet.md
var initSnippet string

const (
	beginMarker = "<!-- BEGIN AF-COORDINATOR INTEGRATION v:1 -->"
	endMarker   = "<!-- END AF-COORDINATOR INTEGRATION -->"
)

type initAction int

const (
	initCreated initAction = iota
	initUpdated
	initUnchanged
)

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
			"action": []string{"created", "updated", "unchanged"}[action],
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
	startIdx := strings.Index(content, beginMarker)
	endIdx := strings.Index(content, endMarker)

	if startIdx >= 0 && endIdx > startIdx {
		// Block exists. Check if content matches.
		existingBlock := content[startIdx : endIdx+len(endMarker)]
		if normalizeBlock(existingBlock) == normalizeBlock(block) {
			// State 4: current block → no-op
			return initUnchanged, nil
		}
		// State 3: stale block → replace in place
		newContent := content[:startIdx] + block + content[endIdx+len(endMarker):]
		if !dryRun {
			if err := os.WriteFile(path, []byte(newContent), 0644); err != nil {
				return 0, err
			}
		}
		return initUpdated, nil
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
