package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/abevz/dibs/internal/client"
	"github.com/abevz/dibs/internal/core"
)

// ─── Repo ───────────────────────────────────────────────────────────────────

func runRepo(ctx context.Context, c *client.Client, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("%s", "Usage: dibs repo <add|list>")
	}

	switch args[0] {
	case "add":
		return runRepoAdd(ctx, c, args[1:])
	case "list":
		return runRepoList(ctx, c, args[1:])
	default:
		return fmt.Errorf("unknown repo subcommand: %s\n", args[0])
	}
}

func runRepoAdd(ctx context.Context, c *client.Client, args []string) error {
	if len(args) < 4 {
		return fmt.Errorf("%s", "Usage: dibs repo add --project <key> --logical-name <name> --canonical-git-dir <path> [--default-branch <branch>] [--remotes '<json>']")
	}

	var req core.CreateRepoRequest
	req.DefaultBranch = "main"

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--project":
			if i+1 < len(args) {
				req.Project = args[i+1]
				i++
			}
		case "--logical-name":
			if i+1 < len(args) {
				req.LogicalName = args[i+1]
				i++
			}
		case "--canonical-git-dir":
			if i+1 < len(args) {
				req.CanonicalGitDir = args[i+1]
				i++
			}
		case "--default-branch":
			if i+1 < len(args) {
				req.DefaultBranch = args[i+1]
				i++
			}
		case "--remotes":
			if i+1 < len(args) {
				if err := json.Unmarshal([]byte(args[i+1]), &req.Remotes); err != nil {
					return fmt.Errorf("invalid --remotes JSON: %v", err)
				}
				i++
			}
		}
	}

	repo, remotes, err := c.CreateRepo(ctx, req)
	if err != nil {
		fail(err)
	}
	if jsonOutput {
		json.NewEncoder(os.Stdout).Encode(map[string]any{
			"repository": repo,
			"remotes":    remotes,
		})
		return nil
	}
	fmt.Printf("Repository ID: %s\n", repo.ID)
	fmt.Printf("Project ID:    %s\n", repo.ProjectID)
	fmt.Printf("Logical Name:  %s\n", repo.LogicalName)
	fmt.Printf("Git Dir:       %s\n", repo.CanonicalGitDir)
	fmt.Printf("Default Branch:%s\n", repo.DefaultBranch)
	if len(remotes) > 0 {
		fmt.Println("Remotes:")
		for _, r := range remotes {
			primary := ""
			if r.IsPrimary {
				primary = " (primary)"
			}
			fmt.Printf("  - %s -> %s%s\n", r.RemoteName, r.FetchURL, primary)
		}
	}
	fmt.Println()
	return nil
}

func runRepoList(ctx context.Context, c *client.Client, args []string) error {
	project := ""
	for i := 0; i < len(args); i++ {
		if args[i] == "--project" && i+1 < len(args) {
			project = args[i+1]
		}
	}

	repos, err := c.ListRepos(ctx, project)
	if err != nil {
		fail(err)
	}
	if jsonOutput {
		json.NewEncoder(os.Stdout).Encode(repos)
		return nil
	}
	if len(repos) == 0 {
		fmt.Println("No repositories found.")
		return nil
	}
	for _, r := range repos {
		fmt.Printf("ID:           %s\n", r.ID)
		fmt.Printf("Project ID:   %s\n", r.ProjectID)
		fmt.Printf("Logical Name: %s\n", r.LogicalName)
		fmt.Printf("Git Dir:      %s\n", r.CanonicalGitDir)
		fmt.Printf("Branch:       %s\n", r.DefaultBranch)
		fmt.Printf("Created:      %s\n\n", r.CreatedAt)
	}
	return nil
}
