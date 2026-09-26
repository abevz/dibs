// Package github contains the CLI-only GitHub boundary used by issue import
// and result publication. The coordinator daemon does not call GitHub.
package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type IssueRef struct {
	Owner  string
	Repo   string
	Number int64
}

var shortRef = regexp.MustCompile(`^([A-Za-z0-9][A-Za-z0-9-]*)/([A-Za-z0-9._-]+)#([1-9][0-9]*)$`)

func ParseIssueRef(raw string) (IssueRef, error) {
	if strings.HasPrefix(strings.ToLower(raw), "https://") {
		u, err := url.Parse(raw)
		if err != nil || !strings.EqualFold(u.Hostname(), "github.com") || u.User != nil || u.Port() != "" {
			return IssueRef{}, fmt.Errorf("invalid GitHub issue URL: %q", raw)
		}
		parts := strings.Split(strings.TrimPrefix(u.EscapedPath(), "/"), "/")
		if len(parts) != 4 || parts[2] != "issues" {
			return IssueRef{}, fmt.Errorf("expected a GitHub issue URL, not a pull request: %q", raw)
		}
		raw = parts[0] + "/" + parts[1] + "#" + parts[3]
	}
	m := shortRef.FindStringSubmatch(raw)
	if m == nil {
		return IssueRef{}, fmt.Errorf("invalid GitHub issue reference %q; use https://github.com/owner/repo/issues/N or owner/repo#N", raw)
	}
	n, err := strconv.ParseInt(m[3], 10, 64)
	if err != nil || n < 1 {
		return IssueRef{}, fmt.Errorf("invalid GitHub issue number in %q", raw)
	}
	return IssueRef{Owner: m[1], Repo: m[2], Number: n}, nil
}

func (r IssueRef) ExternalKey() string {
	return fmt.Sprintf("github:%s/%s#%d", strings.ToLower(r.Owner), strings.ToLower(r.Repo), r.Number)
}

func (r IssueRef) String() string { return fmt.Sprintf("%s/%s#%d", r.Owner, r.Repo, r.Number) }

func (r IssueRef) endpoint() string {
	return fmt.Sprintf("repos/%s/%s/issues/%d", r.Owner, r.Repo, r.Number)
}

type Issue struct {
	Title       string          `json:"title"`
	Body        string          `json:"body"`
	State       string          `json:"state"`
	Locked      bool            `json:"locked"`
	HTMLURL     string          `json:"html_url"`
	PullRequest json.RawMessage `json:"pull_request"`
}

func (i Issue) IsPullRequest() bool { return len(i.PullRequest) > 0 && string(i.PullRequest) != "null" }

type Comment struct {
	Body    string `json:"body"`
	HTMLURL string `json:"html_url"`
}

type Client interface {
	GetIssue(context.Context, IssueRef) (Issue, error)
	ListComments(context.Context, IssueRef) ([]Comment, error)
	CreateComment(context.Context, IssueRef, string) (Comment, error)
}

type Error struct {
	Code   string
	Remedy string
}

func (e *Error) Error() string { return e.Code + ": " + e.Remedy }

// CLI uses the user's gh authentication and never writes credentials to dibs.
type CLI struct{}

func (CLI) call(ctx context.Context, input []byte, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gh", args...)
	if input != nil {
		cmd.Stdin = bytes.NewReader(input)
	}
	out, err := cmd.Output()
	if err != nil {
		return nil, classifyError(err, ctx.Err())
	}
	return out, nil
}

func classifyError(err, ctxErr error) error {
	var execErr *exec.Error
	if errors.As(err, &execErr) && errors.Is(execErr.Err, exec.ErrNotFound) {
		return &Error{"gh_missing", "install GitHub CLI (gh)"}
	}
	if errors.Is(ctxErr, context.DeadlineExceeded) || errors.Is(ctxErr, context.Canceled) {
		return &Error{"timeout", "retry the GitHub request; check your network connection"}
	}
	var exitErr *exec.ExitError
	message := strings.ToLower(err.Error())
	if errors.As(err, &exitErr) {
		message += " " + strings.ToLower(string(exitErr.Stderr))
	}
	switch {
	case strings.Contains(message, "rate limit"), strings.Contains(message, "secondary rate"):
		return &Error{"rate_limited", "wait for the GitHub API rate limit to reset"}
	case strings.Contains(message, "not logged in"), strings.Contains(message, "authentication required"), strings.Contains(message, "authentication failed"), strings.Contains(message, "requires authentication"), strings.Contains(message, "bad credentials"), strings.Contains(message, "http 401"), strings.Contains(message, "gh auth login"):
		return &Error{"gh_auth", "run gh auth login for github.com"}
	case strings.Contains(message, "http 404"), strings.Contains(message, "404 not found"), strings.Contains(message, "not found (http 404)"):
		return &Error{"not_found", "check the issue reference and your repository access"}
	default:
		return &Error{"github", "check gh and your network connection, then retry"}
	}
}

func (c CLI) GetIssue(ctx context.Context, ref IssueRef) (Issue, error) {
	out, err := c.call(ctx, nil, "api", ref.endpoint())
	if err != nil {
		return Issue{}, err
	}
	var issue Issue
	if err := json.Unmarshal(out, &issue); err != nil {
		return Issue{}, fmt.Errorf("github: invalid issue response: %w", err)
	}
	return issue, nil
}

func (c CLI) ListComments(ctx context.Context, ref IssueRef) ([]Comment, error) {
	out, err := c.call(ctx, nil, "api", ref.endpoint()+"/comments", "--paginate", "--slurp")
	if err != nil {
		return nil, err
	}
	var pages [][]Comment
	if err := json.Unmarshal(out, &pages); err != nil {
		return nil, fmt.Errorf("github: invalid comments response: %w", err)
	}
	var comments []Comment
	for _, page := range pages {
		comments = append(comments, page...)
	}
	return comments, nil
}

func (c CLI) CreateComment(ctx context.Context, ref IssueRef, body string) (Comment, error) {
	input, err := json.Marshal(map[string]string{"body": body})
	if err != nil {
		return Comment{}, err
	}
	out, err := c.call(ctx, input, "api", ref.endpoint()+"/comments", "--method", "POST", "--input", "-")
	if err != nil {
		return Comment{}, err
	}
	var comment Comment
	if err := json.Unmarshal(out, &comment); err != nil {
		return Comment{}, fmt.Errorf("github: invalid comment response: %w", err)
	}
	return comment, nil
}
