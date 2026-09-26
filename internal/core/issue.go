package core

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Note represents a comment attached to an issue.
type Note struct {
	ID        string `json:"id"`
	IssueID   string `json:"issue_id"`
	Author    string `json:"author"`
	Body      string `json:"body"`
	CreatedAt string `json:"created_at"`
}

// CreateNoteRequest is the JSON body for POST /v1/issues/{issue_id}/notes.
type CreateNoteRequest struct {
	Author         string `json:"author"`
	Body           string `json:"body"`
	InvocationMode string `json:"invocation_mode,omitempty"`
}

// Event represents an event in the issue activity timeline.
type Event struct {
	Sequence    int64  `json:"sequence"`
	ID          string `json:"id"`
	IssueID     string `json:"issue_id,omitempty"`
	Actor       string `json:"actor"`
	EventType   string `json:"event_type"`
	PayloadJSON string `json:"payload_json"`
	CreatedAt   string `json:"created_at"`
}

// EventPage is a cursor-paginated page of global events.
type EventPage struct {
	Events    []Event `json:"events"`
	NextSince string  `json:"next_since"`
}

// IssueTypes lists the valid values for an issue's issue_type.
var IssueTypes = []string{"task", "bug", "feature", "epic", "chore"}

// ValidIssueType reports whether t is a known issue type.
func ValidIssueType(t string) bool {
	for _, v := range IssueTypes {
		if t == v {
			return true
		}
	}
	return false
}

// Invocation mode records how a claim, note, or close was initiated. It is
// caller-supplied and never inferred from the process tree, so a manual
// one-shot launcher run and an unattended scheduled run stay distinguishable
// in the audit trail (afc-95).
const (
	// InvocationModeInteractive marks a run initiated by a human or a
	// one-shot launcher (for example `dibs issue run` from a shell).
	InvocationModeInteractive = "interactive"
	// InvocationModeScheduled marks an unattended scheduled run (daemon or
	// cron) that picked the issue up on its own.
	InvocationModeScheduled = "scheduled"
	// InvocationModeUnknown is the conservative default recorded when the
	// caller did not declare a mode. It is a statement of absence, never an
	// answer: a reader must not read `unknown` as scheduled.
	InvocationModeUnknown = "unknown"
)

// InvocationModes lists the valid invocation-mode values in the public
// contract (snake_case by design, see AGENTS.md).
var InvocationModes = []string{InvocationModeInteractive, InvocationModeScheduled, InvocationModeUnknown}

// ValidInvocationMode reports whether mode is a known invocation mode.
func ValidInvocationMode(mode string) bool {
	for _, v := range InvocationModes {
		if mode == v {
			return true
		}
	}
	return false
}

// NormalizeInvocationMode applies the conservative default to an omitted
// value and rejects unknown non-empty values.
func NormalizeInvocationMode(mode string) (string, error) {
	if mode == "" {
		return InvocationModeUnknown, nil
	}
	if !ValidInvocationMode(mode) {
		return "", fmt.Errorf("invocation_mode must be one of: %s", strings.Join(InvocationModes, ", "))
	}
	return mode, nil
}

// Issue represents a task or work item in a project.
type Issue struct {
	ID                 string `json:"id"`
	ShortID            string `json:"short_id"`
	ProjectID          string `json:"project_id"`
	RepositoryID       string `json:"repository_id,omitempty"`
	WorktreeID         string `json:"worktree_id,omitempty"`
	ScopeKind          string `json:"scope_kind"`
	IssueType          string `json:"issue_type"`
	Title              string `json:"title"`
	ExternalKey        string `json:"external_key,omitempty"`
	Description        string `json:"description,omitempty"`
	AcceptanceCriteria string `json:"acceptance_criteria,omitempty"`
	Status             string `json:"status"`
	Priority           int    `json:"priority"`
	Assignee           string `json:"assignee,omitempty"`
	Version            int    `json:"version"`
	ClaimedAt          string `json:"claimed_at,omitempty"`
	Holder             string `json:"holder,omitempty"`
	LeaseExpiresAt     string `json:"lease_expires_at,omitempty"`
	// LeasePID is client-reported process metadata. issue run reports its local
	// supervisor; manual issue claim may report a caller ancestor. Other
	// clients can report the same typed session IDs. It is advisory, scoped to
	// LeaseHost, and never proves ownership or process liveness.
	LeasePID     int          `json:"lease_pid,omitempty"`
	LeaseHost    string       `json:"lease_host,omitempty"`
	ClosedAt     string       `json:"closed_at,omitempty"`
	CreatedAt    string       `json:"created_at"`
	UpdatedAt    string       `json:"updated_at"`
	Dependencies []Dependency `json:"dependencies,omitempty"`
	Blocked      bool         `json:"blocked,omitempty"`
	BlockedBy    []string     `json:"blocked_by,omitempty"`
	// Blocks lists the short IDs of non-terminal issues that this issue blocks
	// (the reverse of BlockedBy). It makes a blocking relationship visible from
	// both sides: A.BlockedBy contains B iff B.Blocks contains A.
	Blocks []string `json:"blocks,omitempty"`
	// Tags are namespaced 'namespace/value' strings that classify/route the
	// issue. They never carry state: status, lease, and version stay
	// first-class fields above (see ValidateTag / ADR-029).
	Tags []string `json:"tags,omitempty"`
}

