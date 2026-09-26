package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/abevz/dibs/internal/client"
	"github.com/abevz/dibs/internal/core"
	"github.com/abevz/dibs/internal/firstuse"
	"github.com/abevz/dibs/internal/github"
	"github.com/google/uuid"
)

const issueImportUsage = "Usage: dibs issue import <url|owner/repo#n> [--project <key>] [--repo <name>] [--scope-kind project|repository] [--type <type>] [--priority <n>] [--acceptance <text>] [--tag <namespace/value>]... [--allow-closed]"

type importOptions struct {
	Project, Repo, ScopeKind, IssueType, Acceptance string
	Priority                                        int
	Tags                                            []string
	AllowClosed                                     bool
}

type importResult struct {
	Issue     core.Issue `json:"issue"`
	Imported  bool       `json:"imported"`
	SourceURL string     `json:"source_url"`
}

func importOperationID(projectID, externalKey string) string {
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte("dibs:issue-import:"+projectID+":"+externalKey)).String()
}

func runIssueImport(ctx context.Context, c *client.Client, args []string) error {
	if hasHelpFlag(args) {
		fmt.Println(issueImportUsage)
		return nil
	}
	result, ref, err := importIssue(ctx, c, github.CLI{}, args)
	if err != nil {
		return err
	}
	if jsonOutput {
		return json.NewEncoder(os.Stdout).Encode(result)
	}
	if result.Imported {
		fmt.Printf("Imported %s as %s\n", ref, result.Issue.ShortID)
	} else {
		fmt.Printf("Already imported as %s (%s)\n", result.Issue.ShortID, result.Issue.Status)
	}
	return nil
}

func parseImportArgs(args []string) (github.IssueRef, importOptions, error) {
	if len(args) == 0 {
		return github.IssueRef{}, importOptions{}, usageErr(issueImportUsage, "source issue is required")
	}
	ref, err := github.ParseIssueRef(args[0])
	if err != nil {
		return github.IssueRef{}, importOptions{}, usageErr(issueImportUsage, err.Error())
	}
	var opts importOptions
	for i := 1; i < len(args); i++ {
		if args[i] == "--allow-closed" {
			opts.AllowClosed = true
			continue
		}
		if i+1 >= len(args) || strings.HasPrefix(args[i+1], "--") {
			return github.IssueRef{}, importOptions{}, usageErr(issueImportUsage, args[i]+" requires a value")
		}
		value := args[i+1]
		switch args[i] {
		case "--project":
			opts.Project = value
		case "--repo":
			opts.Repo = value
		case "--scope-kind":
			opts.ScopeKind = value
		case "--type":
			opts.IssueType = value
		case "--priority":
			opts.Priority, err = strconv.Atoi(value)
			if err != nil || opts.Priority < 0 {
				return github.IssueRef{}, importOptions{}, usageErr(issueImportUsage, "--priority requires a non-negative integer")
			}
		case "--acceptance":
			opts.Acceptance = value
		case "--tag":
			opts.Tags = append(opts.Tags, value)
		default:
			return github.IssueRef{}, importOptions{}, usageErr(issueImportUsage, "unknown flag: "+args[i])
		}
		i++
	}
	if opts.ScopeKind != "" && opts.ScopeKind != "project" && opts.ScopeKind != "repository" {
		return github.IssueRef{}, importOptions{}, usageErr(issueImportUsage, "--scope-kind must be project or repository")
	}
	return ref, opts, nil
}

