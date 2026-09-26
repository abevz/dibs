package sqlite

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/abevz/dibs/internal/core"
	"github.com/google/uuid"
)

// CreateIssue inserts a new issue, allocating a short_id from the project's
// sequence. An operation ID replays the exact committed result.
func CreateIssue(ctx context.Context, db *sql.DB, projectKey string, req core.CreateIssueRequest) (core.Issue, error) {
	if err := core.ValidateOperationID(req.OperationID); err != nil {
		return core.Issue{}, core.NewAPIError(core.ErrValidationFailed, err.Error())
	}
	for attempt := 0; attempt < 2; attempt++ {
		issue, err := createIssueAttempt(ctx, db, projectKey, req)
		if errors.Is(err, errOperationRace) {
			continue
		}
		return issue, err
	}
	return core.Issue{}, core.NewAPIError(core.ErrConflict, "create raced a concurrent operation; retry")
}

func createIssueAttempt(ctx context.Context, db *sql.DB, projectKey string, req core.CreateIssueRequest) (core.Issue, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return core.Issue{}, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	fingerprint := core.OperationFingerprint(core.CreateFingerprintFields(projectKey, req))
	if req.OperationID != "" {
		rec, err := lookupOperation(ctx, tx, req.OperationID)
		if err != nil {
			return core.Issue{}, err
		}
		if rec != nil {
			if err := authorizeReplay(rec, core.OperationKindCreate, projectKey, fingerprint); err != nil {
				return core.Issue{}, err
			}
			return decodeCreateOutcome(rec)
		}
	}

	// Resolve references after replay lookup: a committed result remains
	// replayable even if its worktree was later unregistered.
	var projectID string
	var canonicalProjectKey string
	var seq int64
	err = tx.QueryRowContext(ctx, `SELECT id, key, next_issue_seq FROM projects WHERE key = ? OR id = ?
		ORDER BY CASE WHEN key = ? THEN 0 ELSE 1 END LIMIT 1`, projectKey, projectKey, projectKey).Scan(&projectID, &canonicalProjectKey, &seq)
	if err == sql.ErrNoRows {
		return core.Issue{}, core.NewAPIError(core.ErrNotFound, "project not found")
	}
	if err != nil {
		return core.Issue{}, fmt.Errorf("resolve project: %w", err)
	}
	var repoID, worktreeID interface{}
	if req.Repo != "" {
		resolved, err := resolveCreateRepo(ctx, tx, projectID, req.Repo)
		if err != nil {
			return core.Issue{}, fmt.Errorf("resolve repo: %w", err)
		}
		repoID = resolved
	}
	if req.Worktree != "" {
		var resolved string
		err := tx.QueryRowContext(ctx, `SELECT id FROM worktrees WHERE id = ?`, req.Worktree).Scan(&resolved)
		if err == sql.ErrNoRows {
			return core.Issue{}, core.NewAPIError(core.ErrNotFound, "worktree not found: "+req.Worktree)
		}
		if err != nil {
			return core.Issue{}, fmt.Errorf("resolve worktree: %w", err)
		}
		worktreeID = resolved
	}
	nowTime := time.Now().UTC()
	now := nowTime.Format(time.RFC3339)
	id := uuid.New().String()

	shortID := fmt.Sprintf("%s-%d", canonicalProjectKey, seq)

	status := "open"
	priority := req.Priority
	if priority <= 0 {
		priority = 3
	}
	issueType := req.IssueType
	if issueType == "" {
		issueType = "task"
	}

	_, err = tx.ExecContext(ctx,
		`INSERT INTO issues (id, short_id, project_id, repository_id, worktree_id, scope_kind,
		                    issue_type, title, external_key, description, acceptance_criteria, status, priority, assignee, version,
		                    claimed_at, closed_at, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, '', 1, NULL, NULL, ?, ?)`,
		id, shortID, projectID, repoID, worktreeID, req.ScopeKind,
		issueType, req.Title, req.ExternalKey, req.Description, req.AcceptanceCriteria, status, priority, now, now,
	)
	if err != nil {
		return core.Issue{}, fmt.Errorf("insert issue: %w", err)
	}

	// Increment the sequence.
	_, err = tx.ExecContext(ctx, `UPDATE projects SET next_issue_seq = ?, updated_at = ? WHERE id = ?`, seq+1, now, projectID)
	if err != nil {
		return core.Issue{}, fmt.Errorf("update next_issue_seq: %w", err)
	}

	for _, tag := range req.Tags {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO issue_tags (issue_id, tag, created_at) VALUES (?, ?, ?)`,
			id, tag, now,
		); err != nil {
			return core.Issue{}, fmt.Errorf("insert tag: %w", err)
		}
	}

	// Append event.
	eventPayload := map[string]any{
		"title":      req.Title,
		"scope_kind": req.ScopeKind,
	}
	if req.ExternalKey != "" {
		eventPayload["external_key"] = req.ExternalKey
	}
	if len(req.Tags) > 0 {
		eventPayload["tags"] = req.Tags
	}
	payloadBytes, err := json.Marshal(eventPayload)
	if err != nil {
		return core.Issue{}, fmt.Errorf("marshal event payload: %w", err)
	}
	_, err = tx.ExecContext(ctx,
		`INSERT INTO events (id, issue_id, actor, event_type, payload_json, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		uuid.New().String(), id, req.Actor, "issue_created", string(payloadBytes), now,
	)
	if err != nil {
		return core.Issue{}, fmt.Errorf("insert event: %w", err)
	}

	created := scanIssueRow(id, shortID, projectID, repoID, worktreeID, req.ScopeKind,
		issueType, req.Title, req.ExternalKey, req.Description, req.AcceptanceCriteria, status, priority, "", 1, "", "", "", "", now, now)
	if len(req.Tags) > 0 {
		created.Tags = append([]string(nil), req.Tags...)
		sort.Strings(created.Tags)
	}
	if req.OperationID != "" {
		if err := recordOperation(ctx, tx, req.OperationID, core.OperationKindCreate,
			projectKey, req.Actor, fingerprint, created, nowTime); err != nil {
			return core.Issue{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return core.Issue{}, fmt.Errorf("commit tx: %w", err)
	}
	return created, nil
}

func resolveCreateRepo(ctx context.Context, tx *sql.Tx, projectID, ref string) (string, error) {
	var id string
	err := tx.QueryRowContext(ctx, `SELECT id FROM repositories WHERE id = ?`, ref).Scan(&id)
	if err == nil {
		return id, nil
	}
	if err != sql.ErrNoRows {
		return "", err
	}
	rows, err := tx.QueryContext(ctx, `SELECT id FROM repositories WHERE project_id = ? AND logical_name = ? ORDER BY id LIMIT 2`, projectID, ref)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return "", err
		}
		return "", core.NewAPIError(core.ErrNotFound, "repository not found: "+ref)
	}
	if err := rows.Scan(&id); err != nil {
		return "", err
	}
	if rows.Next() {
		return "", core.NewAPIError(core.ErrValidationFailed, "repository name is ambiguous: "+ref)
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	return id, nil
}

// ResolveIssueID resolves an issue by either its UUID id or short_id, returning the UUID id.
func ResolveIssueID(ctx context.Context, db *sql.DB, idOrShortID string) (string, error) {
	return resolveIssueIDWith(ctx, db, idOrShortID)
}

// GetIssue retrieves an issue by ID (UUID), along with its optional active lease.
// Callers should use ResolveIssueID first to resolve short_id to UUID.
func GetIssue(ctx context.Context, db *sql.DB, id string) (core.Issue, *core.IssueLease, error) {
	now := time.Now().UTC().Format(time.RFC3339)

	row := db.QueryRowContext(ctx,
		`SELECT i.id, i.short_id, i.project_id, i.repository_id, i.worktree_id, i.scope_kind,
		        i.issue_type, i.title, i.external_key, i.description, i.acceptance_criteria, i.status, i.priority, i.assignee, i.version,
		        i.claimed_at, i.closed_at, i.created_at, i.updated_at,
		        COALESCE(l.holder, ''), COALESCE(l.expires_at, ''), COALESCE(l.session_id, '')
		 FROM issues i
		 LEFT JOIN leases l ON l.issue_id = i.id AND l.expires_at > ?
		 WHERE i.id = ?`, now, id,
	)
	issue, err := scanIssue(row)
	if err != nil {
		return core.Issue{}, nil, err
	}

	// Look up the lease (separate query for token/expiry detail).
	lease, err := getActiveLease(ctx, db, issue.ID)
	if err != nil {
		return core.Issue{}, nil, err
	}

	populated, err := populateDependencies(ctx, db, []core.Issue{issue})
	if err != nil {
		return core.Issue{}, nil, err
	}
	issue = populated[0]

	populated, err = populateTags(ctx, db, []core.Issue{issue})
	if err != nil {
		return core.Issue{}, nil, err
	}
	issue = populated[0]

	return issue, lease, nil
}

// ListIssues returns issues matching the given filters.
func ListIssues(ctx context.Context, db *sql.DB, params core.IssueListParams) ([]core.Issue, error) {
	var where []string
	args := []interface{}{time.Now().UTC().Format(time.RFC3339)}
	projectKeys := issueListFilterValues(params.Projects, params.Project)
	projectIDs := make([]string, 0, len(projectKeys))

	for _, projectKey := range projectKeys {
		proj, err := GetProjectByKey(ctx, db, projectKey)
		if err != nil {
			return nil, fmt.Errorf("resolve project: %w", err)
		}
		projectIDs = append(projectIDs, proj.ID)
	}
	if len(projectIDs) > 0 {
		appendIssueListInFilter(&where, &args, "i.project_id", projectIDs)
	}
	if params.Repo != "" {
		if len(projectIDs) > 1 {
			return nil, core.NewAPIError(core.ErrValidationFailed,
				"repo filter requires exactly one project")
		}
		projectID := ""
		if len(projectIDs) == 1 {
			projectID = projectIDs[0]
		}
		repo, err := GetRepoInProject(ctx, db, projectID, params.Repo)
		if err != nil {
			return nil, fmt.Errorf("resolve repo: %w", err)
		}
		where = append(where, "i.repository_id = ?")
		args = append(args, repo.ID)
	}
	if params.Worktree != "" {
		wt, err := GetWorktree(ctx, db, params.Worktree)
		if err != nil {
			return nil, fmt.Errorf("resolve worktree: %w", err)
		}
		where = append(where, "i.worktree_id = ?")
		args = append(args, wt.ID)
	}
	if statuses := issueListFilterValues(params.Statuses, params.Status); len(statuses) > 0 {
		appendIssueListInFilter(&where, &args, "i.status", statuses)
	}
	if params.Assignee != "" {
		where = append(where, "i.assignee = ?")
		args = append(args, params.Assignee)
	}
	if issueTypes := issueListFilterValues(params.IssueTypes, params.IssueType); len(issueTypes) > 0 {
		appendIssueListInFilter(&where, &args, "i.issue_type", issueTypes)
	}
	if params.ExternalKey != "" {
		where = append(where, "i.external_key = ?")
		args = append(args, params.ExternalKey)
	}
	for _, tag := range params.Tags {
		where = append(where, `EXISTS (SELECT 1 FROM issue_tags t WHERE t.issue_id = i.id AND t.tag = ?)`)
		args = append(args, tag)
	}

	query := `SELECT i.id, i.short_id, i.project_id, i.repository_id, i.worktree_id, i.scope_kind,
	                 i.issue_type, i.title, i.external_key, i.description, i.acceptance_criteria, i.status, i.priority, i.assignee, i.version,
	                 i.claimed_at, i.closed_at, i.created_at, i.updated_at,
	                 COALESCE(l.holder, ''), COALESCE(l.expires_at, ''), COALESCE(l.session_id, '')
	          FROM issues i
	          LEFT JOIN leases l ON l.issue_id = i.id AND l.expires_at > ?`
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY i.updated_at DESC, i.id ASC"
	if params.Limit < 0 || params.Offset < 0 || params.Limit > 1000 {
		return nil, core.NewAPIError(core.ErrValidationFailed, "invalid limit or offset")
	}
	if params.Limit > 0 || params.Offset > 0 {
		limit := params.Limit
		if limit == 0 {
			limit = -1
		}
		query += " LIMIT ? OFFSET ?"
		args = append(args, limit, params.Offset)
	}

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list issues: %w", err)
	}
	defer rows.Close()

	var issues []core.Issue
	for rows.Next() {
		issue, err := scanIssue(rows)
		if err != nil {
			return nil, err
		}
		issues = append(issues, issue)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate issues: %w", err)
	}
	if issues == nil {
		issues = []core.Issue{}
	} else {
		issues, err = populateDependencies(ctx, db, issues)
		if err != nil {
			return nil, err
		}
		issues, err = populateTags(ctx, db, issues)
		if err != nil {
			return nil, err
		}
	}
	return issues, nil
}

func issueListFilterValues(values []string, fallback string) []string {
	if len(values) > 0 {
		return values
	}
	if fallback == "" {
		return nil
	}
	return []string{fallback}
}

func appendIssueListInFilter(where *[]string, args *[]interface{}, column string, values []string) {
	placeholders := make([]string, len(values))
	for i, value := range values {
		placeholders[i] = "?"
		*args = append(*args, value)
	}
	*where = append(*where, column+" IN ("+strings.Join(placeholders, ", ")+")")
}

// readyIssueEligibilityPredicate is the shared executable-state contract used
// by both the advisory ready view and the authoritative claim transaction.
// Lease availability is checked separately because claim must distinguish an
// active owner from a dependency/status conflict without ever returning that
// owner's secret token.
const readyIssueEligibilityPredicate = `
	            i.status IN ('open', 'in_progress')
	            AND i.issue_type != 'epic'
	            AND NOT EXISTS (
	                SELECT 1 FROM dependencies d
	                JOIN issues blocker ON blocker.id = d.depends_on_issue_id
	                WHERE d.issue_id = i.id
	                  AND d.kind = 'blocks'
	                  AND blocker.status NOT IN ('done', 'cancelled')
	            )`

// ListReadyIssues returns issues that are actionable (not terminal), not currently leased,
// and not blocked by an unfinished blocks dependency. tags filters to
// issues carrying every listed tag (AND semantics).
func ListReadyIssues(ctx context.Context, db *sql.DB, projectID, repoID string, tags []string) ([]core.Issue, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	args := []interface{}{now}
	query := `SELECT i.id, i.short_id, i.project_id, i.repository_id, i.worktree_id, i.scope_kind,
	                 i.issue_type, i.title, i.external_key, i.description, i.acceptance_criteria, i.status, i.priority, i.assignee, i.version,
	                 i.claimed_at, i.closed_at, i.created_at, i.updated_at,
	                 COALESCE(l.holder, ''), COALESCE(l.expires_at, ''), COALESCE(l.session_id, '')
	          FROM issues i
	          LEFT JOIN leases l ON l.issue_id = i.id AND l.expires_at > ?
	          WHERE ` + readyIssueEligibilityPredicate + `
	            AND NOT EXISTS (SELECT 1 FROM leases active_lease WHERE active_lease.issue_id = i.id AND active_lease.expires_at > ?)`

	args = append(args, now)

	if projectID != "" {
		query += " AND i.project_id = ?"
		args = append(args, projectID)
	}
	if repoID != "" {
		query += " AND i.repository_id = ?"
		args = append(args, repoID)
	}
	for _, tag := range tags {
		query += ` AND EXISTS (SELECT 1 FROM issue_tags t WHERE t.issue_id = i.id AND t.tag = ?)`
		args = append(args, tag)
	}
	query += " ORDER BY i.priority, i.updated_at DESC"

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list ready issues: %w", err)
	}
	defer rows.Close()

	var issues []core.Issue
	for rows.Next() {
		issue, err := scanIssue(rows)
		if err != nil {
			return nil, err
		}
		issues = append(issues, issue)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate ready issues: %w", err)
	}
	if issues == nil {
		issues = []core.Issue{}
	} else {
		issues, err = populateDependencies(ctx, db, issues)
		if err != nil {
			return nil, err
		}
		issues, err = populateTags(ctx, db, issues)
		if err != nil {
			return nil, err
		}
	}
	return issues, nil
}