// Dependency represents a relationship to another issue.
type Dependency struct {
	IssueID          string `json:"issue_id"`
	IssueShortID     string `json:"issue_short_id"`
	DependsOnID      string `json:"depends_on_id"`
	DependsOnShortID string `json:"depends_on_short_id"`
	Kind             string `json:"kind"`
}

// IssueLease represents the current lease on an issue (included in responses).
type IssueLease struct {
	Holder          string `json:"holder"`
	LeaseToken      string `json:"-"`
	LeaseGeneration int64  `json:"lease_generation"`
	ExpiresAt       string `json:"expires_at"`
	AttemptID       string `json:"attempt_id"`
	SessionID       string `json:"session_id,omitempty"`
}

// CreateIssueRequest is the JSON body for POST /v1/issues.
type CreateIssueRequest struct {
	OperationID        string   `json:"operation_id,omitempty"`
	Project            string   `json:"project"`
	ScopeKind          string   `json:"scope_kind"`
	IssueType          string   `json:"issue_type,omitempty"`
	Repo               string   `json:"repo,omitempty"`
	Worktree           string   `json:"worktree,omitempty"`
	Title              string   `json:"title"`
	ExternalKey        string   `json:"external_key,omitempty"`
	Description        string   `json:"description,omitempty"`
	AcceptanceCriteria string   `json:"acceptance_criteria,omitempty"`
	Priority           int      `json:"priority,omitempty"`
	Actor              string   `json:"actor,omitempty"`
	Tags               []string `json:"tags,omitempty"`
}

// IssueListParams represents query params for GET /v1/issues.
type IssueListParams struct {
	Project     string
	Repo        string
	Worktree    string
	Status      string
	Assignee    string
	IssueType   string
	ExternalKey string
	Limit       int
	Offset      int
	Projects    []string
	Statuses    []string
	IssueTypes  []string
	// Tags filters to issues carrying every listed tag (AND semantics),
	// via a per-tag EXISTS check against issue_tags.
	Tags []string
}

// NormalizeIssueListValues splits comma-separated filter values, trims
// surrounding whitespace, and rejects empty elements. It accepts repeated
// query values so HTTP and CLI callers can share the same normalization.
func NormalizeIssueListValues(values []string) ([]string, error) {
	if len(values) == 0 {
		return nil, nil
	}

	normalized := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		for _, element := range strings.Split(value, ",") {
			element = strings.TrimSpace(element)
			if element == "" {
				return nil, fmt.Errorf("filter values must not contain empty elements")
			}
			if _, ok := seen[element]; ok {
				continue
			}
			seen[element] = struct{}{}
			normalized = append(normalized, element)
		}
	}
	return normalized, nil
}

// ClaimRequest is the JSON body for POST /v1/issues/{issue_id}/claim.
type ClaimRequest struct {
	Holder         string `json:"holder"`
	TTLSeconds     int    `json:"ttl_seconds"`
	SessionID      string `json:"session_id,omitempty"`
	InvocationMode string `json:"invocation_mode,omitempty"`
	// OperationID is an opaque client-generated idempotency key
	// (AFC-SDD-0159). Retrying a claim with the same OperationID and the same
	// arguments returns the original committed ClaimResponse — including its
	// lease token and generation — instead of attempting a second claim.
	// Omitting it preserves the pre-ledger behavior exactly.
	//
	// It is a capability, not an identity: it is never returned by issue reads
	// or listings, and holder/session_id can neither substitute for it nor
	// override it.
	OperationID string `json:"operation_id,omitempty"`
}

