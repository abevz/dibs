package export

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/abevz/dibs/internal/core"
)

// Record is a single JSONL envelope emitted by the export stream.
type Record struct {
	Type    string `json:"type"`
	Payload any    `json:"payload"`
}

// Dependency captures an exported dependency edge as a first-class record.
type Dependency struct {
	IssueID          string `json:"issue_id"`
	IssueShortID     string `json:"issue_short_id"`
	DependsOnID      string `json:"depends_on_id"`
	DependsOnShortID string `json:"depends_on_short_id"`
	Kind             string `json:"kind"`
	CreatedAt        string `json:"created_at"`
}

// Reference captures an exported issue-to-artifact link.
type Reference struct {
	IssueID       string `json:"issue_id"`
	IssueShortID  string `json:"issue_short_id"`
	ArtifactID    string `json:"artifact_id"`
	ArtifactPath  string `json:"artifact_path"`
	ArtifactKind  string `json:"artifact_kind"`
	Relation      string `json:"relation"`
	LinkCreatedAt string `json:"link_created_at"`
}

// Source provides the normalized records needed by WriteJSONL.
type Source interface {
	ListProjects(context.Context) ([]core.Project, error)
	ListRepos(ctx context.Context, projectID string) ([]core.Repository, error)
	ListRepoRemotes(context.Context) ([]core.RepoRemote, error)
	ListWorktrees(ctx context.Context, repoID string) ([]core.Worktree, error)
	ListArtifactRoots(ctx context.Context, repoID string) ([]core.ArtifactRoot, error)
	ListArtifacts(ctx context.Context, repoID string) ([]core.Artifact, error)
	ListIssues(ctx context.Context, params core.IssueListParams) ([]core.Issue, error)
	ListExportDependencies(context.Context) ([]Dependency, error)
	ListReferences(context.Context) ([]Reference, error)
	ListAllNotes(context.Context) ([]core.Note, error)
	ListAllEvents(context.Context) ([]core.Event, error)
}

// WriteJSONL streams a normalized JSONL export of coordinator state.
func WriteJSONL(ctx context.Context, source Source, w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)

	emit := func(recordType string, payload any) error {
		if err := enc.Encode(Record{Type: recordType, Payload: payload}); err != nil {
			return fmt.Errorf("encode %s record: %w", recordType, err)
		}
		return nil
	}

	projects, err := source.ListProjects(ctx)
	if err != nil {
		return fmt.Errorf("list projects: %w", err)
	}
	for _, project := range projects {
		if err := emit("project", project); err != nil {
			return err
		}
	}

	repos, err := source.ListRepos(ctx, "")
	if err != nil {
		return fmt.Errorf("list repositories: %w", err)
	}
	for _, repo := range repos {
		if err := emit("repository", repo); err != nil {
			return err
		}
	}

	remotes, err := source.ListRepoRemotes(ctx)
	if err != nil {
		return fmt.Errorf("list repo remotes: %w", err)
	}
	for _, remote := range remotes {
		if err := emit("repo_remote", remote); err != nil {
			return err
		}
	}

	worktrees, err := source.ListWorktrees(ctx, "")
	if err != nil {
		return fmt.Errorf("list worktrees: %w", err)
	}
	for _, worktree := range worktrees {
		if err := emit("worktree", worktree); err != nil {
			return err
		}
	}

	roots, err := source.ListArtifactRoots(ctx, "")
	if err != nil {
		return fmt.Errorf("list artifact roots: %w", err)
	}
	for _, root := range roots {
		if err := emit("artifact_root", root); err != nil {
			return err
		}
	}

	artifacts, err := source.ListArtifacts(ctx, "")
	if err != nil {
		return fmt.Errorf("list artifacts: %w", err)
	}
	for _, artifact := range artifacts {
		if err := emit("artifact", artifact); err != nil {
			return err
		}
	}

	issues, err := source.ListIssues(ctx, core.IssueListParams{})
	if err != nil {
		return fmt.Errorf("list issues: %w", err)
	}
	for _, issue := range issues {
		issue.Dependencies = nil
		if err := emit("issue", issue); err != nil {
			return err
		}
	}

	dependencies, err := source.ListExportDependencies(ctx)
	if err != nil {
		return fmt.Errorf("list dependencies: %w", err)
	}
	for _, dependency := range dependencies {
		if err := emit("dependency", dependency); err != nil {
			return err
		}
	}

	references, err := source.ListReferences(ctx)
	if err != nil {
		return fmt.Errorf("list references: %w", err)
	}
	for _, reference := range references {
		if err := emit("reference", reference); err != nil {
			return err
		}
	}

	notes, err := source.ListAllNotes(ctx)
	if err != nil {
		return fmt.Errorf("list notes: %w", err)
	}
	for _, note := range notes {
		if err := emit("note", note); err != nil {
			return err
		}
	}

	events, err := source.ListAllEvents(ctx)
	if err != nil {
		return fmt.Errorf("list events: %w", err)
	}
	for _, event := range events {
		if err := emit("event", event); err != nil {
			return err
		}
	}

	return nil
}