// ClaimIssue acquires a lease without session correlation for compatibility.
func ClaimIssue(ctx context.Context, db *sql.DB, issueID, holder string, ttlSeconds int) (core.ClaimResponse, error) {
	return ClaimIssueWithSession(ctx, db, issueID, holder, ttlSeconds, "")
}

// ClaimIssueWithSession acquires a lease and creates the durable attempt
// identity used by lifecycle events. Session IDs are optional caller supplied
// correlation data; they do not affect holder or lease authorization.
func ClaimIssueWithSession(ctx context.Context, db *sql.DB, issueID, holder string, ttlSeconds int, sessionID string) (core.ClaimResponse, error) {
	return ClaimIssueWithMode(ctx, db, issueID, holder, ttlSeconds, sessionID, "")
}

// ClaimIssueWithMode acquires a lease like ClaimIssueWithSession and records
// the caller-declared invocation mode on the claim event. An empty value
// normalizes to the conservative "unknown" default; the mode is never
// inferred from the process tree.
func ClaimIssueWithMode(ctx context.Context, db *sql.DB, issueID, holder string, ttlSeconds int, sessionID, invocationMode string) (core.ClaimResponse, error) {
	return ClaimIssueWithOperation(ctx, db, issueID, core.ClaimRequest{
		Holder:         holder,
		TTLSeconds:     ttlSeconds,
		SessionID:      sessionID,
		InvocationMode: invocationMode,
	})
}

// ClaimIssueWithOperation is the full claim entry point. When req.OperationID
// is set, the claim participates in the durable idempotency ledger
// (AFC-SDD-0159): the committed outcome is recorded in the claim's own
// transaction, and an exact retry of the same operation returns that stored
// outcome instead of claiming again.
//
// This is operation retry, not lease recovery. It answers "what did MY
// committed operation do?" using a secret the caller generated before it ever
// sent the request. It never answers "who owns this lease?", so it does not
// reintroduce the holder-only reattach removed by AFC-SDD-0154: a caller
// without the original operation_id gets normal lease_held behavior no matter
// what holder or session_id it presents.
//
// An empty OperationID preserves pre-ledger behavior exactly.
func ClaimIssueWithOperation(ctx context.Context, db *sql.DB, issueID string, req core.ClaimRequest) (core.ClaimResponse, error) {
	if err := core.ValidateOperationID(req.OperationID); err != nil {
		return core.ClaimResponse{}, core.NewAPIError(core.ErrValidationFailed, err.Error())
	}

	// A lost race against a concurrent transaction using the same operation_id
	// is resolved by retrying once: the second attempt's ledger lookup sees the
	// winner's committed row and replays it. Only one outcome can ever exist,
	// so this loop cannot claim twice.
	for attempt := 0; attempt < 2; attempt++ {
		resp, err := claimIssueAttempt(ctx, db, issueID, req)
		if errors.Is(err, errOperationRace) {
			continue
		}
		return resp, err
	}
	return core.ClaimResponse{}, core.NewAPIError(core.ErrConflict,
		"claim raced a concurrent operation; retry")
}