// resolveImportTarget uses the same registered canonical Git directory that
// dibs init stores. Explicit project/repo flags select the target directly.
func resolveImportTarget(ctx context.Context, c *client.Client, opts importOptions) (core.Project, string, string, error) {
	projects, err := c.ListProjects(ctx)
	if err != nil {
		return core.Project{}, "", "", err
	}
	repos, err := c.ListRepos(ctx, "")
	if err != nil {
		return core.Project{}, "", "", err
	}
	var project core.Project
	var repo core.Repository
	if opts.Project == "" {
		git := exec.CommandContext(ctx, "git", "rev-parse", "--path-format=absolute", "--git-common-dir")
		out, err := git.Output()
		if err != nil {
			return project, "", "", fmt.Errorf("cannot identify the current registered Git repository; pass --project: %w", err)
		}
		gitDir, err := filepath.EvalSymlinks(strings.TrimSpace(string(out)))
		if err != nil {
			return project, "", "", fmt.Errorf("cannot resolve Git common directory; pass --project: %w", err)
		}
		for _, candidate := range repos {
			if firstuse.SameRegisteredGitDir(ctx, candidate.CanonicalGitDir, gitDir) {
				repo = candidate
				break
			}
		}
		if repo.ID == "" {
			return project, "", "", fmt.Errorf("current Git repository is not registered; run dibs init or pass --project")
		}
		for _, candidate := range projects {
			if candidate.ID == repo.ProjectID {
				project = candidate
				break
			}
		}
		if project.ID == "" {
			return project, "", "", fmt.Errorf("registered repository has no project; pass --project")
		}
		if opts.Repo != "" && opts.Repo != repo.LogicalName && opts.Repo != repo.ID {
			return project, "", "", fmt.Errorf("--repo %q differs from the current registered repository %q", opts.Repo, repo.LogicalName)
		}
		if opts.ScopeKind == "project" {
			return project, "", "", fmt.Errorf("implicit --project always uses repository scope; pass --project explicitly for project scope")
		}
		return project, repo.LogicalName, "repository", nil
	}
	for _, candidate := range projects {
		if candidate.Key == opts.Project || candidate.ID == opts.Project {
			project = candidate
			break
		}
	}
	if project.ID == "" {
		return project, "", "", fmt.Errorf("project %q is not registered", opts.Project)
	}
	if opts.Repo == "" {
		if opts.ScopeKind == "repository" {
			return project, "", "", fmt.Errorf("--repo is required for repository scope")
		}
		return project, "", "project", nil
	}
	if opts.ScopeKind == "project" {
		return project, "", "", fmt.Errorf("--repo cannot be combined with project scope")
	}
	for _, candidate := range repos {
		if candidate.ProjectID == project.ID && (candidate.LogicalName == opts.Repo || candidate.ID == opts.Repo) {
			return project, candidate.LogicalName, "repository", nil
		}
	}
	return project, "", "", fmt.Errorf("repository %q is not registered in project %s", opts.Repo, project.Key)
}

func importIssue(ctx context.Context, c *client.Client, gh github.Client, args []string) (importResult, github.IssueRef, error) {
	ref, opts, err := parseImportArgs(args)
	if err != nil {
		return importResult{}, ref, err
	}
	project, repoName, scope, err := resolveImportTarget(ctx, c, opts)
	if err != nil {
		return importResult{}, ref, err
	}
	key := ref.ExternalKey()
	sourceURL := fmt.Sprintf("https://github.com/%s/%s/issues/%d", ref.Owner, ref.Repo, ref.Number)
	lookup := func() (core.Issue, bool, error) {
		issues, err := c.ListIssuesWithFilters(ctx, core.IssueListParams{Project: project.Key, ExternalKey: key})
		if err != nil {
			return core.Issue{}, false, err
		}
		if len(issues) == 0 {
			return core.Issue{}, false, nil
		}
		oldest := issues[0]
		for _, issue := range issues[1:] {
			if issue.CreatedAt < oldest.CreatedAt || (issue.CreatedAt == oldest.CreatedAt && issue.ID < oldest.ID) {
				oldest = issue
			}
		}
		return oldest, true, nil
	}
	if issue, ok, err := lookup(); err != nil {
		return importResult{}, ref, err
	} else if ok {
		return importResult{Issue: issue, SourceURL: sourceURL}, ref, nil
	}
	source, err := gh.GetIssue(ctx, ref)
	if err != nil {
		return importResult{}, ref, err
	}
	if source.IsPullRequest() {
		return importResult{}, ref, fmt.Errorf("GitHub pull requests cannot be imported; use an issue URL")
	}
	if source.State == "closed" && !opts.AllowClosed {
		return importResult{}, ref, fmt.Errorf("GitHub issue is closed; pass --allow-closed to import it")
	}
	actor, err := resolveActor("")
	if err != nil {
		return importResult{}, ref, err
	}
	req := core.CreateIssueRequest{
		Project: project.Key, ScopeKind: scope, Repo: repoName,
		IssueType: opts.IssueType, Priority: opts.Priority, Tags: opts.Tags,
		Title: source.Title, ExternalKey: key,
		Description:        "Source: " + source.HTMLURL + "\n\n" + source.Body,
		AcceptanceCriteria: opts.Acceptance, Actor: actor,
		OperationID: importOperationID(project.ID, key),
	}
	issue, err := c.CreateIssue(ctx, req)
	if err != nil {
		var clientErr *client.ClientError
		if errors.As(err, &clientErr) && (clientErr.Code == "idempotency_conflict" || clientErr.Code == "conflict") {
			if existing, ok, lookupErr := lookup(); lookupErr == nil && ok {
				return importResult{Issue: existing, SourceURL: sourceURL}, ref, nil
			}
		}
		return importResult{}, ref, err
	}
	return importResult{Issue: issue, Imported: true, SourceURL: source.HTMLURL}, ref, nil
}
