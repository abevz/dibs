package sqlite

import (
	"context"
	"database/sql"
	"io"
	"time"

	"github.com/abevz/dibs/internal/core"
	coordinatorexport "github.com/abevz/dibs/internal/export"
)

// Store adapts the SQLite function set to the API-facing store boundary.
type Store struct {
	db *sql.DB
}

// NewStore returns a SQLite-backed CoordinatorStore implementation.
func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

func (s *Store) Ping(ctx context.Context) error {
	return s.db.PingContext(ctx)
}

func (s *Store) ExportJSONL(ctx context.Context, w io.Writer) error {
	return coordinatorexport.WriteJSONL(ctx, s, w)
}

func (s *Store) CreateProject(ctx context.Context, key, name, description string) (core.Project, error) {
	return CreateProject(ctx, s.db, key, name, description)
}

func (s *Store) GetProjectByKey(ctx context.Context, key string) (core.Project, error) {
	return GetProjectByKey(ctx, s.db, key)
}

func (s *Store) ListProjects(ctx context.Context) ([]core.Project, error) {
	return ListProjects(ctx, s.db)
}

func (s *Store) CreateRepo(ctx context.Context, projectKey string, req core.CreateRepoRequest) (core.Repository, []core.RepoRemote, error) {
	return CreateRepo(ctx, s.db, projectKey, req)
}

func (s *Store) GetRepo(ctx context.Context, idOrName string) (core.Repository, error) {
	return GetRepo(ctx, s.db, idOrName)
}

func (s *Store) GetRepoInProject(ctx context.Context, projectID, idOrName string) (core.Repository, error) {
	return GetRepoInProject(ctx, s.db, projectID, idOrName)
}

func (s *Store) ListRepos(ctx context.Context, projectID string) ([]core.Repository, error) {
	return ListRepos(ctx, s.db, projectID)
}

func (s *Store) ListReposByProjectKey(ctx context.Context, projectKey string) ([]core.Repository, error) {
	return ListReposByProjectKey(ctx, s.db, projectKey)
}

func (s *Store) UpsertWorktree(ctx context.Context, repoID string, req core.CreateWorktreeRequest) (core.Worktree, bool, error) {
	return UpsertWorktree(ctx, s.db, repoID, req)
}

func (s *Store) ListWorktrees(ctx context.Context, repoID string) ([]core.Worktree, error) {
	return ListWorktrees(ctx, s.db, repoID)
}

func (s *Store) DeleteWorktree(ctx context.Context, id string) (core.Worktree, error) {
	return DeleteWorktree(ctx, s.db, id)
}

func (s *Store) CreateArtifactRoot(ctx context.Context, repoID string, req core.CreateArtifactRootRequest) (core.ArtifactRoot, error) {
	return CreateArtifactRoot(ctx, s.db, repoID, req)
}

func (s *Store) ListArtifactRoots(ctx context.Context, repoID string) ([]core.ArtifactRoot, error) {
	return ListArtifactRoots(ctx, s.db, repoID)
}

func (s *Store) CreateArtifact(ctx context.Context, repoID string, req core.CreateArtifactRequest) (core.Artifact, error) {
	return CreateArtifact(ctx, s.db, repoID, req)
}

func (s *Store) ListArtifacts(ctx context.Context, repoID string) ([]core.Artifact, error) {
	return ListArtifacts(ctx, s.db, repoID)
}

func (s *Store) CreateIssue(ctx context.Context, projectKey string, req core.CreateIssueRequest) (core.Issue, error) {
	return CreateIssue(ctx, s.db, projectKey, req)
}

func (s *Store) ResolveIssueID(ctx context.Context, idOrShortID string) (string, error) {
	return ResolveIssueID(ctx, s.db, idOrShortID)
}

func (s *Store) GetIssue(ctx context.Context, id string) (core.Issue, *core.IssueLease, error) {
	return GetIssue(ctx, s.db, id)
}

func (s *Store) ListIssues(ctx context.Context, params core.IssueListParams) ([]core.Issue, error) {
	return ListIssues(ctx, s.db, params)
}

func (s *Store) ListReadyIssues(ctx context.Context, projectID, repoID string, tags []string) ([]core.Issue, error) {
	return ListReadyIssues(ctx, s.db, projectID, repoID, tags)
}

func (s *Store) ClaimIssue(ctx context.Context, issueID, holder string, ttlSeconds int) (core.ClaimResponse, error) {
	return ClaimIssue(ctx, s.db, issueID, holder, ttlSeconds)
}

func (s *Store) ClaimIssueWithSession(ctx context.Context, issueID, holder string, ttlSeconds int, sessionID string) (core.ClaimResponse, error) {
	return ClaimIssueWithSession(ctx, s.db, issueID, holder, ttlSeconds, sessionID)
}