func claimIssueAttempt(ctx context.Context, db *sql.DB, issueID string, req core.ClaimRequest) (core.ClaimResponse, error) {
	holder := req.Holder
	ttlSeconds := req.TTLSeconds
	sessionID := req.SessionID

	invocationMode, err := core.NormalizeInvocationMode(req.InvocationMode)
	if err != nil {
		return core.ClaimResponse{}, core.NewAPIError(core.ErrValidationFailed, err.Error())
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return core.ClaimResponse{}, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	fingerprint := core.OperationFingerprint(core.ClaimFingerprintFields(issueID, req, invocationMode))

	// Replay check runs before any state validation. A committed operation's
	// outcome must still be retrievable after the lease was released or the
	// issue closed, because the caller is asking about its own past commit.
	if req.OperationID != "" {
		rec, err := lookupOperation(ctx, tx, req.OperationID)
		if err != nil {
			return core.ClaimResponse{}, err
		}
		if rec != nil {
			if err := authorizeReplay(rec, core.OperationKindClaim, issueID, fingerprint); err != nil {
				return core.ClaimResponse{}, err
			}
			return decodeClaimOutcome(rec)
		}
	}

	// Check issue exists and is open.
	var status, issueType string
	var version int
	var currentGeneration int64
	err = tx.QueryRowContext(ctx, `SELECT status, issue_type, version, lease_generation FROM issues WHERE id = ?`, issueID).Scan(&status, &issueType, &version, &currentGeneration)
	if err == sql.ErrNoRows {
		return core.ClaimResponse{}, core.NewAPIError(core.ErrNotFound, "issue not found: "+issueID)
	}
	if err != nil {
		return core.ClaimResponse{}, fmt.Errorf("select issue: %w", err)
	}
	if issueType == "epic" {
		return core.ClaimResponse{}, core.NewAPIError(core.ErrValidationFailed,
			"epics cannot be claimed; claim their child issues instead")
	}
	if status != "open" && status != "in_progress" {
		return core.ClaimResponse{}, core.NewAPIError(core.ErrConflict,
			"issue cannot be claimed from status: "+status)
	}

	now := time.Now().UTC()
	nowStr := now.Format(time.RFC3339)

	// Holder is attribution, not authentication. An active lease always wins;
	// claim never reads or returns its token, even when the caller repeats the
	// same holder string. Retry-safe recovery belongs to the operation ledger.
	var activeLease int
	err = tx.QueryRowContext(ctx,
		`SELECT 1 FROM leases WHERE issue_id = ? AND expires_at > ?`,
		issueID, nowStr,
	).Scan(&activeLease)
	if err != nil && err != sql.ErrNoRows {
		return core.ClaimResponse{}, fmt.Errorf("check lease: %w", err)
	}
	if err == nil {
		return core.ClaimResponse{}, core.NewAPIError(core.ErrLeaseHeld,
			"issue is already claimed: "+issueID)
	}

	// Re-evaluate the exact ready eligibility predicate inside the claim
	// transaction. Ready-list output is advisory and may be stale immediately;
	// this check is authoritative for dependencies and executable state.
	var eligible int
	err = tx.QueryRowContext(ctx,
		`SELECT EXISTS (SELECT 1 FROM issues i WHERE i.id = ? AND `+readyIssueEligibilityPredicate+`)`,
		issueID,
	).Scan(&eligible)
	if err != nil {
		return core.ClaimResponse{}, fmt.Errorf("check claim eligibility: %w", err)
	}
	if eligible != 1 {
		return core.ClaimResponse{}, core.NewAPIError(core.ErrIssueNotReady,
			"issue is not ready: unfinished blocks dependency")
	}

	// A row that did not pass the active-lease query is an expired attempt.
	// Record its terminal outcome before replacing it, so the event stream
	// remains the complete attempt history.
	var expiredAttemptID, expiredSessionID, expiredAt, expiredLastHeartbeat string
	var expiredGeneration int64
	var expiredHeartbeats int64
	err = tx.QueryRowContext(ctx,
		`SELECT attempt_id, session_id, expires_at, lease_generation, heartbeat_count, last_heartbeat_at FROM leases WHERE issue_id = ? AND expires_at <= ?`,
		issueID, nowStr,
	).Scan(&expiredAttemptID, &expiredSessionID, &expiredAt, &expiredGeneration, &expiredHeartbeats, &expiredLastHeartbeat)
	if err != nil && err != sql.ErrNoRows {
		return core.ClaimResponse{}, fmt.Errorf("select expired lease: %w", err)
	}
	if err == nil {
		payload := map[string]any{
			"attempt_id":        expiredAttemptID,
			"lease_generation":  expiredGeneration,
			"end_reason":        "expired",
			"expired_at":        expiredAt,
			"session_id":        expiredSessionID,
			"heartbeat_count":   expiredHeartbeats,
			"last_heartbeat_at": expiredLastHeartbeat,
		}
		if err := insertEvent(ctx, tx, issueID, "system", "lease_expired", payload, nowStr); err != nil {
			return core.ClaimResponse{}, err
		}
	}

	// Delete the expired attempt before inserting its replacement.
	_, err = tx.ExecContext(ctx, `DELETE FROM leases WHERE issue_id = ?`, issueID)
	if err != nil {
		return core.ClaimResponse{}, fmt.Errorf("delete expired lease: %w", err)
	}

	// Generate lease and a separate non-secret attempt ID. The lease token is
	// intentionally never written to events or public issue responses.
	leaseToken := uuid.New().String()
	attemptID := uuid.New().String()
	leaseGeneration := currentGeneration + 1
	expiresAt := now.Add(time.Duration(ttlSeconds) * time.Second).Format(time.RFC3339)

	_, err = tx.ExecContext(ctx,
		`INSERT INTO leases (issue_id, holder, lease_token, expires_at, attempt_id, session_id, lease_generation, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		issueID, holder, leaseToken, expiresAt, attemptID, sessionID, leaseGeneration, nowStr, nowStr,
	)
	if err != nil {
		// Constraint violation (PK on issue_id) means another claim won while
		// both deferred transactions were reading the same "open" state.
		if isSQLiteConstraintError(err) {
			// With an operation_id in play the winner may be this very
			// operation committing on another connection. Retry so the ledger
			// lookup can replay it rather than reporting a false conflict.
			if req.OperationID != "" {
				return core.ClaimResponse{}, errOperationRace
			}
			return core.ClaimResponse{}, core.NewAPIError(core.ErrLeaseHeld,
				"issue is already claimed: "+issueID)
		}
		return core.ClaimResponse{}, fmt.Errorf("insert lease: %w", err)
	}

	// Update issue status and version.
	_, err = tx.ExecContext(ctx,
		`UPDATE issues SET status = 'in_progress', claimed_at = ?, version = version + 1,
		 lease_generation = ?, updated_at = ? WHERE id = ?`,
		nowStr, leaseGeneration, nowStr, issueID,
	)
	if err != nil {
		return core.ClaimResponse{}, fmt.Errorf("update issue: %w", err)
	}

	payload := map[string]any{
		"attempt_id":       attemptID,
		"lease_generation": leaseGeneration,
		"ttl_seconds":      ttlSeconds,
		"expires_at":       expiresAt,
		"session_id":       sessionID,
		"invocation_mode":  invocationMode,
	}
	if err := insertEvent(ctx, tx, issueID, holder, "issue_claimed", payload, nowStr); err != nil {
		return core.ClaimResponse{}, err
	}

	resp := core.ClaimResponse{LeaseToken: leaseToken, LeaseGeneration: leaseGeneration, ExpiresAt: expiresAt, AttemptID: attemptID, Version: version + 1}

	// Same transaction as the lease insert, status change, and claim event:
	// the ledger row and the effect it describes commit together or not at all.
	if req.OperationID != "" {
		if err := recordOperation(ctx, tx, req.OperationID, core.OperationKindClaim,
			issueID, holder, fingerprint, resp, now); err != nil {
			return core.ClaimResponse{}, err
		}
	}

	runCoordinationProofHook(ctx, coordinationProofClaimBeforeCommit)
	if err := tx.Commit(); err != nil {
		return core.ClaimResponse{}, fmt.Errorf("commit tx: %w", err)
	}

	return resp, nil
}

// HeartbeatLease renews the current unexpired lease identified by issue_id,
// lease_token, and lease_generation. The renewal is a compare-and-swap: the
// UPDATE carries the full lease predicate (token, generation, unexpired) and
// must affect exactly one row, so a stale heartbeat can never resurrect a
// replaced or expired lease. now is the daemon-supplied UTC wall-clock
// boundary; the returned value is the stored new deadline.
func HeartbeatLease(ctx context.Context, db *sql.DB, issueID, leaseToken string, leaseGeneration int64, ttlSeconds int, now time.Time) (string, error) {
	return HeartbeatLeaseWithOperation(ctx, db, issueID, core.HeartbeatRequest{LeaseToken: leaseToken, LeaseGeneration: leaseGeneration, TTLSeconds: ttlSeconds}, now)
}

func HeartbeatLeaseWithOperation(ctx context.Context, db *sql.DB, issueID string, req core.HeartbeatRequest, now time.Time) (string, error) {
	if err := core.ValidateOperationID(req.OperationID); err != nil {
		return "", core.NewAPIError(core.ErrValidationFailed, err.Error())
	}
	for attempt := 0; attempt < 2; attempt++ {
		outcome, err := heartbeatLeaseAttempt(ctx, db, issueID, req, now)
		if errors.Is(err, errOperationRace) {
			continue
		}
		return outcome, err
	}
	return "", core.NewAPIError(core.ErrConflict, "heartbeat raced a concurrent operation; retry")
}

func heartbeatLeaseAttempt(ctx context.Context, db *sql.DB, issueID string, req core.HeartbeatRequest, now time.Time) (string, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	fingerprintReq := req
	fingerprintReq.OperationID = ""
	fingerprint := requestFingerprint(fingerprintReq)
	if outcome, found, err := replayOperation[string](ctx, tx, req.OperationID, core.OperationKindHeartbeat, issueID, fingerprint); err != nil || found {
		return outcome, err
	}

	nowStr := now.UTC().Format(time.RFC3339)
	newExpiresAt := now.UTC().Add(time.Duration(req.TTLSeconds) * time.Second).Format(time.RFC3339)

	result, err := tx.ExecContext(ctx,
		`UPDATE leases SET expires_at = ?, updated_at = ?,
		 heartbeat_count = heartbeat_count + 1, last_heartbeat_at = ?
		 WHERE issue_id = ? AND lease_token = ? AND lease_generation = ? AND expires_at > ?`,
		newExpiresAt, nowStr, nowStr, issueID, req.LeaseToken, req.LeaseGeneration, nowStr,
	)
	if err != nil {
		return "", fmt.Errorf("update lease: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return "", fmt.Errorf("heartbeat rows affected: %w", err)
	}
	if rows != 1 {
		return "", leaseOwnershipError(ctx, tx, issueID, req.LeaseToken, req.LeaseGeneration)
	}

	runCoordinationProofHook(ctx, coordinationProofHeartbeatBeforeCommit)
	if req.OperationID != "" {
		if err := recordOperation(ctx, tx, req.OperationID, core.OperationKindHeartbeat, issueID, "", fingerprint, newExpiresAt, now); err != nil {
			return "", err
		}
	}
	if err := tx.Commit(); err != nil {
		return "", fmt.Errorf("commit tx: %w", err)
	}
	return newExpiresAt, nil
}

// ReleaseLease releases the current unexpired lease and returns the issue to
// 'open' (unless blocked). The conditional delete plus affected-row checks
// make release a compare-and-swap: an expired, missing, or replaced lease
// cannot be released and cannot change issue/event state. The status/version
// transition, lease removal, and issue_released event commit atomically. now
// is the daemon-supplied UTC wall-clock boundary.
func ReleaseLease(ctx context.Context, db *sql.DB, issueID, leaseToken string, leaseGeneration int64, now time.Time) error {
	return ReleaseLeaseWithOperation(ctx, db, issueID, core.ReleaseRequest{LeaseToken: leaseToken, LeaseGeneration: leaseGeneration}, now)
}

func ReleaseLeaseWithOperation(ctx context.Context, db *sql.DB, issueID string, req core.ReleaseRequest, now time.Time) error {
	if err := core.ValidateOperationID(req.OperationID); err != nil {
		return core.NewAPIError(core.ErrValidationFailed, err.Error())
	}
	for attempt := 0; attempt < 2; attempt++ {
		err := releaseLeaseAttempt(ctx, db, issueID, req, now)
		if errors.Is(err, errOperationRace) {
			continue
		}
		return err
	}
	return core.NewAPIError(core.ErrConflict, "release raced a concurrent operation; retry")
}

func releaseLeaseAttempt(ctx context.Context, db *sql.DB, issueID string, req core.ReleaseRequest, now time.Time) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	fingerprintReq := req
	fingerprintReq.OperationID = ""
	fingerprint := requestFingerprint(fingerprintReq)
	if _, found, err := replayOperation[struct{}](ctx, tx, req.OperationID, core.OperationKindRelease, issueID, fingerprint); err != nil || found {
		return err
	}

	nowStr := now.UTC().Format(time.RFC3339)

	// Read the current attempt under the full lease predicate so the release
	// event references the holder/attempt/generation that the conditional
	// delete below actually removes.
	var holder, attemptID, lastHeartbeat string
	var storedGeneration int64
	var heartbeatCount int64
	err = tx.QueryRowContext(ctx,
		`SELECT holder, attempt_id, lease_generation, heartbeat_count, last_heartbeat_at FROM leases
		 WHERE issue_id = ? AND lease_token = ? AND lease_generation = ? AND expires_at > ?`,
		issueID, req.LeaseToken, req.LeaseGeneration, nowStr,
	).Scan(&holder, &attemptID, &storedGeneration, &heartbeatCount, &lastHeartbeat)
	if err == sql.ErrNoRows {
		return leaseOwnershipError(ctx, tx, issueID, req.LeaseToken, req.LeaseGeneration)
	}
	if err != nil {
		return fmt.Errorf("select active lease: %w", err)
	}

	// Conditional delete re-verifies ownership at write time; the affected-row
	// count closes any gap between the read above and this write.
	result, err := tx.ExecContext(ctx,
		`DELETE FROM leases WHERE issue_id = ? AND lease_token = ? AND lease_generation = ? AND expires_at > ?`,
		issueID, req.LeaseToken, req.LeaseGeneration, nowStr,
	)
	if err != nil {
		return fmt.Errorf("delete lease: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("release rows affected: %w", err)
	}
	if rows != 1 {
		return core.NewAPIError(core.ErrLeaseExpired, "active lease not found")
	}

	// Update issue status: in_progress -> open (unless blocked).
	issueResult, err := tx.ExecContext(ctx,
		`UPDATE issues SET
		     status = CASE WHEN status = 'blocked' THEN 'blocked' ELSE 'open' END,
		     claimed_at = NULL,
		     version = version + 1,
		     updated_at = ?
		 WHERE id = ?`,
		nowStr, issueID,
	)
	if err != nil {
		return fmt.Errorf("update issue: %w", err)
	}
	issueRows, err := issueResult.RowsAffected()
	if err != nil {
		return fmt.Errorf("release issue rows affected: %w", err)
	}
	if issueRows != 1 {
		return fmt.Errorf("update issue: expected 1 row affected, got %d", issueRows)
	}

	if err := insertEvent(ctx, tx, issueID, holder, "issue_released", map[string]any{
		"attempt_id":        attemptID,
		"lease_generation":  storedGeneration,
		"end_reason":        "released",
		"heartbeat_count":   heartbeatCount,
		"last_heartbeat_at": lastHeartbeat,
	}, nowStr); err != nil {
		return err
	}
	if req.OperationID != "" {
		if err := recordOperation(ctx, tx, req.OperationID, core.OperationKindRelease, issueID, holder, fingerprint, struct{}{}, now); err != nil {
			return err
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}

	return nil
}

// leaseOwnershipError maps a failed lease CAS to a typed ownership-loss error.
// The diagnostic read runs on the same transaction as the failed write so the
// diagnosis matches the write's snapshot. Heartbeat and release only authorize
// the current unexpired lease, so any other state means the presented
// ownership is no longer valid.
func leaseOwnershipError(ctx context.Context, tx *sql.Tx, issueID, leaseToken string, leaseGeneration int64) error {
	var storedGeneration int64
	var expiresAt string
	err := tx.QueryRowContext(ctx,
		`SELECT lease_generation, expires_at FROM leases WHERE issue_id = ? AND lease_token = ?`,
		issueID, leaseToken,
	).Scan(&storedGeneration, &expiresAt)
	if err == sql.ErrNoRows {
		return core.NewAPIError(core.ErrLeaseExpired, "lease not found or expired")
	}
	if err != nil {
		return fmt.Errorf("diagnostic lease read: %w", err)
	}
	if storedGeneration != leaseGeneration {
		return core.NewAPIError(core.ErrLeaseExpired, "lease ownership lost: generation mismatch")
	}
	return core.NewAPIError(core.ErrLeaseExpired, "lease has expired")
}

// leaseHeartbeatSummary reads only bounded, non-secret attempt evidence.
func leaseHeartbeatSummary(ctx context.Context, tx *sql.Tx, issueID string) (int64, string, error) {
	var count int64
	var last string
	if err := tx.QueryRowContext(ctx,
		`SELECT heartbeat_count, last_heartbeat_at FROM leases WHERE issue_id = ?`, issueID,
	).Scan(&count, &last); err != nil {
		return 0, "", fmt.Errorf("read lease heartbeat summary: %w", err)
	}
	return count, last, nil
}

// HandoffLease records a required HANDOFF note and releases its active lease
// in one transaction. The note is authored by the current lease holder, so a
// caller cannot separate the handoff evidence from its authorized owner.
func HandoffLease(ctx context.Context, db *sql.DB, issueID string, req core.HandoffRequest) (core.HandoffResponse, error) {
	if err := core.ValidateOperationID(req.OperationID); err != nil {
		return core.HandoffResponse{}, core.NewAPIError(core.ErrValidationFailed, err.Error())
	}
	for attempt := 0; attempt < 2; attempt++ {
		outcome, err := handoffLeaseAttempt(ctx, db, issueID, req)
		if errors.Is(err, errOperationRace) {
			continue
		}
		return outcome, err
	}
	return core.HandoffResponse{}, core.NewAPIError(core.ErrConflict, "handoff raced a concurrent operation; retry")
}

func handoffLeaseAttempt(ctx context.Context, db *sql.DB, issueID string, req core.HandoffRequest) (core.HandoffResponse, error) {
	if err := core.ValidateHandoffRequest(req); err != nil {
		return core.HandoffResponse{}, core.NewAPIError(core.ErrValidationFailed, err.Error())
	}
	if req.LeaseToken == "" {
		return core.HandoffResponse{}, core.NewAPIError(core.ErrValidationFailed, "lease_token is required")
	}

	invocationMode, err := core.NormalizeInvocationMode(req.InvocationMode)
	if err != nil {
		return core.HandoffResponse{}, core.NewAPIError(core.ErrValidationFailed, err.Error())
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return core.HandoffResponse{}, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	replayReq := req
	replayReq.OperationID = ""
	replayReq.InvocationMode = invocationMode
	fingerprint := requestFingerprint(replayReq)
	if outcome, found, err := replayOperation[core.HandoffResponse](ctx, tx, req.OperationID, core.OperationKindHandoff, issueID, fingerprint); err != nil || found {
		return outcome, err
	}

	nowTime := time.Now().UTC()
	now := nowTime.Format(time.RFC3339)
	var holder, attemptID, lastHeartbeat string
	var leaseGeneration int64
	var heartbeatCount int64
	err = tx.QueryRowContext(ctx,
		`SELECT holder, attempt_id, lease_generation, heartbeat_count, last_heartbeat_at FROM leases
		 WHERE issue_id = ? AND lease_token = ? AND lease_generation = ? AND expires_at > ?`,
		issueID, req.LeaseToken, req.LeaseGeneration, now,
	).Scan(&holder, &attemptID, &leaseGeneration, &heartbeatCount, &lastHeartbeat)
	if err == sql.ErrNoRows {
		return core.HandoffResponse{}, core.NewAPIError(core.ErrLeaseExpired, "active lease not found")
	}
	if err != nil {
		return core.HandoffResponse{}, fmt.Errorf("select active lease: %w", err)
	}

	note := core.Note{
		ID:        uuid.New().String(),
		IssueID:   issueID,
		Author:    holder,
		Body:      req.Note,
		CreatedAt: now,
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO notes (id, issue_id, author, body, created_at) VALUES (?, ?, ?, ?, ?)`,
		note.ID, note.IssueID, note.Author, note.Body, note.CreatedAt,
	); err != nil {
		return core.HandoffResponse{}, fmt.Errorf("insert handoff note: %w", err)
	}
	if err := insertEvent(ctx, tx, issueID, holder, "note_added", map[string]any{"invocation_mode": invocationMode}, now); err != nil {
		return core.HandoffResponse{}, err
	}

	result, err := tx.ExecContext(ctx,
		`DELETE FROM leases
		 WHERE issue_id = ? AND lease_token = ? AND lease_generation = ? AND expires_at > ?`,
		issueID, req.LeaseToken, req.LeaseGeneration, now)
	if err != nil {
		return core.HandoffResponse{}, fmt.Errorf("delete lease: %w", err)
	}
	if rows, err := result.RowsAffected(); err != nil {
		return core.HandoffResponse{}, fmt.Errorf("handoff lease rows affected: %w", err)
	} else if rows != 1 {
		return core.HandoffResponse{}, core.NewAPIError(core.ErrLeaseExpired, "active lease not found")
	}

	issueResult, err := tx.ExecContext(ctx,
		`UPDATE issues SET
		     status = CASE WHEN status = 'blocked' THEN 'blocked' ELSE 'open' END,
		     claimed_at = NULL,
		     version = version + 1,
		     updated_at = ?
		 WHERE id = ?`,
		now, issueID,
	)
	if err != nil {
		return core.HandoffResponse{}, fmt.Errorf("update issue: %w", err)
	}
	if rows, err := issueResult.RowsAffected(); err != nil {
		return core.HandoffResponse{}, fmt.Errorf("handoff issue rows affected: %w", err)
	} else if rows != 1 {
		return core.HandoffResponse{}, core.NewAPIError(core.ErrConflict, "issue changed while handing off")
	}
	if err := insertEvent(ctx, tx, issueID, holder, "issue_released", map[string]any{
		"attempt_id":        attemptID,
		"lease_generation":  leaseGeneration,
		"end_reason":        "handoff",
		"heartbeat_count":   heartbeatCount,
		"last_heartbeat_at": lastHeartbeat,
	}, now); err != nil {
		return core.HandoffResponse{}, err
	}

	runCoordinationProofHook(ctx, coordinationProofHandoffBeforeCommit)
	outcome := core.HandoffResponse{Note: note}
	if req.OperationID != "" {
		if err := recordOperation(ctx, tx, req.OperationID, core.OperationKindHandoff, issueID, holder, fingerprint, outcome, nowTime); err != nil {
			return core.HandoffResponse{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return core.HandoffResponse{}, fmt.Errorf("commit handoff: %w", err)
	}
	return outcome, nil
}

func getActiveLease(ctx context.Context, db *sql.DB, issueID string) (*core.IssueLease, error) {
	row := db.QueryRowContext(ctx,
		`SELECT holder, lease_token, lease_generation, expires_at, attempt_id, session_id FROM leases WHERE issue_id = ? AND expires_at > ?`,
		issueID, time.Now().UTC().Format(time.RFC3339),
	)
	var lease core.IssueLease
	err := row.Scan(&lease.Holder, &lease.LeaseToken, &lease.LeaseGeneration, &lease.ExpiresAt, &lease.AttemptID, &lease.SessionID)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scan lease: %w", err)
	}
	return &lease, nil
}

func scanIssue(s scanner) (core.Issue, error) {
	var i core.Issue
	var repoID, worktreeID, claimedAt, closedAt sql.NullString
	var holder, leaseExpiresAt, leaseSessionID sql.NullString
	err := s.Scan(&i.ID, &i.ShortID, &i.ProjectID, &repoID, &worktreeID,
		&i.ScopeKind, &i.IssueType, &i.Title, &i.ExternalKey, &i.Description, &i.AcceptanceCriteria, &i.Status, &i.Priority,
		&i.Assignee, &i.Version, &claimedAt, &closedAt,
		&i.CreatedAt, &i.UpdatedAt, &holder, &leaseExpiresAt, &leaseSessionID)
	if err != nil {
		if err == sql.ErrNoRows {
			return core.Issue{}, core.NewAPIError(core.ErrNotFound, "issue not found")
		}
		return core.Issue{}, fmt.Errorf("scan issue: %w", err)
	}
	if repoID.Valid {
		i.RepositoryID = repoID.String
	}
	if worktreeID.Valid {
		i.WorktreeID = worktreeID.String
	}
	if claimedAt.Valid {
		i.ClaimedAt = claimedAt.String
	}
	if closedAt.Valid {
		i.ClosedAt = closedAt.String
	}
	if holder.Valid {
		i.Holder = holder.String
	}
	if leaseSessionID.Valid {
		i.LeaseHost, i.LeasePID = parseIssueRunSessionID(leaseSessionID.String)
	}
	if leaseExpiresAt.Valid {
		i.LeaseExpiresAt = leaseExpiresAt.String
	}
	return i, nil
}

// parseIssueRunSessionID recognizes the dibs-run session convention. The
// caller supplies session_id, so parsed process metadata is unverified.
// Other session IDs (including ordinary manual and older claims) have no PID.
func parseIssueRunSessionID(sessionID string) (string, int) {
	const prefix = "dibs-run:v1:"
	if !strings.HasPrefix(sessionID, prefix) {
		return "", 0
	}
	host, pidText, ok := strings.Cut(sessionID[len(prefix):], ":")
	if !ok || len(host) == 0 || len(host) > 253 {
		return "", 0
	}
	for _, c := range host {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '.' || c == '-' || c == '_') {
			return "", 0
		}
	}
	pid, err := strconv.Atoi(pidText)
	if err != nil || pid <= 0 {
		return "", 0
	}
	return host, pid
}

// UpdateIssue updates an issue's mutable fields with optimistic concurrency.
func UpdateIssue(ctx context.Context, db *sql.DB, issueID string, req core.UpdateIssueRequest) (core.Issue, error) {
	if err := core.ValidateOperationID(req.OperationID); err != nil {
		return core.Issue{}, core.NewAPIError(core.ErrValidationFailed, err.Error())
	}
	for attempt := 0; attempt < 2; attempt++ {
		outcome, err := updateIssueAttempt(ctx, db, issueID, req)
		if errors.Is(err, errOperationRace) {
			continue
		}
		return outcome, err
	}
	return core.Issue{}, core.NewAPIError(core.ErrConflict, "update raced a concurrent operation; retry")
}

func updateIssueAttempt(ctx context.Context, db *sql.DB, issueID string, req core.UpdateIssueRequest) (core.Issue, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return core.Issue{}, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	replayReq := req
	replayReq.OperationID = ""
	fingerprint := requestFingerprint(replayReq)
	if outcome, found, err := replayOperation[core.Issue](ctx, tx, req.OperationID, core.OperationKindUpdate, issueID, fingerprint); err != nil || found {
		return outcome, err
	}

	// Read version, state, and lease ownership after the immediate transaction
	// begins. GetIssue intentionally hides expired lease rows, so using it here
	// would let a former owner update between expiry and reclaim.
	issue, err := getIssueForTerminalTransition(ctx, tx, issueID, req.ExpectedVersion)
	if err != nil {
		return core.Issue{}, err
	}

	var lease *core.IssueLease
	var storedLease core.IssueLease
	err = tx.QueryRowContext(ctx,
		`SELECT holder, lease_token, lease_generation, expires_at, attempt_id, session_id
		 FROM leases WHERE issue_id = ?`, issueID,
	).Scan(&storedLease.Holder, &storedLease.LeaseToken, &storedLease.LeaseGeneration,
		&storedLease.ExpiresAt, &storedLease.AttemptID, &storedLease.SessionID)
	if err != nil && err != sql.ErrNoRows {
		return core.Issue{}, fmt.Errorf("select update lease: %w", err)
	}
	if err == nil {
		lease = &storedLease
		if storedLease.LeaseToken != req.LeaseToken ||
			storedLease.LeaseGeneration != req.LeaseGeneration ||
			storedLease.ExpiresAt <= now {
			return core.Issue{}, core.NewAPIError(core.ErrLeaseExpired,
				"current unexpired lease ownership is required to update the issue")
		}
	}

	// Generic update is for metadata and non-terminal routing only. Closing and
	// reopening have dedicated authorization and audit paths.
	if req.Status != "" && req.Status != issue.Status {
		if isTerminalStatus(req.Status) {
			return core.Issue{}, core.NewAPIError(core.ErrValidationFailed,
				"terminal status changes require issue close or issue operator-close")
		}
		if isTerminalStatus(issue.Status) {
			return core.Issue{}, core.NewAPIError(core.ErrValidationFailed,
				"terminal issues require issue operator-reopen")
		}
		if err := core.ValidateStatusTransition(issue.Status, req.Status); err != nil {
			return core.Issue{}, err
		}
	}

	runCoordinationProofHook(ctx, coordinationProofUpdateAfterAuthorize)

	// Build dynamic SET clause for non-zero / non-empty fields.
	var sets []string
	var args []interface{}

	if req.Title != "" {
		sets = append(sets, "title = ?")
		args = append(args, req.Title)
	}
	if req.IssueType != "" {
		if !core.ValidIssueType(req.IssueType) {
			return core.Issue{}, core.NewAPIError(core.ErrValidationFailed,
				"issue_type must be one of: "+strings.Join(core.IssueTypes, ", "))
		}
		sets = append(sets, "issue_type = ?")
		args = append(args, req.IssueType)
	}
	if req.ExternalKey != "" {
		sets = append(sets, "external_key = ?")
		args = append(args, req.ExternalKey)
	}
	if req.Description != "" {
		sets = append(sets, "description = ?")
		args = append(args, req.Description)
	}
	if req.AcceptanceCriteria != "" {
		sets = append(sets, "acceptance_criteria = ?")
		args = append(args, req.AcceptanceCriteria)
	}
	if req.Priority > 0 {
		sets = append(sets, "priority = ?")
		args = append(args, req.Priority)
	}
	if req.Assignee != "" {
		sets = append(sets, "assignee = ?")
		args = append(args, req.Assignee)
	}
	if req.Status != "" {
		sets = append(sets, "status = ?")
		args = append(args, req.Status)
	}

	if len(sets) == 0 {
		return core.Issue{}, core.NewAPIError(core.ErrValidationFailed, "no fields to update")
	}

	sets = append(sets, "version = version + 1")
	sets = append(sets, "updated_at = ?")
	args = append(args, now)
	// Re-verify the version inside the transaction so a competing mutation
	// between the read above and this write yields one typed conflict instead
	// of a lost update.
	args = append(args, issueID, issue.Version)

	query := "UPDATE issues SET " + strings.Join(sets, ", ") + " WHERE id = ? AND version = ?"
	result, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return core.Issue{}, fmt.Errorf("update issue: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return core.Issue{}, fmt.Errorf("rows affected: %w", err)
	}
	if rows == 0 {
		return core.Issue{}, core.NewAPIError(core.ErrConflict,
			"issue changed while updating")
	}

	// Append event. Include "release" in the changed field list when applicable.
	changed := buildChangedFields(req)
	if req.ReleaseLease {
		changed = append(changed, "release")
	}
	eventPayload := map[string]interface{}{"changed": changed}
	if req.Status != "" && req.Status != issue.Status {
		eventPayload["from_status"] = issue.Status
		eventPayload["to_status"] = req.Status
	}
	payloadBytes, _ := json.Marshal(eventPayload)
	_, err = tx.ExecContext(ctx,
		`INSERT INTO events (id, issue_id, actor, event_type, payload_json, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		uuid.New().String(), issueID, req.Actor, "issue_updated", string(payloadBytes), now,
	)
	if err != nil {
		return core.Issue{}, fmt.Errorf("insert event: %w", err)
	}

	// If --release was requested, atomically release the lease within the same transaction.
	if req.ReleaseLease {
		if lease == nil {
			return core.Issue{}, core.NewAPIError(core.ErrValidationFailed,
				"no active lease to release")
		}
		heartbeatCount, lastHeartbeat, err := leaseHeartbeatSummary(ctx, tx, issueID)
		if err != nil {
			return core.Issue{}, err
		}

		// Re-verify the complete lease predicate at the release write and require
		// exactly one row, matching the standalone release contract.
		leaseResult, err := tx.ExecContext(ctx,
			`DELETE FROM leases
			 WHERE issue_id = ? AND lease_token = ? AND lease_generation = ? AND expires_at > ?`,
			issueID, req.LeaseToken, req.LeaseGeneration, now,
		)
		if err != nil {
			return core.Issue{}, fmt.Errorf("delete lease: %w", err)
		}
		leaseRows, err := leaseResult.RowsAffected()
		if err != nil {
			return core.Issue{}, fmt.Errorf("update release rows affected: %w", err)
		}
		if leaseRows != 1 {
			return core.Issue{}, core.NewAPIError(core.ErrLeaseExpired, "active lease not found")
		}

		// Update status to open (unless blocked) and clear claimed_at.
		// Version is already bumped by the metadata update above, so do not bump again.
		statusResult, err := tx.ExecContext(ctx,
			`UPDATE issues SET
			     status = CASE WHEN status = 'blocked' THEN 'blocked' ELSE 'open' END,
			     claimed_at = NULL,
			     updated_at = ?
			 WHERE id = ?`,
			now, issueID,
		)
		if err != nil {
			return core.Issue{}, fmt.Errorf("update issue status for release: %w", err)
		}
		statusRows, err := statusResult.RowsAffected()
		if err != nil {
			return core.Issue{}, fmt.Errorf("update release status rows affected: %w", err)
		}
		if statusRows != 1 {
			return core.Issue{}, core.NewAPIError(core.ErrConflict, "issue changed while releasing update lease")
		}

		if err := insertEvent(ctx, tx, issueID, lease.Holder, "issue_released", map[string]any{
			"attempt_id":        lease.AttemptID,
			"lease_generation":  lease.LeaseGeneration,
			"end_reason":        "released",
			"heartbeat_count":   heartbeatCount,
			"last_heartbeat_at": lastHeartbeat,
		}, now); err != nil {
			return core.Issue{}, err
		}
	}

	// Capture the public outcome before commit, while this transaction still
	// owns the exact version it wrote. A later update must not change replay.
	updated, err := issueOutcome(ctx, tx, issueID, now)
	if err != nil {
		return core.Issue{}, fmt.Errorf("read update outcome: %w", err)
	}
	if req.OperationID != "" {
		if err := recordOperation(ctx, tx, req.OperationID, core.OperationKindUpdate, issueID, req.Actor, fingerprint, updated, time.Now().UTC()); err != nil {
			return core.Issue{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return core.Issue{}, fmt.Errorf("commit tx: %w", err)
	}
	return updated, nil
}

func issueOutcome(ctx context.Context, tx *sql.Tx, issueID, now string) (core.Issue, error) {
	issue, err := scanIssue(tx.QueryRowContext(ctx,
		`SELECT i.id, i.short_id, i.project_id, i.repository_id, i.worktree_id, i.scope_kind,
		        i.issue_type, i.title, i.external_key, i.description, i.acceptance_criteria, i.status, i.priority, i.assignee, i.version,
		        i.claimed_at, i.closed_at, i.created_at, i.updated_at,
		        COALESCE(l.holder, ''), COALESCE(l.expires_at, ''), COALESCE(l.session_id, '')
		 FROM issues i LEFT JOIN leases l ON l.issue_id = i.id AND l.expires_at > ?
		 WHERE i.id = ?`, now, issueID))
	if err != nil {
		return core.Issue{}, err
	}
	withDependencies, err := populateDependencies(ctx, tx, []core.Issue{issue})
	if err != nil {
		return core.Issue{}, err
	}
	withTags, err := populateTags(ctx, tx, withDependencies)
	if err != nil {
		return core.Issue{}, err
	}
	return withTags[0], nil
}

// buildChangedFields returns the list of field names that were changed in the update request.
func buildChangedFields(req core.UpdateIssueRequest) []string {
	var changed []string
	if req.Title != "" {
		changed = append(changed, "title")
	}
	if req.IssueType != "" {
		changed = append(changed, "issue_type")
	}
	if req.ExternalKey != "" {
		changed = append(changed, "external_key")
	}
	if req.Description != "" {
		changed = append(changed, "description")
	}
	if req.AcceptanceCriteria != "" {
		changed = append(changed, "acceptance_criteria")
	}
	if req.Priority > 0 {
		changed = append(changed, "priority")
	}
	if req.Assignee != "" {
		changed = append(changed, "assignee")
	}
	if req.Status != "" {
		changed = append(changed, "status")
	}
	return changed
}

// CloseIssue closes a non-terminal issue through the agent path. The caller
// must prove it still owns an active lease for the exact issue version.
func CloseIssue(ctx context.Context, db *sql.DB, issueID string, req core.CloseIssueRequest) (core.CloseIssueResult, error) {
	if err := core.ValidateOperationID(req.OperationID); err != nil {
		return core.CloseIssueResult{}, core.NewAPIError(core.ErrValidationFailed, err.Error())
	}
	for attempt := 0; attempt < 2; attempt++ {
		outcome, err := closeIssueAttempt(ctx, db, issueID, req)
		if errors.Is(err, errOperationRace) {
			continue
		}
		return outcome, err
	}
	return core.CloseIssueResult{}, core.NewAPIError(core.ErrConflict, "close raced a concurrent operation; retry")
}

func closeIssueAttempt(ctx context.Context, db *sql.DB, issueID string, req core.CloseIssueRequest) (core.CloseIssueResult, error) {
	if err := validateResolution(req.Resolution); err != nil {
		return core.CloseIssueResult{}, err
	}

	invocationMode, err := core.NormalizeInvocationMode(req.InvocationMode)
	if err != nil {
		return core.CloseIssueResult{}, core.NewAPIError(core.ErrValidationFailed, err.Error())
	}

	nowTime := time.Now().UTC()
	now := nowTime.Format(time.RFC3339)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return core.CloseIssueResult{}, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	replayReq := req
	replayReq.OperationID = ""
	replayReq.InvocationMode = invocationMode
	fingerprint := requestFingerprint(replayReq)
	if outcome, found, err := replayOperation[core.CloseIssueResult](ctx, tx, req.OperationID, core.OperationKindClose, issueID, fingerprint); err != nil || found {
		return outcome, err
	}

	issue, err := getIssueForTerminalTransition(ctx, tx, issueID, req.ExpectedVersion)
	if err != nil {
		return core.CloseIssueResult{}, err
	}
	if isTerminalStatus(issue.Status) {
		return core.CloseIssueResult{}, core.NewAPIError(core.ErrValidationFailed,
			"issue is already terminal")
	}
	if err := validateTerminalTransition(issue.Status, req.Resolution); err != nil {
		return core.CloseIssueResult{}, err
	}

	var leaseToken, attemptID string
	var leaseGeneration int64
	err = tx.QueryRowContext(ctx,
		`SELECT lease_token, attempt_id, lease_generation FROM leases WHERE issue_id = ? AND expires_at > ?`,
		issueID, now,
	).Scan(&leaseToken, &attemptID, &leaseGeneration)
	if err == sql.ErrNoRows {
		return core.CloseIssueResult{}, core.NewAPIError(core.ErrLeaseExpired,
			"an active lease is required to close an issue")
	}
	if err != nil {
		return core.CloseIssueResult{}, fmt.Errorf("select lease: %w", err)
	}
	if leaseToken != req.LeaseToken {
		return core.CloseIssueResult{}, core.NewAPIError(core.ErrLeaseExpired,
			"lease_token does not match the active lease")
	}
	if leaseGeneration != req.LeaseGeneration {
		return core.CloseIssueResult{}, core.NewAPIError(core.ErrLeaseExpired,
			"lease_generation does not match the active lease")
	}
	heartbeatCount, lastHeartbeat, err := leaseHeartbeatSummary(ctx, tx, issueID)
	if err != nil {
		return core.CloseIssueResult{}, err
	}

	result, err := updateTerminalIssue(ctx, tx, issue, req.Resolution, now)
	if err != nil {
		return core.CloseIssueResult{}, err
	}
	result.Branch = req.Branch
	result.PRURL = req.PRURL
	result.CommitSHA = req.CommitSHA

	leaseResult, err := tx.ExecContext(ctx,
		`DELETE FROM leases
		 WHERE issue_id = ? AND lease_token = ? AND lease_generation = ? AND expires_at > ?`,
		issueID, req.LeaseToken, req.LeaseGeneration, now,
	)
	if err != nil {
		return core.CloseIssueResult{}, fmt.Errorf("delete lease: %w", err)
	}
	if rows, err := leaseResult.RowsAffected(); err != nil {
		return core.CloseIssueResult{}, fmt.Errorf("close lease rows affected: %w", err)
	} else if rows != 1 {
		return core.CloseIssueResult{}, core.NewAPIError(core.ErrLeaseExpired, "active lease not found")
	}

	// A close note is appended before its closing event so the audit stream has
	// a deterministic note-then-close order.
	if req.Note != "" {
		if err := insertCloseNote(ctx, tx, issueID, req.Actor, req.Note, now, invocationMode); err != nil {
			return core.CloseIssueResult{}, err
		}
	}

	payload := terminalEventPayload(issue.Status, req.Resolution, issue.ExternalKey)
	payload["invocation_mode"] = invocationMode
	payload["attempt_id"] = attemptID
	payload["lease_generation"] = leaseGeneration
	payload["end_reason"] = req.Resolution
	payload["heartbeat_count"] = heartbeatCount
	payload["last_heartbeat_at"] = lastHeartbeat
	if req.Branch != "" {
		payload["branch"] = req.Branch
	}
	if req.PRURL != "" {
		payload["pr_url"] = req.PRURL
	}
	if req.CommitSHA != "" {
		payload["commit_sha"] = req.CommitSHA
	}
	if err := insertEvent(ctx, tx, issueID, req.Actor, "issue_closed", payload, now); err != nil {
		return core.CloseIssueResult{}, err
	}
	if req.OperationID != "" {
		if err := recordOperation(ctx, tx, req.OperationID, core.OperationKindClose, issueID, req.Actor, fingerprint, result, nowTime); err != nil {
			return core.CloseIssueResult{}, err
		}
	}

	if err := tx.Commit(); err != nil {
		return core.CloseIssueResult{}, fmt.Errorf("commit tx: %w", err)
	}
	return result, nil
}

// OperatorCloseIssue closes unclaimable or administratively managed work on a
// distinct, tokenless local-operator path. It is deliberately separate from
// CloseIssue so it cannot be mistaken for agent lease authorization.
func OperatorCloseIssue(ctx context.Context, db *sql.DB, issueID string, req core.OperatorCloseIssueRequest) (core.CloseIssueResult, error) {
	if err := validateOperatorRequest(req.ExpectedVersion, req.Actor, req.Reason); err != nil {
		return core.CloseIssueResult{}, err
	}
	if err := validateResolution(req.Resolution); err != nil {
		return core.CloseIssueResult{}, err
	}

	invocationMode, err := core.NormalizeInvocationMode(req.InvocationMode)
	if err != nil {
		return core.CloseIssueResult{}, core.NewAPIError(core.ErrValidationFailed, err.Error())
	}

	now := time.Now().UTC().Format(time.RFC3339)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return core.CloseIssueResult{}, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	issue, err := getIssueForTerminalTransition(ctx, tx, issueID, req.ExpectedVersion)
	if err != nil {
		return core.CloseIssueResult{}, err
	}
	if isTerminalStatus(issue.Status) {
		return core.CloseIssueResult{}, core.NewAPIError(core.ErrValidationFailed,
			"issue is already terminal")
	}
	if err := validateTerminalTransition(issue.Status, req.Resolution); err != nil {
		return core.CloseIssueResult{}, err
	}

	result, err := updateTerminalIssue(ctx, tx, issue, req.Resolution, now)
	if err != nil {
		return core.CloseIssueResult{}, err
	}
	result.Branch = req.Branch
	result.PRURL = req.PRURL
	result.CommitSHA = req.CommitSHA

	var attemptID sql.NullString
	var leaseGeneration sql.NullInt64
	var heartbeatCount sql.NullInt64
	var lastHeartbeat sql.NullString
	if err := tx.QueryRowContext(ctx,
		`SELECT attempt_id, lease_generation, heartbeat_count, last_heartbeat_at FROM leases WHERE issue_id = ?`, issueID,
	).Scan(&attemptID, &leaseGeneration, &heartbeatCount, &lastHeartbeat); err != nil && err != sql.ErrNoRows {
		return core.CloseIssueResult{}, fmt.Errorf("select operator-close lease: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM leases WHERE issue_id = ?`, issueID); err != nil {
		return core.CloseIssueResult{}, fmt.Errorf("delete leases: %w", err)
	}

	// A close note is appended before its closing event so the audit stream has
	// a deterministic note-then-close order.
	if req.Note != "" {
		if err := insertCloseNote(ctx, tx, issueID, req.Actor, req.Note, now, invocationMode); err != nil {
			return core.CloseIssueResult{}, err
		}
	}

	payload := terminalEventPayload(issue.Status, req.Resolution, issue.ExternalKey)
	payload["invocation_mode"] = invocationMode
	payload["reason"] = req.Reason
	if attemptID.Valid {
		payload["attempt_id"] = attemptID.String
	}
	if leaseGeneration.Valid {
		payload["lease_generation"] = leaseGeneration.Int64
		payload["heartbeat_count"] = heartbeatCount.Int64
		payload["last_heartbeat_at"] = lastHeartbeat.String
	}
	if req.Branch != "" {
		payload["branch"] = req.Branch
	}
	if req.PRURL != "" {
		payload["pr_url"] = req.PRURL
	}
	if req.CommitSHA != "" {
		payload["commit_sha"] = req.CommitSHA
	}
	if err := insertEvent(ctx, tx, issueID, req.Actor, "issue_operator_closed", payload, now); err != nil {
		return core.CloseIssueResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return core.CloseIssueResult{}, fmt.Errorf("commit tx: %w", err)
	}
	return result, nil
}

// OperatorReopenIssue reopens done or cancelled work on the explicit local
// operator path. Generic metadata updates may not reopen terminal issues.
func OperatorReopenIssue(ctx context.Context, db *sql.DB, issueID string, req core.OperatorReopenIssueRequest) (core.Issue, error) {
	if err := validateOperatorRequest(req.ExpectedVersion, req.Actor, req.Reason); err != nil {
		return core.Issue{}, err
	}

	now := time.Now().UTC().Format(time.RFC3339)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return core.Issue{}, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	issue, err := getIssueForTerminalTransition(ctx, tx, issueID, req.ExpectedVersion)
	if err != nil {
		return core.Issue{}, err
	}
	if !isTerminalStatus(issue.Status) {
		return core.Issue{}, core.NewAPIError(core.ErrValidationFailed,
			"only terminal issues can be reopened")
	}

	result, err := tx.ExecContext(ctx,
		`UPDATE issues
		 SET status = 'open', closed_at = NULL, claimed_at = NULL, version = version + 1, updated_at = ?
		 WHERE id = ? AND version = ?`,
		now, issueID, req.ExpectedVersion,
	)
	if err != nil {
		return core.Issue{}, fmt.Errorf("reopen issue: %w", err)
	}
	if rows, err := result.RowsAffected(); err != nil {
		return core.Issue{}, fmt.Errorf("reopen rows affected: %w", err)
	} else if rows != 1 {
		return core.Issue{}, core.NewAPIError(core.ErrConflict, "issue changed while reopening")
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM leases WHERE issue_id = ?`, issueID); err != nil {
		return core.Issue{}, fmt.Errorf("delete leases: %w", err)
	}
	if err := insertEvent(ctx, tx, issueID, req.Actor, "issue_reopened", map[string]any{
		"from_status": issue.Status,
		"to_status":   "open",
		"reason":      req.Reason,
	}, now); err != nil {
		return core.Issue{}, err
	}
	if err := tx.Commit(); err != nil {
		return core.Issue{}, fmt.Errorf("commit tx: %w", err)
	}

	updated, _, err := GetIssue(ctx, db, issueID)
	if err != nil {
		return core.Issue{}, fmt.Errorf("re-read reopened issue: %w", err)
	}
	return updated, nil
}

// OperatorReleaseIssue force-clears an issue's lease without a lease token
// and returns it to open, on the explicit local operator path. It is the
// recovery path for a claim whose lease token was lost before its TTL
// naturally expired it: without this, the only way to unstick the issue
// before expiry was operator-close followed by operator-reopen. Unlike
// operator-close, it never touches resolution — the work is not considered
// done, just unstuck for someone (possibly the same agent) to reclaim.
func OperatorReleaseIssue(ctx context.Context, db *sql.DB, issueID string, req core.OperatorReleaseIssueRequest) (core.Issue, error) {
	if err := validateOperatorRequest(req.ExpectedVersion, req.Actor, req.Reason); err != nil {
		return core.Issue{}, err
	}

	now := time.Now().UTC().Format(time.RFC3339)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return core.Issue{}, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	issue, err := getIssueForTerminalTransition(ctx, tx, issueID, req.ExpectedVersion)
	if err != nil {
		return core.Issue{}, err
	}
	if issue.Status != "in_progress" {
		return core.Issue{}, core.NewAPIError(core.ErrValidationFailed,
			"only in_progress issues can be force-released; use operator-close for terminal resolution or operator-reopen for terminal issues")
	}

	result, err := tx.ExecContext(ctx,
		`UPDATE issues
		 SET status = 'open', claimed_at = NULL, version = version + 1, updated_at = ?
		 WHERE id = ? AND version = ?`,
		now, issueID, req.ExpectedVersion,
	)
	if err != nil {
		return core.Issue{}, fmt.Errorf("release issue: %w", err)
	}
	if rows, err := result.RowsAffected(); err != nil {
		return core.Issue{}, fmt.Errorf("release rows affected: %w", err)
	} else if rows != 1 {
		return core.Issue{}, core.NewAPIError(core.ErrConflict, "issue changed while releasing")
	}
	var attemptID string
	var leaseGeneration int64
	var heartbeatCount int64
	var lastHeartbeat string
	if err := tx.QueryRowContext(ctx,
		`SELECT attempt_id, lease_generation, heartbeat_count, last_heartbeat_at FROM leases WHERE issue_id = ?`, issueID,
	).Scan(&attemptID, &leaseGeneration, &heartbeatCount, &lastHeartbeat); err == sql.ErrNoRows {
		return core.Issue{}, core.NewAPIError(core.ErrLeaseExpired, "lease not found")
	} else if err != nil {
		return core.Issue{}, fmt.Errorf("select operator-release lease: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM leases WHERE issue_id = ?`, issueID); err != nil {
		return core.Issue{}, fmt.Errorf("delete leases: %w", err)
	}
	if err := insertEvent(ctx, tx, issueID, req.Actor, "issue_operator_released", map[string]any{
		"reason":            req.Reason,
		"attempt_id":        attemptID,
		"lease_generation":  leaseGeneration,
		"heartbeat_count":   heartbeatCount,
		"last_heartbeat_at": lastHeartbeat,
	}, now); err != nil {
		return core.Issue{}, err
	}
	if err := tx.Commit(); err != nil {
		return core.Issue{}, fmt.Errorf("commit tx: %w", err)
	}

	updated, _, err := GetIssue(ctx, db, issueID)
	if err != nil {
		return core.Issue{}, fmt.Errorf("re-read released issue: %w", err)
	}
	return updated, nil
}

func isTerminalStatus(status string) bool {
	return status == "done" || status == "cancelled"
}

func validateResolution(resolution string) error {
	if resolution != "done" && resolution != "cancelled" {
		return core.NewAPIError(core.ErrValidationFailed, "resolution must be 'done' or 'cancelled'")
	}
	return nil
}

func validateTerminalTransition(from, to string) error {
	if err := core.ValidateStatusTransition(from, to); err != nil {
		return core.NewAPIError(core.ErrValidationFailed, err.Error())
	}
	return nil
}

func validateOperatorRequest(expectedVersion int, actor, reason string) error {
	if expectedVersion <= 0 {
		return core.NewAPIError(core.ErrValidationFailed, "expected_version is required")
	}
	if strings.TrimSpace(actor) == "" {
		return core.NewAPIError(core.ErrValidationFailed, "actor is required")
	}
	if strings.TrimSpace(reason) == "" {
		return core.NewAPIError(core.ErrValidationFailed, "reason is required")
	}
	return nil
}

func getIssueForTerminalTransition(ctx context.Context, tx *sql.Tx, issueID string, expectedVersion int) (core.Issue, error) {
	var issue core.Issue
	err := tx.QueryRowContext(ctx,
		`SELECT id, status, external_key, version FROM issues WHERE id = ?`, issueID,
	).Scan(&issue.ID, &issue.Status, &issue.ExternalKey, &issue.Version)
	if err == sql.ErrNoRows {
		return core.Issue{}, core.NewAPIError(core.ErrNotFound, "issue not found: "+issueID)
	}
	if err != nil {
		return core.Issue{}, fmt.Errorf("select issue: %w", err)
	}
	if expectedVersion != issue.Version {
		return core.Issue{}, core.NewAPIError(core.ErrConflict,
			fmt.Sprintf("expected version %d, current version is %d", expectedVersion, issue.Version))
	}
	return issue, nil
}

func updateTerminalIssue(ctx context.Context, tx *sql.Tx, issue core.Issue, resolution, now string) (core.CloseIssueResult, error) {
	result, err := tx.ExecContext(ctx,
		`UPDATE issues
		 SET status = ?, closed_at = ?, version = version + 1, updated_at = ?
		 WHERE id = ? AND version = ?`,
		resolution, now, now, issue.ID, issue.Version,
	)
	if err != nil {
		return core.CloseIssueResult{}, fmt.Errorf("update issue: %w", err)
	}
	if rows, err := result.RowsAffected(); err != nil {
		return core.CloseIssueResult{}, fmt.Errorf("close rows affected: %w", err)
	} else if rows != 1 {
		return core.CloseIssueResult{}, core.NewAPIError(core.ErrConflict, "issue changed while closing")
	}
	return core.CloseIssueResult{
		Status:      "closed",
		Resolution:  resolution,
		ExternalKey: issue.ExternalKey,
		ClosedAt:    now,
	}, nil
}

func insertCloseNote(ctx context.Context, tx *sql.Tx, issueID, actor, note, now, invocationMode string) error {
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO notes (id, issue_id, author, body, created_at) VALUES (?, ?, ?, ?, ?)`,
		uuid.New().String(), issueID, actor, note, now,
	); err != nil {
		return fmt.Errorf("insert note: %w", err)
	}
	return insertEvent(ctx, tx, issueID, actor, "note_added", map[string]any{"invocation_mode": invocationMode}, now)
}

func terminalEventPayload(fromStatus, resolution, externalKey string) map[string]any {
	payload := map[string]any{
		"changed":     []string{"status", "closed_at"},
		"from_status": fromStatus,
		"to_status":   resolution,
		"resolution":  resolution,
	}
	if externalKey != "" {
		payload["external_key"] = externalKey
	}
	return payload
}

func insertEvent(ctx context.Context, tx *sql.Tx, issueID, actor, eventType string, payload map[string]any, now string) error {
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal %s payload: %w", eventType, err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO events (id, issue_id, actor, event_type, payload_json, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		uuid.New().String(), issueID, actor, eventType, string(payloadBytes), now,
	); err != nil {
		return fmt.Errorf("insert %s event: %w", eventType, err)
	}
	return nil
}

// dependencyQueryer is satisfied by both *sql.DB and *sql.Tx so dependency
// validation can run against the serialized-writer transaction.
type dependencyQueryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// AddDependency adds a dependency between two issues. Endpoint resolution, the
// cross-project endpoint policy, 'blocks' cycle detection, the edge insert, and
// the dependency_added event all run in one transaction on the serialized
// writer, so concurrent opposite edges cannot each validate against an
// incomplete graph.
func AddDependency(ctx context.Context, db *sql.DB, issueID string, req core.AddDependencyRequest) error {
	return addDependency(ctx, db, issueID, req, wouldCreateCycle)
}

// addDependency is AddDependency with an injectable cycle check so tests can
// force traversal failures without touching the production code path.
func addDependency(ctx context.Context, db *sql.DB, issueID string, req core.AddDependencyRequest, cycleCheck func(context.Context, dependencyQueryer, string, string) (bool, error)) error {
	kind := req.Kind
	if kind == "" {
		kind = "blocks"
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Resolve both endpoints and verify they exist inside the transaction.
	dependsOnID, err := resolveIssueIDWith(ctx, tx, req.DependsOn)
	if err != nil {
		return err
	}
	sourceProjectID, err := projectIDForIssue(ctx, tx, issueID)
	if err != nil {
		return err
	}
	targetProjectID, err := projectIDForIssue(ctx, tx, dependsOnID)
	if err != nil {
		return err
	}
	// Cross-project policy: a dependency edge must stay inside one project.
	if sourceProjectID != targetProjectID {
		return core.NewAPIError(core.ErrValidationFailed,
			"dependency endpoints must belong to the same project")
	}
	req.DependsOn = dependsOnID

	if kind == "blocks" {
		cycle, err := cycleCheck(ctx, tx, issueID, req.DependsOn)
		if err != nil {
			// Traversal errors are real failures, never "no cycle".
			return fmt.Errorf("traverse dependency graph: %w", err)
		}
		if cycle {
			return core.NewAPIError(core.ErrDependencyCycle,
				"adding this dependency would create a cycle")
		}
	}

	now := time.Now().UTC().Format(time.RFC3339)

	_, err = tx.ExecContext(ctx,
		`INSERT INTO dependencies (issue_id, depends_on_issue_id, kind, created_at)
		 VALUES (?, ?, ?, ?)`,
		issueID, req.DependsOn, kind, now,
	)
	if err != nil {
		// Handle duplicate / unique constraint.
		if isSQLiteConstraintError(err) {
			return core.NewAPIError(core.ErrConflict,
				"dependency already exists")
		}
		return fmt.Errorf("insert dependency: %w", err)
	}

	// Append event.
	_, err = tx.ExecContext(ctx,
		`INSERT INTO events (id, issue_id, actor, event_type, payload_json, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		uuid.New().String(), issueID, req.Actor, "dependency_added",
		fmt.Sprintf(`{"depends_on":"%s","kind":"%s"}`, req.DependsOn, kind), now,
	)
	if err != nil {
		return fmt.Errorf("insert event: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}

	return nil
}

// resolveIssueIDWith resolves an issue by UUID or short_id against a queryer
// (either the pooled *sql.DB or an in-flight transaction).
func resolveIssueIDWith(ctx context.Context, q dependencyQueryer, idOrShortID string) (string, error) {
	// Try by primary key first.
	var id string
	err := q.QueryRowContext(ctx, `SELECT id FROM issues WHERE id = ?`, idOrShortID).Scan(&id)
	if err == nil {
		return id, nil
	}
	if err != sql.ErrNoRows {
		return "", fmt.Errorf("resolve issue by id: %w", err)
	}

	// Fall back to short_id.
	err = q.QueryRowContext(ctx, `SELECT id FROM issues WHERE short_id = ?`, idOrShortID).Scan(&id)
	if err == sql.ErrNoRows {
		return "", core.NewAPIError(core.ErrNotFound, "issue not found: "+idOrShortID)
	}
	if err != nil {
		return "", fmt.Errorf("resolve issue by short_id: %w", err)
	}
	return id, nil
}

// projectIDForIssue returns the project_id for an issue, verifying it exists.
func projectIDForIssue(ctx context.Context, q dependencyQueryer, issueID string) (string, error) {
	var projectID string
	err := q.QueryRowContext(ctx, `SELECT project_id FROM issues WHERE id = ?`, issueID).Scan(&projectID)
	if err == sql.ErrNoRows {
		return "", core.NewAPIError(core.ErrNotFound, "issue not found: "+issueID)
	}
	if err != nil {
		return "", fmt.Errorf("resolve issue project: %w", err)
	}
	return projectID, nil
}

// RemoveDependency removes a dependency record.
func RemoveDependency(ctx context.Context, db *sql.DB, issueID, dependsOn, kind, actor string) error {
	if kind == "" {
		kind = "blocks"
	}

	dependsOnID, err := ResolveIssueID(ctx, db, dependsOn)
	if err != nil {
		return err
	}
	dependsOn = dependsOnID

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	result, err := tx.ExecContext(ctx,
		`DELETE FROM dependencies WHERE issue_id = ? AND depends_on_issue_id = ? AND kind = ?`,
		issueID, dependsOn, kind,
	)
	if err != nil {
		return fmt.Errorf("delete dependency: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}
	if rows == 0 {
		return core.NewAPIError(core.ErrNotFound, "dependency not found")
	}

	now := time.Now().UTC().Format(time.RFC3339)

	// Append event.
	_, err = tx.ExecContext(ctx,
		`INSERT INTO events (id, issue_id, actor, event_type, payload_json, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		uuid.New().String(), issueID, actor, "dependency_removed",
		fmt.Sprintf(`{"depends_on":"%s","kind":"%s"}`, dependsOn, kind), now,
	)
	if err != nil {
		return fmt.Errorf("insert event: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}

	return nil
}

// wouldCreateCycle reports whether adding a 'blocks' dependency from
// fromIssueID to toIssueID would create a cycle. It BFS-follows 'blocks' edges
// from toIssueID and returns true when it reaches fromIssueID. Traversal errors
// are returned to the caller, never interpreted as "no cycle".
func wouldCreateCycle(ctx context.Context, q dependencyQueryer, fromIssueID, toIssueID string) (bool, error) {
	visited := map[string]bool{}
	queue := []string{toIssueID}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		if current == fromIssueID {
			return true, nil // cycle!
		}
		if visited[current] {
			continue
		}
		visited[current] = true
		rows, err := q.QueryContext(ctx,
			`SELECT depends_on_issue_id FROM dependencies WHERE issue_id = ? AND kind = 'blocks'`, current)
		if err != nil {
			return false, err
		}
		for rows.Next() {
			var dep string
			if err := rows.Scan(&dep); err != nil {
				rows.Close()
				return false, err
			}
			if !visited[dep] {
				queue = append(queue, dep)
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return false, err
		}
		rows.Close()
	}
	return false, nil
}

// LinkArtifact links an artifact to an issue by inserting into issue_artifacts.
func LinkArtifact(ctx context.Context, db *sql.DB, issueID string, req core.LinkArtifactRequest) (string, error) {
	// Verify the issue exists to get its repository_id for path resolution.
	issue, _, err := GetIssue(ctx, db, issueID)
	if err != nil {
		return "", err
	}

	// Resolve artifact by ID or relative path.
	artifactID, err := ResolveArtifactID(ctx, db, issue.RepositoryID, req.Artifact)
	if err != nil {
		return "", err
	}

	relation := req.Relation
	if relation == "" {
		relation = "implements"
	}

	now := time.Now().UTC().Format(time.RFC3339)

	_, err = db.ExecContext(ctx,
		`INSERT INTO issue_artifacts (issue_id, artifact_id, relation, created_at)
		 VALUES (?, ?, ?, ?)`,
		issueID, artifactID, relation, now,
	)
	if err != nil {
		if isSQLiteConstraintError(err) {
			return "", core.NewAPIError(core.ErrAlreadyLinked,
				"issue is already linked to this artifact with relation '"+relation+"'")
		}
		return "", fmt.Errorf("insert issue_artifact: %w", err)
	}

	return now, nil
}

// ListIssueArtifacts returns all artifacts linked to an issue.
func ListIssueArtifacts(ctx context.Context, db *sql.DB, issueID string) ([]core.ArtifactRef, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT a.id, a.relative_path, a.kind, ia.relation
		 FROM issue_artifacts ia
		 JOIN artifacts a ON ia.artifact_id = a.id
		 WHERE ia.issue_id = ?
		 ORDER BY ia.created_at`,
		issueID,
	)
	if err != nil {
		return nil, fmt.Errorf("list issue artifacts: %w", err)
	}
	defer rows.Close()

	var refs []core.ArtifactRef
	for rows.Next() {
		var ref core.ArtifactRef
		if err := rows.Scan(&ref.ID, &ref.RelativePath, &ref.Kind, &ref.Relation); err != nil {
			return nil, fmt.Errorf("scan artifact ref: %w", err)
		}
		refs = append(refs, ref)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate artifact refs: %w", err)
	}
	if refs == nil {
		refs = []core.ArtifactRef{}
	}
	return refs, nil
}

// UnlinkArtifact removes an artifact link from an issue. When relation is empty
// it removes every relation to that artifact; otherwise only the matching one.
func UnlinkArtifact(ctx context.Context, db *sql.DB, issueID, artifact, relation, actor string) error {
	issue, _, err := GetIssue(ctx, db, issueID)
	if err != nil {
		return err
	}

	artifactID, err := ResolveArtifactID(ctx, db, issue.RepositoryID, artifact)
	if err != nil {
		return err
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var result sql.Result
	if relation != "" {
		result, err = tx.ExecContext(ctx,
			`DELETE FROM issue_artifacts WHERE issue_id = ? AND artifact_id = ? AND relation = ?`,
			issueID, artifactID, relation)
	} else {
		result, err = tx.ExecContext(ctx,
			`DELETE FROM issue_artifacts WHERE issue_id = ? AND artifact_id = ?`,
			issueID, artifactID)
	}
	if err != nil {
		return fmt.Errorf("delete issue_artifact: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}
	if rows == 0 {
		return core.NewAPIError(core.ErrNotFound, "artifact link not found")
	}

	payload, err := json.Marshal(map[string]string{
		"artifact": artifact,
		"relation": relation,
	})
	if err != nil {
		return fmt.Errorf("marshal event payload: %w", err)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	_, err = tx.ExecContext(ctx,
		`INSERT INTO events (id, issue_id, actor, event_type, payload_json, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		uuid.New().String(), issueID, actor, "artifact_unlinked",
		string(payload), now)
	if err != nil {
		return fmt.Errorf("insert event: %w", err)
	}

	return tx.Commit()
}

// CreateNote inserts a new note on an issue.
func CreateNote(ctx context.Context, db *sql.DB, issueID string, req core.CreateNoteRequest) (core.Note, error) {
	// Verify issue exists.
	if _, _, err := GetIssue(ctx, db, issueID); err != nil {
		return core.Note{}, err
	}

	invocationMode, err := core.NormalizeInvocationMode(req.InvocationMode)
	if err != nil {
		return core.Note{}, core.NewAPIError(core.ErrValidationFailed, err.Error())
	}

	now := time.Now().UTC().Format(time.RFC3339)
	id := uuid.New().String()

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return core.Note{}, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	_, err = tx.ExecContext(ctx,
		`INSERT INTO notes (id, issue_id, author, body, created_at)
		 VALUES (?, ?, ?, ?, ?)`,
		id, issueID, req.Author, req.Body, now,
	)
	if err != nil {
		return core.Note{}, fmt.Errorf("insert note: %w", err)
	}

	// Append event.
	notePayload, err := json.Marshal(map[string]string{"invocation_mode": invocationMode})
	if err != nil {
		return core.Note{}, fmt.Errorf("marshal note event payload: %w", err)
	}
	_, err = tx.ExecContext(ctx,
		`INSERT INTO events (id, issue_id, actor, event_type, payload_json, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		uuid.New().String(), issueID, req.Author, "note_added", string(notePayload), now,
	)
	if err != nil {
		return core.Note{}, fmt.Errorf("insert event: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return core.Note{}, fmt.Errorf("commit tx: %w", err)
	}

	return core.Note{
		ID:        id,
		IssueID:   issueID,
		Author:    req.Author,
		Body:      req.Body,
		CreatedAt: now,
	}, nil
}

// ListNotes returns all notes for an issue, ordered by creation time.
func ListNotes(ctx context.Context, db *sql.DB, issueID string) ([]core.Note, error) {
	// Verify issue exists.
	if _, _, err := GetIssue(ctx, db, issueID); err != nil {
		return nil, err
	}

	rows, err := db.QueryContext(ctx,
		`SELECT id, issue_id, author, body, created_at FROM notes WHERE issue_id = ? ORDER BY created_at ASC`,
		issueID,
	)
	if err != nil {
		return nil, fmt.Errorf("list notes: %w", err)
	}
	defer rows.Close()

	var notes []core.Note
	for rows.Next() {
		var n core.Note
		if err := rows.Scan(&n.ID, &n.IssueID, &n.Author, &n.Body, &n.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan note: %w", err)
		}
		notes = append(notes, n)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate notes: %w", err)
	}
	if notes == nil {
		notes = []core.Note{}
	}
	return notes, nil
}

// ListEvents returns all events for an issue, ordered by creation time.
func ListEvents(ctx context.Context, db *sql.DB, issueID string) ([]core.Event, error) {
	// Verify issue exists.
	if _, _, err := GetIssue(ctx, db, issueID); err != nil {
		return nil, err
	}

	rows, err := db.QueryContext(ctx,
		`SELECT sequence, id, issue_id, actor, event_type, payload_json, created_at FROM events WHERE issue_id = ? ORDER BY sequence ASC`,
		issueID,
	)
	if err != nil {
		return nil, fmt.Errorf("list events: %w", err)
	}
	defer rows.Close()

	var events []core.Event
	for rows.Next() {
		var e core.Event
		var issueID sql.NullString
		if err := rows.Scan(&e.Sequence, &e.ID, &issueID, &e.Actor, &e.EventType, &e.PayloadJSON, &e.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan event: %w", err)
		}
		if issueID.Valid {
			e.IssueID = issueID.String
		}
		events = append(events, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate events: %w", err)
	}
	if events == nil {
		events = []core.Event{}
	}
	return events, nil
}

type eventCursor struct {
	Sequence int64 `json:"sequence"`
}

type legacyEventCursor struct {
	ID        string `json:"id"`
	CreatedAt string `json:"created_at"`
}

// ListGlobalEvents returns a global cursor-paginated event stream ordered by
// daemon-assigned sequence.
// ListRecentEvents returns the newest events in ascending order and a cursor
// positioned at the newest event, ready for subsequent ListGlobalEvents calls.
func ListRecentEvents(ctx context.Context, db *sql.DB, limit int) (core.EventPage, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := db.QueryContext(ctx,
		`SELECT sequence, id, issue_id, actor, event_type, payload_json, created_at
		 FROM events ORDER BY sequence DESC LIMIT ?`, limit)
	if err != nil {
		return core.EventPage{}, fmt.Errorf("list recent events: %w", err)
	}
	defer rows.Close()
	events := make([]core.Event, 0)
	for rows.Next() {
		var event core.Event
		var issueID sql.NullString
		if err := rows.Scan(&event.Sequence, &event.ID, &issueID, &event.Actor, &event.EventType, &event.PayloadJSON, &event.CreatedAt); err != nil {
			return core.EventPage{}, fmt.Errorf("scan recent event: %w", err)
		}
		if issueID.Valid {
			event.IssueID = issueID.String
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return core.EventPage{}, fmt.Errorf("iterate recent events: %w", err)
	}
	for left, right := 0, len(events)-1; left < right; left, right = left+1, right-1 {
		events[left], events[right] = events[right], events[left]
	}
	sequence := int64(0)
	if len(events) > 0 {
		sequence = events[len(events)-1].Sequence
	}
	cursor, err := encodeEventCursor(eventCursor{Sequence: sequence})
	if err != nil {
		return core.EventPage{}, err
	}
	return core.EventPage{Events: events, NextSince: cursor}, nil
}

func ListGlobalEvents(ctx context.Context, db *sql.DB, since string, limit int) (core.EventPage, error) {
	if limit <= 0 {
		limit = 100
	}

	cursor, err := decodeEventCursor(ctx, db, since)
	if err != nil {
		return core.EventPage{}, err
	}

	var (
		rows *sql.Rows
	)
	if cursor == nil {
		rows, err = db.QueryContext(ctx,
			`SELECT sequence, id, issue_id, actor, event_type, payload_json, created_at
			 FROM events
			 ORDER BY sequence ASC
			 LIMIT ?`,
			limit,
		)
	} else {
		rows, err = db.QueryContext(ctx,
			`SELECT sequence, id, issue_id, actor, event_type, payload_json, created_at
			 FROM events
			 WHERE sequence > ?
			 ORDER BY sequence ASC
			 LIMIT ?`,
			cursor.Sequence, limit,
		)
	}
	if err != nil {
		return core.EventPage{}, fmt.Errorf("list global events: %w", err)
	}
	defer rows.Close()

	events := make([]core.Event, 0)
	for rows.Next() {
		var e core.Event
		var issueID sql.NullString
		if err := rows.Scan(&e.Sequence, &e.ID, &issueID, &e.Actor, &e.EventType, &e.PayloadJSON, &e.CreatedAt); err != nil {
			return core.EventPage{}, fmt.Errorf("scan global event: %w", err)
		}
		if issueID.Valid {
			e.IssueID = issueID.String
		}
		events = append(events, e)
	}
	if err := rows.Err(); err != nil {
		return core.EventPage{}, fmt.Errorf("iterate global events: %w", err)
	}

	page := core.EventPage{
		Events:    events,
		NextSince: since,
	}
	if page.Events == nil {
		page.Events = []core.Event{}
	}
	if len(page.Events) > 0 {
		last := page.Events[len(page.Events)-1]
		next, err := encodeEventCursor(eventCursor{Sequence: last.Sequence})
		if err != nil {
			return core.EventPage{}, err
		}
		page.NextSince = next
	}
	return page, nil
}

func encodeEventCursor(cursor eventCursor) (string, error) {
	data, err := json.Marshal(cursor)
	if err != nil {
		return "", fmt.Errorf("marshal event cursor: %w", err)
	}
	return "v2." + base64.RawURLEncoding.EncodeToString(data), nil
}

func decodeEventCursor(ctx context.Context, db *sql.DB, since string) (*eventCursor, error) {
	if since == "" {
		return nil, nil
	}

	decode := func(encoded string, destination any) error {
		raw, err := base64.RawURLEncoding.DecodeString(encoded)
		if err != nil {
			return core.NewAPIError(core.ErrValidationFailed, "since cursor is invalid")
		}
		if err := json.Unmarshal(raw, destination); err != nil {
			return core.NewAPIError(core.ErrValidationFailed, "since cursor is invalid")
		}
		return nil
	}

	switch {
	case strings.HasPrefix(since, "v2."):
		var cursor eventCursor
		if err := decode(strings.TrimPrefix(since, "v2."), &cursor); err != nil {
			return nil, err
		}
		if cursor.Sequence <= 0 {
			return nil, core.NewAPIError(core.ErrValidationFailed, "since cursor is invalid")
		}
		var exists int
		err := db.QueryRowContext(ctx, `SELECT 1 FROM events WHERE sequence = ?`, cursor.Sequence).Scan(&exists)
		if err == sql.ErrNoRows {
			return nil, core.NewAPIError(core.ErrValidationFailed, "since cursor does not reference an event")
		}
		if err != nil {
			return nil, fmt.Errorf("resolve event cursor: %w", err)
		}
		return &cursor, nil
	case strings.HasPrefix(since, "v1."):
		var legacy legacyEventCursor
		if err := decode(strings.TrimPrefix(since, "v1."), &legacy); err != nil {
			return nil, err
		}
		if legacy.ID == "" || legacy.CreatedAt == "" {
			return nil, core.NewAPIError(core.ErrValidationFailed, "since cursor is invalid")
		}
		if _, err := time.Parse(time.RFC3339, legacy.CreatedAt); err != nil {
			return nil, core.NewAPIError(core.ErrValidationFailed, "since cursor is invalid")
		}
		var sequence int64
		err := db.QueryRowContext(ctx,
			`SELECT sequence FROM events WHERE id = ? AND created_at = ?`,
			legacy.ID, legacy.CreatedAt,
		).Scan(&sequence)
		if err == sql.ErrNoRows {
			return nil, core.NewAPIError(core.ErrValidationFailed, "since cursor does not reference an event")
		}
		if err != nil {
			return nil, fmt.Errorf("resolve legacy event cursor: %w", err)
		}
		return &eventCursor{Sequence: sequence}, nil
	default:
		return nil, core.NewAPIError(core.ErrValidationFailed, "since cursor must start with v2. or v1.")
	}
}

// isSQLiteConstraintError checks if an error is a SQLite constraint violation.
func isSQLiteConstraintError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "UNIQUE constraint failed")
}

// scanIssueRow builds an Issue struct from raw fields (used by CreateIssue to avoid a second query).
func scanIssueRow(id, shortID, projectID string, repoID, worktreeID interface{},
	scopeKind, issueType, title, externalKey, description, acceptanceCriteria, status string, priority int,
	assignee string, version int, claimedAt, closedAt, holder, leaseExpiresAt, createdAt, updatedAt string) core.Issue {
	i := core.Issue{
		ID:                 id,
		ShortID:            shortID,
		ProjectID:          projectID,
		ScopeKind:          scopeKind,
		IssueType:          issueType,
		Title:              title,
		ExternalKey:        externalKey,
		Description:        description,
		AcceptanceCriteria: acceptanceCriteria,
		Status:             status,
		Priority:           priority,
		Assignee:           assignee,
		Version:            version,
		CreatedAt:          createdAt,
		UpdatedAt:          updatedAt,
	}
	if rid, ok := repoID.(string); ok {
		i.RepositoryID = rid
	}
	if wid, ok := worktreeID.(string); ok {
		i.WorktreeID = wid
	}
	if claimedAt != "" {
		i.ClaimedAt = claimedAt
	}
	if closedAt != "" {
		i.ClosedAt = closedAt
	}
	if holder != "" {
		i.Holder = holder
	}
	if leaseExpiresAt != "" {
		i.LeaseExpiresAt = leaseExpiresAt
	}
	return i
}