// ClaimFingerprintFields returns the canonical request arguments that define a
// claim for idempotency purposes. Two claims are the same logical operation
// exactly when these match.
//
// TTL is included because it determines the committed expiry: replaying a
// 60-second claim in answer to a 3600-second request would silently hand back
// the wrong deadline, so that mismatch must fail closed instead.
//
// Holder and session_id are included to make replay stricter, never to
// authorize it. Ownership proof remains the operation_id; these fields only
// ensure a differently-attributed request is treated as a different request.
// The resolved issue ID is used so that a short ID and its UUID do not look
// like two different operations.
func ClaimFingerprintFields(issueID string, req ClaimRequest, invocationMode string) map[string]string {
	return map[string]string{
		"issue_id":        issueID,
		"holder":          req.Holder,
		"ttl_seconds":     strconv.Itoa(req.TTLSeconds),
		"session_id":      req.SessionID,
		"invocation_mode": invocationMode,
	}
}

// ClaimResponse is returned on successful claim.
type ClaimResponse struct {
	LeaseToken      string `json:"lease_token"`
	LeaseGeneration int64  `json:"lease_generation"`
	ExpiresAt       string `json:"expires_at"`
	AttemptID       string `json:"attempt_id"`
	// Version is the issue's version immediately after this claim. Claiming
	// increments the issue version as a side effect, so this is the value to
	// pass as --expected-version on the close/handoff that ends this attempt
	// — not a version read earlier from `issue get`, which is stale the
	// instant a claim succeeds.
	Version int `json:"version"`
}

// HeartbeatRequest is the JSON body for POST /v1/issues/{issue_id}/heartbeat.
// LeaseGeneration is the fencing value from the claim that created this lease;
// the daemon only renews the lease when token, generation, and expiry all match.
type HeartbeatRequest struct {
	LeaseToken      string `json:"lease_token"`
	LeaseGeneration int64  `json:"lease_generation"`
	TTLSeconds      int    `json:"ttl_seconds"`
	OperationID     string `json:"operation_id,omitempty"`
}

// ReleaseRequest is the JSON body for POST /v1/issues/{issue_id}/release.
// LeaseGeneration is the fencing value from the claim that created this lease;
// the daemon only releases the current unexpired lease when token and
// generation match.
type ReleaseRequest struct {
	LeaseToken      string `json:"lease_token"`
	LeaseGeneration int64  `json:"lease_generation"`
	OperationID     string `json:"operation_id,omitempty"`
}

// HandoffRequest is the JSON body for POST /v1/issues/{issue_id}/handoff.
// The server derives the note author from the active lease holder. The
// presented lease_generation must match the current unexpired lease, so a
// stale holder after a reclaim cannot hand the replacement's work off.
type HandoffRequest struct {
	LeaseToken      string `json:"lease_token"`
	LeaseGeneration int64  `json:"lease_generation"`
	Note            string `json:"note"`
	InvocationMode  string `json:"invocation_mode,omitempty"`
	OperationID     string `json:"operation_id,omitempty"`
}

// HandoffResponse is returned after atomically recording a HANDOFF note and
// releasing the active lease.
type HandoffResponse struct {
	Note Note `json:"note"`
}

// UpdateIssueRequest is the JSON body for PATCH /v1/issues/{issue_id}.
type UpdateIssueRequest struct {
	Title              string `json:"title,omitempty"`
	IssueType          string `json:"issue_type,omitempty"`
	ExternalKey        string `json:"external_key,omitempty"`
	Description        string `json:"description,omitempty"`
	AcceptanceCriteria string `json:"acceptance_criteria,omitempty"`
	Priority           int    `json:"priority,omitempty"`
	Assignee           string `json:"assignee,omitempty"`
	Status             string `json:"status,omitempty"`
	ExpectedVersion    int    `json:"expected_version"`
	LeaseToken         string `json:"lease_token,omitempty"`
	LeaseGeneration    int64  `json:"lease_generation"`
	ReleaseLease       bool   `json:"release_lease,omitempty"`
	Actor              string `json:"actor,omitempty"`
	OperationID        string `json:"operation_id,omitempty"`
}