func (s *Store) ClaimIssueWithMode(ctx context.Context, issueID, holder string, ttlSeconds int, sessionID, invocationMode string) (core.ClaimResponse, error) {
	return ClaimIssueWithMode(ctx, s.db, issueID, holder, ttlSeconds, sessionID, invocationMode)
}

func (s *Store) ClaimIssueWithOperation(ctx context.Context, issueID string, req core.ClaimRequest) (core.ClaimResponse, error) {
	return ClaimIssueWithOperation(ctx, s.db, issueID, req)
}

func (s *Store) HeartbeatLease(ctx context.Context, issueID, leaseToken string, leaseGeneration int64, ttlSeconds int, now time.Time) (string, error) {
	return HeartbeatLease(ctx, s.db, issueID, leaseToken, leaseGeneration, ttlSeconds, now)
}

func (s *Store) HeartbeatLeaseWithOperation(ctx context.Context, issueID string, req core.HeartbeatRequest, now time.Time) (string, error) {
	return HeartbeatLeaseWithOperation(ctx, s.db, issueID, req, now)
}

func (s *Store) ReleaseLease(ctx context.Context, issueID, leaseToken string, leaseGeneration int64, now time.Time) error {
	return ReleaseLease(ctx, s.db, issueID, leaseToken, leaseGeneration, now)
}

func (s *Store) ReleaseLeaseWithOperation(ctx context.Context, issueID string, req core.ReleaseRequest, now time.Time) error {
	return ReleaseLeaseWithOperation(ctx, s.db, issueID, req, now)
}

func (s *Store) HandoffLease(ctx context.Context, issueID string, req core.HandoffRequest) (core.HandoffResponse, error) {
	return HandoffLease(ctx, s.db, issueID, req)
}

func (s *Store) UpdateIssue(ctx context.Context, issueID string, req core.UpdateIssueRequest) (core.Issue, error) {
	return UpdateIssue(ctx, s.db, issueID, req)
}

func (s *Store) CloseIssue(ctx context.Context, issueID string, req core.CloseIssueRequest) (core.CloseIssueResult, error) {
	return CloseIssue(ctx, s.db, issueID, req)
}

func (s *Store) OperatorCloseIssue(ctx context.Context, issueID string, req core.OperatorCloseIssueRequest) (core.CloseIssueResult, error) {
	return OperatorCloseIssue(ctx, s.db, issueID, req)
}

func (s *Store) OperatorReopenIssue(ctx context.Context, issueID string, req core.OperatorReopenIssueRequest) (core.Issue, error) {
	return OperatorReopenIssue(ctx, s.db, issueID, req)
}

func (s *Store) OperatorReleaseIssue(ctx context.Context, issueID string, req core.OperatorReleaseIssueRequest) (core.Issue, error) {
	return OperatorReleaseIssue(ctx, s.db, issueID, req)
}

func (s *Store) AddDependency(ctx context.Context, issueID string, req core.AddDependencyRequest) error {
	return AddDependency(ctx, s.db, issueID, req)
}

func (s *Store) RemoveDependency(ctx context.Context, issueID, dependsOn, kind, actor string) error {
	return RemoveDependency(ctx, s.db, issueID, dependsOn, kind, actor)
}

func (s *Store) AddTag(ctx context.Context, issueID string, req core.AddTagRequest) error {
	return AddTag(ctx, s.db, issueID, req)
}

func (s *Store) RemoveTag(ctx context.Context, issueID, tag, actor string) error {
	return RemoveTag(ctx, s.db, issueID, tag, actor)
}

func (s *Store) LinkArtifact(ctx context.Context, issueID string, req core.LinkArtifactRequest) (string, error) {
	return LinkArtifact(ctx, s.db, issueID, req)
}

func (s *Store) UnlinkArtifact(ctx context.Context, issueID, artifact, relation, actor string) error {
	return UnlinkArtifact(ctx, s.db, issueID, artifact, relation, actor)
}

func (s *Store) ListIssueArtifacts(ctx context.Context, issueID string) ([]core.ArtifactRef, error) {
	return ListIssueArtifacts(ctx, s.db, issueID)
}

func (s *Store) CreateNote(ctx context.Context, issueID string, req core.CreateNoteRequest) (core.Note, error) {
	return CreateNote(ctx, s.db, issueID, req)
}

func (s *Store) ListNotes(ctx context.Context, issueID string) ([]core.Note, error) {
	return ListNotes(ctx, s.db, issueID)
}

func (s *Store) ListEvents(ctx context.Context, issueID string) ([]core.Event, error) {
	return ListEvents(ctx, s.db, issueID)
}

func (s *Store) ListGlobalEvents(ctx context.Context, since string, limit int) (core.EventPage, error) {
	return ListGlobalEvents(ctx, s.db, since, limit)
}
