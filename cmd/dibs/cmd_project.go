package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/abevz/dibs/internal/client"
	"github.com/abevz/dibs/internal/core"
)

// ─── Project ────────────────────────────────────────────────────────────────

func runProject(ctx context.Context, c *client.Client, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("%s", "Usage: dibs project <add|list>")
	}

	switch args[0] {
	case "add":
		return runProjectAdd(ctx, c, args[1:])
	case "list":
		return runProjectList(ctx, c)
	default:
		return fmt.Errorf("unknown project subcommand: %s\n", args[0])
	}
}

func runProjectAdd(ctx context.Context, c *client.Client, args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("%s", "Usage: dibs project add --key <key> --name <name> [--description <desc>]")
	}

	var key, name, description string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--key":
			if i+1 < len(args) {
				key = args[i+1]
				i++
			}
		case "--name":
			if i+1 < len(args) {
				name = args[i+1]
				i++
			}
		case "--description":
			if i+1 < len(args) {
				description = args[i+1]
				i++
			}
		}
	}

	project, err := c.CreateProject(ctx, key, name, description)
	if err != nil {
		fail(err)
	}
	if jsonOutput {
		json.NewEncoder(os.Stdout).Encode(project)
		return nil
	}
	printProject(project)
	return nil
}

func runProjectList(ctx context.Context, c *client.Client) error {
	projects, err := c.ListProjects(ctx)
	if err != nil {
		fail(err)
	}
	if jsonOutput {
		json.NewEncoder(os.Stdout).Encode(projects)
		return nil
	}
	if len(projects) == 0 {
		fmt.Println("No projects found.")
		return nil
	}
	for _, p := range projects {
		printProject(p)
	}
	return nil
}

func printProject(p core.Project) {
	fmt.Printf("ID:          %s\n", p.ID)
	fmt.Printf("Key:         %s\n", p.Key)
	fmt.Printf("Name:        %s\n", p.Name)
	fmt.Printf("Description: %s\n", p.Description)
	fmt.Printf("Created:     %s\n", p.CreatedAt)
	fmt.Printf("Updated:     %s\n\n", p.UpdatedAt)
}