// CloseIssueRequest is the JSON body for POST /v1/issues/{issue_id}/close.
type CloseIssueRequest struct {
	Resolution      string `json:"resolution"`
	Branch          string `json:"branch,omitempty"`
	PRURL           string `json:"pr_url,omitempty"`
	CommitSHA       string `json:"commit_sha,omitempty"`
	ExpectedVersion int    `json:"expected_version"`
	LeaseToken      string `json:"lease_token"`
	LeaseGeneration int64  `json:"lease_generation"`
	Actor           string `json:"actor,omitempty"`
	Note            string `json:"note,omitempty"`
	InvocationMode  string `json:"invocation_mode,omitempty"`
	OperationID     string `json:"operation_id,omitempty"`
}

// OperatorCloseIssueRequest closes an issue through the explicit local
// operator path. It never accepts a lease token.
type OperatorCloseIssueRequest struct {
	Resolution      string `json:"resolution"`
	Branch          string `json:"branch,omitempty"`
	PRURL           string `json:"pr_url,omitempty"`
	CommitSHA       string `json:"commit_sha,omitempty"`
	ExpectedVersion int    `json:"expected_version"`
	Actor           string `json:"actor"`
	Reason          string `json:"reason"`
	Note            string `json:"note,omitempty"`
	InvocationMode  string `json:"invocation_mode,omitempty"`
}

// OperatorReopenIssueRequest reopens terminal work through the explicit local
// operator path. It never accepts a lease token.
type OperatorReopenIssueRequest struct {
	ExpectedVersion int    `json:"expected_version"`
	Actor           string `json:"actor"`
	Reason          string `json:"reason"`
}

// OperatorReleaseIssueRequest force-clears a stuck in_progress lease through
// the explicit local operator path, returning the issue to open without
// closing it. It never accepts a lease token — that is the point: it is the
// recovery path for a claim whose lease token was lost (crashed script,
// never persisted) before its TTL naturally expired it.
type OperatorReleaseIssueRequest struct {
	ExpectedVersion int    `json:"expected_version"`
	Actor           string `json:"actor"`
	Reason          string `json:"reason"`
}

// CloseIssueResult is returned after a successful close.
type CloseIssueResult struct {
	Status      string `json:"status"`
	Resolution  string `json:"resolution"`
	Branch      string `json:"branch,omitempty"`
	PRURL       string `json:"pr_url,omitempty"`
	CommitSHA   string `json:"commit_sha,omitempty"`
	ExternalKey string `json:"external_key,omitempty"`
	ClosedAt    string `json:"closed_at"`
}

// AddDependencyRequest is the JSON body for POST /v1/issues/{issue_id}/dependencies.
type AddDependencyRequest struct {
	DependsOn string `json:"depends_on"`
	Kind      string `json:"kind"`
	Actor     string `json:"actor,omitempty"`
}

// RemoveDependencyRequest holds the path and query params for DELETE /v1/issues/{issue_id}/dependencies/{depends_on}.
type RemoveDependencyRequest struct {
	DependsOn string
	Kind      string
	Actor     string
}

// AddTagRequest is the JSON body for POST /v1/issues/{issue_id}/tags.
type AddTagRequest struct {
	Tag   string `json:"tag"`
	Actor string `json:"actor,omitempty"`
}

// LinkArtifactRequest is the JSON body for POST /v1/issues/{issue_id}/links.
type LinkArtifactRequest struct {
	Artifact string `json:"artifact"`           // artifact ID or relative path
	Relation string `json:"relation,omitempty"` // default: "implements"
}

// UnlinkArtifactRequest holds the query params for DELETE /v1/issues/{issue_id}/links.
type UnlinkArtifactRequest struct {
	Artifact string // artifact ID or relative path
	Relation string // if empty, removes every relation to the artifact
	Actor    string
}

// ArtifactRef is a linked artifact with relation info.
type ArtifactRef struct {
	ID           string `json:"id"`
	RelativePath string `json:"relative_path"`
	Kind         string `json:"kind"`
	Relation     string `json:"relation"`
}

