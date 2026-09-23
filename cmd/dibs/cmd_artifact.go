package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/abevz/dibs/internal/client"
	"github.com/abevz/dibs/internal/core"
)

// ─── Artifact Root ──────────────────────────────────────────────────────────

func runArtifactRoot(ctx context.Context, c *client.Client, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("%s", "Usage: dibs artifact-root <add|list>")
	}

	switch args[0] {
	case "add":
		return runArtifactRootAdd(ctx, c, args[1:])
	case "list":
		return runArtifactRootList(ctx, c, args[1:])
	default:
		return fmt.Errorf("unknown artifact-root subcommand: %s\n", args[0])
	}
}

func runArtifactRootAdd(ctx context.Context, c *client.Client, args []string) error {
	if len(args) < 4 {
		return fmt.Errorf("%s", "Usage: dibs artifact-root add --repo <repo-id> --root-path <path> [--kind <kind>] [--primary]")
	}

	var req core.CreateArtifactRootRequest
	req.Kind = "sdd"

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--repo":
			if i+1 < len(args) {
				req.Repo = args[i+1]
				i++
			}
		case "--root-path":
			if i+1 < len(args) {
				req.RootPath = args[i+1]
				i++
			}
		case "--kind":
			if i+1 < len(args) {
				req.Kind = args[i+1]
				i++
			}
		case "--primary":
			req.Primary = true
		}
	}

	root, err := c.CreateArtifactRoot(ctx, req)
	if err != nil {
		fail(err)
	}
	if jsonOutput {
		json.NewEncoder(os.Stdout).Encode(root)
		return nil
	}
	printArtifactRoot(root)
	return nil
}

func runArtifactRootList(ctx context.Context, c *client.Client, args []string) error {
	repo := ""
	for i := 0; i < len(args); i++ {
		if args[i] == "--repo" && i+1 < len(args) {
			repo = args[i+1]
		}
	}

	roots, err := c.ListArtifactRoots(ctx, repo)
	if err != nil {
		fail(err)
	}
	if jsonOutput {
		json.NewEncoder(os.Stdout).Encode(roots)
		return nil
	}
	if len(roots) == 0 {
		fmt.Println("No artifact roots found.")
		return nil
	}
	for _, r := range roots {
		printArtifactRoot(r)
	}
	return nil
}

func printArtifactRoot(r core.ArtifactRoot) {
	primary := ""
	if r.IsPrimary {
		primary = " (primary)"
	}
	fmt.Printf("ID:           %s%s\n", r.ID, primary)
	fmt.Printf("Repository ID:%s\n", r.RepositoryID)
	fmt.Printf("Root Path:    %s\n", r.RootPath)
	fmt.Printf("Kind:         %s\n", r.Kind)
	fmt.Printf("Created:      %s\n", r.CreatedAt)
	fmt.Printf("Updated:      %s\n\n", r.UpdatedAt)
}

// ─── Artifact ───────────────────────────────────────────────────────────────

func runArtifact(ctx context.Context, c *client.Client, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("%s", "Usage: dibs artifact <register|list>")
	}

	switch args[0] {
	case "register":
		return runArtifactRegister(ctx, c, args[1:])
	case "list":
		return runArtifactList(ctx, c, args[1:])
	default:
		return fmt.Errorf("unknown artifact subcommand: %s\n", args[0])
	}
}

func runArtifactRegister(ctx context.Context, c *client.Client, args []string) error {
	if len(args) < 6 {
		return fmt.Errorf("%s", "Usage: dibs artifact register --repo <repo-id> --relative-path <path> --kind <kind> [--worktree <worktree-id>] [--artifact-root <root-id>] [--title <title>] [--external-key <key>] [--status <status>]")
	}

	var req core.CreateArtifactRequest
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--repo":
			if i+1 < len(args) {
				req.Repo = args[i+1]
				i++
			}
		case "--relative-path":
			if i+1 < len(args) {
				req.RelativePath = args[i+1]
				i++
			}
		case "--kind":
			if i+1 < len(args) {
				req.Kind = args[i+1]
				i++
			}
		case "--worktree":
			if i+1 < len(args) {
				req.Worktree = args[i+1]
				i++
			}
		case "--artifact-root":
			if i+1 < len(args) {
				req.ArtifactRootID = args[i+1]
				i++
			}
		case "--title":
			if i+1 < len(args) {
				req.Title = args[i+1]
				i++
			}
		case "--external-key":
			if i+1 < len(args) {
				req.ExternalKey = args[i+1]
				i++
			}
		case "--status":
			if i+1 < len(args) {
				req.Status = args[i+1]
				i++
			}
		}
	}

	artifact, err := c.CreateArtifact(ctx, req)
	if err != nil {
		fail(err)
	}
	if jsonOutput {
		json.NewEncoder(os.Stdout).Encode(artifact)
		return nil
	}
	printArtifact(artifact)
	return nil
}

func runArtifactList(ctx context.Context, c *client.Client, args []string) error {
	repo := ""
	for i := 0; i < len(args); i++ {
		if args[i] == "--repo" && i+1 < len(args) {
			repo = args[i+1]
		}
	}

	artifacts, err := c.ListArtifacts(ctx, repo)
	if err != nil {
		fail(err)
	}
	if jsonOutput {
		json.NewEncoder(os.Stdout).Encode(artifacts)
		return nil
	}
	if len(artifacts) == 0 {
		fmt.Println("No artifacts found.")
		return nil
	}
	for _, a := range artifacts {
		printArtifact(a)
	}
	return nil
}

func printArtifact(a core.Artifact) {
	fmt.Printf("ID:             %s\n", a.ID)
	fmt.Printf("Repository ID:  %s\n", a.RepositoryID)
	if a.WorktreeID != "" {
		fmt.Printf("Worktree ID:    %s\n", a.WorktreeID)
	}
	if a.ArtifactRootID != "" {
		fmt.Printf("Artifact Root:  %s\n", a.ArtifactRootID)
	}
	fmt.Printf("Kind:           %s\n", a.Kind)
	fmt.Printf("Relative Path:  %s\n", a.RelativePath)
	if a.Title != "" {
		fmt.Printf("Title:          %s\n", a.Title)
	}
	if a.ExternalKey != "" {
		fmt.Printf("External Key:   %s\n", a.ExternalKey)
	}
	if a.Status != "" {
		fmt.Printf("Status:         %s\n", a.Status)
	}
	fmt.Printf("Created:        %s\n", a.CreatedAt)
	fmt.Printf("Updated:        %s\n\n", a.UpdatedAt)
}
