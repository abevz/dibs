package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/abevz/dibs/internal/client"
	"github.com/abevz/dibs/internal/core"
)

// ─── Worktree ───────────────────────────────────────────────────────────────

func runWorktree(ctx context.Context, c *client.Client, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("%s", "Usage: dibs worktree <register|list|unregister|prune>")
	}

	switch args[0] {
	case "register":
		return runWorktreeRegister(ctx, c, args[1:])
	case "list":
		return runWorktreeList(ctx, c, args[1:])
	case "unregister":
		return runWorktreeUnregister(ctx, c, args[1:])
	case "prune":
		return runWorktreePrune(ctx, c, args[1:])
	default:
		return fmt.Errorf("unknown worktree subcommand: %s\n", args[0])
	}
}

func runWorktreeRegister(ctx context.Context, c *client.Client, args []string) error {
	if len(args) < 4 {
		return fmt.Errorf("%s", "Usage: dibs worktree register --repo <repo-id> --absolute-path <path> [--branch <branch>] [--head-commit <sha>] [--remote-name <name>] [--remote-branch <branch>] [--main] [--ephemeral]")
	}

	var req core.CreateWorktreeRequest
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--repo":
			if i+1 < len(args) {
				req.Repo = args[i+1]
				i++
			}
		case "--absolute-path":
			if i+1 < len(args) {
				req.AbsolutePath = args[i+1]
				i++
			}
		case "--branch":
			if i+1 < len(args) {
				req.Branch = args[i+1]
				i++
			}
		case "--head-commit":
			if i+1 < len(args) {
				req.HeadCommit = args[i+1]
				i++
			}
		case "--remote-name":
			if i+1 < len(args) {
				req.RemoteName = args[i+1]
				i++
			}
		case "--remote-branch":
			if i+1 < len(args) {
				req.RemoteBranch = args[i+1]
				i++
			}
		case "--main":
			req.IsMain = true
		case "--ephemeral":
			req.IsEphemeral = true
		}
	}

	wt, err := c.RegisterWorktree(ctx, req)
	if err != nil {
		fail(err)
	}
	if jsonOutput {
		json.NewEncoder(os.Stdout).Encode(wt)
		return nil
	}
	printWorktree(wt)
	return nil
}

func runWorktreeList(ctx context.Context, c *client.Client, args []string) error {
	repo := ""
	for i := 0; i < len(args); i++ {
		if args[i] == "--repo" && i+1 < len(args) {
			repo = args[i+1]
		}
	}

	worktrees, err := c.ListWorktrees(ctx, repo)
	if err != nil {
		fail(err)
	}
	if jsonOutput {
		json.NewEncoder(os.Stdout).Encode(worktrees)
		return nil
	}
	if len(worktrees) == 0 {
		fmt.Println("No worktrees found.")
		return nil
	}
	for _, wt := range worktrees {
		printWorktree(wt)
	}
	return nil
}

func runWorktreeUnregister(ctx context.Context, c *client.Client, args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("%s", "Usage: dibs worktree unregister --worktree <worktree-id>")
	}

	worktreeID := ""
	for i := 0; i < len(args); i++ {
		if args[i] == "--worktree" && i+1 < len(args) {
			worktreeID = args[i+1]
			i++
		}
	}
	if worktreeID == "" {
		return fmt.Errorf("%s", "Usage: dibs worktree unregister --worktree <worktree-id>")
	}

	wt, err := c.DeleteWorktree(ctx, worktreeID)
	if err != nil {
		fail(err)
	}
	if jsonOutput {
		json.NewEncoder(os.Stdout).Encode(wt)
		return nil
	}

	fmt.Println("Unregistered worktree:")
	printWorktree(wt)
	return nil
}

func runWorktreePrune(ctx context.Context, c *client.Client, args []string) error {
	repo := ""
	for i := 0; i < len(args); i++ {
		if args[i] == "--repo" && i+1 < len(args) {
			repo = args[i+1]
			i++
		}
	}

	worktrees, err := c.ListWorktrees(ctx, repo)
	if err != nil {
		fail(err)
	}

	pruned := make([]core.Worktree, 0)
	for _, wt := range worktrees {
		if wt.IsMain {
			continue
		}
		if _, err := os.Stat(wt.AbsolutePath); err == nil {
			continue
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("stat worktree %s: %w", wt.AbsolutePath, err)
		}

		deleted, err := c.DeleteWorktree(ctx, wt.ID)
		if err != nil {
			fail(err)
		}
		pruned = append(pruned, deleted)
	}

	if jsonOutput {
		json.NewEncoder(os.Stdout).Encode(pruned)
		return nil
	}

	if len(pruned) == 0 {
		fmt.Println("No stale worktrees found.")
		return nil
	}

	fmt.Printf("Pruned %d stale worktree(s):\n\n", len(pruned))
	for _, wt := range pruned {
		printWorktree(wt)
	}
	return nil
}
