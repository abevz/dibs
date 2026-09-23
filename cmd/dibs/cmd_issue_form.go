package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/abevz/dibs/internal/client"
	"github.com/abevz/dibs/internal/core"
	"github.com/charmbracelet/huh"
)

func runIssueCreateForm(ctx context.Context, c *client.Client, args []string) error {
	if hasHelpFlag(args) {
		fmt.Println("Usage: dibs issue create-form — interactive issue creation wizard (no flags)")
		return nil
	}

	// --allow-duplicate may be passed before entering the interactive form.
	allowDuplicate := false
	for _, a := range args {
		if a == "--allow-duplicate" {
			allowDuplicate = true
			break
		}
	}

	actor, _ := resolveActor("")

	projects, err := c.ListProjects(ctx)
	if err != nil {
		return fmt.Errorf("fetch projects: %w", err)
	}
	if len(projects) == 0 {
		return fmt.Errorf("no projects found, please create a project first")
	}

	projectOpts := make([]huh.Option[string], len(projects))
	projectKeyByID := make(map[string]string)
	for i, p := range projects {
		projectOpts[i] = huh.NewOption(p.Key, p.Key)
		projectKeyByID[p.ID] = p.Key
	}

	repos, err := c.ListRepos(ctx, "")
	if err != nil {
		return fmt.Errorf("fetch all repos: %w", err)
	}
	var allRepoOpts []huh.Option[string]
	repoNameByID := make(map[string]string)
	for _, r := range repos {
		pKey := projectKeyByID[r.ProjectID]
		allRepoOpts = append(allRepoOpts, huh.NewOption(fmt.Sprintf("[%s] %s", pKey, r.LogicalName), r.LogicalName))
		repoNameByID[r.ID] = r.LogicalName
	}
	if len(allRepoOpts) == 0 {
		allRepoOpts = append(allRepoOpts, huh.NewOption("No repos available", ""))
	}

	wts, err := c.ListWorktrees(ctx, "")
	if err != nil {
		return fmt.Errorf("fetch all worktrees: %w", err)
	}
	var allWtOpts []huh.Option[string]
	for _, w := range wts {
		rName := repoNameByID[w.RepositoryID]
		label := fmt.Sprintf("[%s] %s (%s)", rName, w.Branch, w.ID[:8])
		allWtOpts = append(allWtOpts, huh.NewOption(label, w.ID))
	}
	if len(allWtOpts) == 0 {
		allWtOpts = append(allWtOpts, huh.NewOption("No worktrees available", ""))
	}

	var project, scope, repo, worktree, title, issueType, externalKey string
	// Preselect the API default (priority 3) instead of huh's first option.
	priorityStr := "3"
	var description, acceptance, assignee string
	var blockedByStr, parentStr, artifactLink string
	var confirm bool

	fields1 := []huh.Field{
		huh.NewInput().Title("Title").Value(&title).Validate(func(s string) error {
			if strings.TrimSpace(s) == "" {
				return fmt.Errorf("title is required")
			}
			return nil
		}),
		huh.NewSelect[string]().Title("Project").Options(projectOpts...).Value(&project),
		huh.NewSelect[string]().Title("Scope").Options(
			huh.NewOption("Project", "project"),
			huh.NewOption("Repository", "repository"),
			huh.NewOption("Worktree", "worktree"),
		).Value(&scope),
		huh.NewSelect[string]().Title("Type").Options(
			huh.NewOption("Task", "task"),
			huh.NewOption("Bug", "bug"),
			huh.NewOption("Feature", "feature"),
			huh.NewOption("Epic", "epic"),
			huh.NewOption("Chore", "chore"),
		).Value(&issueType),
		huh.NewSelect[string]().Title("Priority").Options(
			huh.NewOption("1 (Urgent)", "1"),
			huh.NewOption("2 (High)", "2"),
			huh.NewOption("3 (Normal)", "3"),
			huh.NewOption("4 (Low)", "4"),
		).Value(&priorityStr),
	}

	if actor == "" {
		fields1 = append(fields1, huh.NewInput().Title("Actor").Value(&actor).Validate(func(s string) error {
			if strings.TrimSpace(s) == "" {
				return fmt.Errorf("actor is required")
			}
			return nil
		}))
	}

	err = huh.NewForm(
		huh.NewGroup(fields1...).Title("Context & Basics"),
		huh.NewGroup(
			huh.NewSelect[string]().Title("Repository").Options(allRepoOpts...).Value(&repo).Validate(func(r string) error {
				for _, rp := range repos {
					if rp.LogicalName == r && projectKeyByID[rp.ProjectID] == project {
						return nil
					}
				}
				return fmt.Errorf("repository %s does not exist in project %s", r, project)
			}),
		).Title("Repository").WithHideFunc(func() bool {
			return scope != "repository" && scope != "worktree"
		}),
		huh.NewGroup(
			huh.NewSelect[string]().Title("Worktree").Options(allWtOpts...).Value(&worktree).Validate(func(w string) error {
				for _, wt := range wts {
					if wt.ID == w {
						rName := repoNameByID[wt.RepositoryID]
						if rName != repo {
							return fmt.Errorf("worktree belongs to repo %s, not %s", rName, repo)
						}
						return nil
					}
				}
				return fmt.Errorf("worktree not found")
			}),
		).Title("Worktree").WithHideFunc(func() bool {
			return scope != "worktree"
		}),
		huh.NewGroup(
			huh.NewText().Title("Description").Value(&description).Lines(5),
			huh.NewText().Title("Acceptance criteria (optional)").Value(&acceptance).Lines(4),
			huh.NewInput().Title("External key (optional)").Value(&externalKey),
			huh.NewInput().Title("Assignee (optional)").Value(&assignee),
		).Title("Details"),
		huh.NewGroup(
			huh.NewInput().Title("Blocked by (comma-separated short IDs, e.g. utils-5,afc-3)").Value(&blockedByStr),
			huh.NewInput().Title("Parent issue (short ID, e.g. utils-10)").Value(&parentStr),
			huh.NewInput().Title("Artifact Link (relative path or UUID)").Value(&artifactLink),
		).Title("Dependencies & Links"),
		huh.NewGroup(
			huh.NewConfirm().Title("Create Issue?").Value(&confirm),
		),
	).WithTheme(huh.ThemeBase()).Run()

	if err != nil {
		return err // User cancelled
	}

	if !confirm {
		fmt.Println("Cancelled.")
		return nil
	}

	priority, _ := strconv.Atoi(priorityStr)

	req := core.CreateIssueRequest{
		Project:            project,
		ScopeKind:          scope,
		IssueType:          issueType,
		Repo:               repo,
		Worktree:           worktree,
		Title:              title,
		ExternalKey:        externalKey,
		Description:        description,
		AcceptanceCriteria: acceptance,
		Priority:           priority,
		Actor:              actor,
	}
	req.OperationID = newOperationID()
	journalTarget := req.Project + "-" + core.OperationFingerprint(core.CreateFingerprintFields(req.Project, req))
	journalPath, previousOperationID, err := journalOperationID("create", journalTarget, req.OperationID)
	if err != nil {
		return err
	}

	// Duplicate title warning: flag open/in_progress issues with the same title
	// in the same project, unless --allow-duplicate is explicitly passed.
	if !allowDuplicate && req.Project != "" && req.Title != "" {
		existing, err := c.ListIssuesWithFilters(ctx, core.IssueListParams{
			Projects: []string{req.Project},
			Statuses: []string{"open", "in_progress"},
		})
		if err == nil {
			for _, iss := range existing {
				if iss.Title == req.Title {
					fmt.Printf("Warning: an %s issue with the same title already exists: %s (%s)\n", iss.Status, iss.ShortID, iss.ID)
					break
				}
			}
		}
	}

	issue, err := c.CreateIssue(ctx, req)
	if err != nil {
		if createFailureDefinitelyRejected(err) {
			restoreJournaledOperationID(journalPath, previousOperationID)
		} else {
			fmt.Fprintf(os.Stderr, "create outcome is unconfirmed; operation_id: %s; journaled at: %s; retry with the same create arguments and --operation-id %s\n", req.OperationID, journalPath, req.OperationID)
		}
		return fmt.Errorf("failed to create issue: %w", err)
	}

	fmt.Printf("Created issue: %s (%s)\n", issue.ShortID, issue.ID)
	fmt.Printf("Operation ID: %s\n", req.OperationID)

	// Post-create steps
	if assignee != "" {
		_, err := c.UpdateIssue(ctx, issue.ID, core.UpdateIssueRequest{
			Assignee:        assignee,
			ExpectedVersion: issue.Version,
			Actor:           actor,
		})
		if err != nil {
			fmt.Printf("Warning: failed to set assignee: %v\n", err)
		} else {
			fmt.Printf("Set assignee to: %s\n", assignee)
		}
	}

	if blockedByStr != "" {
		deps := strings.Split(blockedByStr, ",")
		for _, dep := range deps {
			dep = strings.TrimSpace(dep)
			if dep == "" {
				continue
			}
			err := c.AddDependency(ctx, issue.ID, core.AddDependencyRequest{
				DependsOn: dep,
				Kind:      "blocks",
				Actor:     actor,
			})
			if err != nil {
				fmt.Printf("Warning: failed to add blocker %s: %v\n", dep, err)
			} else {
				fmt.Printf("Added blocker: %s\n", dep)
			}
		}
	}

	if parentStr != "" {
		parentStr = strings.TrimSpace(parentStr)
		err := c.AddDependency(ctx, issue.ID, core.AddDependencyRequest{
			DependsOn: parentStr,
			Kind:      "parent",
			Actor:     actor,
		})
		if err != nil {
			fmt.Printf("Warning: failed to set parent %s: %v\n", parentStr, err)
		} else {
			fmt.Printf("Set parent: %s\n", parentStr)
		}
	}

	if artifactLink != "" {
		err := c.LinkArtifact(ctx, issue.ID, core.LinkArtifactRequest{
			Artifact: artifactLink,
			Relation: "implements",
		})
		if err != nil {
			fmt.Printf("Warning: failed to link artifact %s: %v\n", artifactLink, err)
		} else {
			fmt.Printf("Linked artifact: %s\n", artifactLink)
		}
	}

	return nil
}