// ValidateCreateIssue checks required fields for creating an issue.
func ValidateCreateIssue(req CreateIssueRequest) error {
	var errs []string
	if req.Project == "" {
		errs = append(errs, "project is required")
	}
	if req.ScopeKind == "" {
		errs = append(errs, "scope_kind is required")
	} else if req.ScopeKind != "project" && req.ScopeKind != "repository" && req.ScopeKind != "worktree" {
		errs = append(errs, "scope_kind must be 'project', 'repository', or 'worktree'")
	}
	if req.Title == "" {
		errs = append(errs, "title is required")
	}
	if req.IssueType != "" && !ValidIssueType(req.IssueType) {
		errs = append(errs, "issue_type must be one of: "+strings.Join(IssueTypes, ", "))
	}
	// For repo/worktree scope, repo is required
	if req.ScopeKind != "project" && req.Repo == "" {
		errs = append(errs, "repo is required when scope_kind is not 'project'")
	}
	for _, tag := range req.Tags {
		if err := ValidateTag(tag); err != nil {
			errs = append(errs, err.Error())
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("validation_failed: %s", strings.Join(errs, "; "))
	}
	return nil
}

// validTag matches namespaced tags 'namespace/value' using a closed
// charset: lowercase letters, digits, and '-' on each side of exactly one
// '/'. The per-segment character classes already exclude '/', so a second
// slash fails the match without a separate check.
var validTag = regexp.MustCompile(`^[a-z0-9-]+/[a-z0-9-]+$`)

// maxTagLength bounds a tag's total length (namespace/value combined).
const maxTagLength = 64

// reservedTagNamespaces are namespaces that would let a tag masquerade as
// first-class issue state. Status, lease, and version stay first-class
// columns on Issue; the coordinator ledger is the single source of state
// (see docs/decisions/ADR-029-af-coordinator-as-control-plane.md).
var reservedTagNamespaces = map[string]bool{
	"open":        true,
	"blocked":     true,
	"done":        true,
	"in_progress": true,
	"status":      true,
	"state":       true,
}

// ValidateTag checks a namespaced tag ('namespace/value') against the
// closed charset, length bound, and the no-state-in-tags invariant.
func ValidateTag(tag string) error {
	var errs []string
	switch {
	case tag == "":
		errs = append(errs, "tag is required")
	case len(tag) > maxTagLength:
		errs = append(errs, fmt.Sprintf("tag must be at most %d characters", maxTagLength))
	case !validTag.MatchString(tag):
		errs = append(errs, fmt.Sprintf("tag %q must be 'namespace/value' using lowercase letters, digits, and '-' only", tag))
	default:
		ns := tag[:strings.IndexByte(tag, '/')]
		if reservedTagNamespaces[ns] {
			errs = append(errs, fmt.Sprintf("tag namespace %q is reserved for issue state, not tags", ns))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return nil
}

// ValidateHandoffRequest checks the non-secret user input for an atomic
// HANDOFF. The lease token is authorized separately by the store.
func ValidateHandoffRequest(req HandoffRequest) error {
	if strings.TrimSpace(req.Note) == "" {
		return fmt.Errorf("note is required")
	}
	if !strings.HasPrefix(req.Note, "HANDOFF:") {
		return fmt.Errorf("note must begin with HANDOFF:")
	}
	return nil
}

// ValidateStatusTransition checks if a status transition is valid.
func ValidateStatusTransition(current, target string) error {
	validTransitions := map[string][]string{
		"open":        {"in_progress", "blocked", "deferred", "done", "cancelled"},
		"in_progress": {"open", "blocked", "deferred", "done", "cancelled"},
		"blocked":     {"open", "in_progress", "deferred", "done", "cancelled"},
		"deferred":    {"open", "in_progress", "blocked", "done", "cancelled"},
		"done":        {"open"},
		"cancelled":   {"open"},
	}
	valid, ok := validTransitions[current]
	if !ok {
		return fmt.Errorf("validation_failed: invalid current status: %s", current)
	}
	for _, v := range valid {
		if target == v {
			return nil
		}
	}
	return fmt.Errorf("validation_failed: cannot transition from '%s' to '%s'", current, target)
}
