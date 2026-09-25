package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/abevz/dibs/internal/client"
	"github.com/abevz/dibs/internal/config"
	"github.com/abevz/dibs/internal/firstuse"
)

func runInitSetup(ctx context.Context, c *client.Client, cfg config.Config, args []string) error {
	var opts firstuse.Options
	var target string
	var dryRun bool
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--dry-run":
			dryRun = true
		case "--path", "--project", "--repo", "--default-branch":
			if i+1 >= len(args) {
				return argumentError(args[i] + " requires a value")
			}
			value := args[i+1]
			i++
			switch args[i-1] {
			case "--path":
				target = value
			case "--project":
				opts.Project = value
			case "--repo":
				opts.Repo = value
			case "--default-branch":
				opts.DefaultBranch = value
			}
		}
	}
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	info, err := firstuse.Discover(ctx, dir, opts)
	if err != nil {
		return fmt.Errorf("init: %w", err)
	}
	if target == "" {
		target = filepath.Join(info.Worktree, "AGENTS.md")
	}
	if !jsonOutput {
		fmt.Printf("Project: %s\nRepository: %s\nGit directory: %s\nWorktree: %s (%s)\n", info.Project, info.Repo, info.GitDir, info.Worktree, info.Branch)
	}
	if !dryRun {
		if err := firstuse.EnsureDaemon(ctx, cfg, firstuse.FindDaemon()); err != nil {
			return fmt.Errorf("init: %w", err)
		}
		if _, _, _, err := firstuse.Register(ctx, c, info, opts.Project != ""); err != nil {
			return fmt.Errorf("init: register: %w", err)
		}
	}
	action, err := applyBlock(target, formatBlock(initSnippet), dryRun, nil)
	if err != nil {
		return fmt.Errorf("init: instructions: %w", err)
	}
	if jsonOutput {
		return json.NewEncoder(os.Stdout).Encode(map[string]any{
			"mapping": info, "action": []string{"created", "updated", "unchanged"}[action],
			"path": target, "dry_run": dryRun,
		})
	}
	verb := []string{"created", "updated", "unchanged"}[action]
	if dryRun {
		verb = "would " + verb
	}
	fmt.Printf("Instructions %s: %s\n", verb, target)
	return nil
}
