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
		path := strings.TrimSuffix(u.EscapedPath(), "/")
		parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
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
	CommentsURL string          `json:"comments_url"`
	PullRequest json.RawMessage `json:"pull_request"`
}

func (i Issue) IsPullRequest() bool { return len(i.PullRequest) > 0 && string(i.PullRequest) != "null" }

type Comment struct {
	Body    string `json:"body"`
	HTMLURL string `json:"html_url"`
}

type Client interface {
	GetIssue(context.Context, IssueRef) (Issue, error)
	ListComments(context.Context, string) ([]Comment, error)
	CreateComment(context.Context, string, string) (Comment, error)
}

type Error struct {
	Code   string
	Remedy string
	Stderr string
}

func (e *Error) Message() string {
	if e.Stderr == "" {
		return e.Remedy
	}
	if strings.HasPrefix(strings.ToLower(e.Stderr), "gh:") {
		return e.Stderr + "; " + e.Remedy
	}
	return "gh: " + e.Stderr + "; " + e.Remedy
}

func (e *Error) Error() string { return e.Code + ": " + e.Message() }

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
		return &Error{Code: "gh_missing", Remedy: "install GitHub CLI (gh)"}
	}
	if errors.Is(ctxErr, context.DeadlineExceeded) || errors.Is(ctxErr, context.Canceled) {
		return &Error{Code: "timeout", Remedy: "retry the GitHub request; check your network connection"}
	}
	var exitErr *exec.ExitError
	message := strings.ToLower(err.Error())
	stderr := ""
	if errors.As(err, &exitErr) {
		message += " " + strings.ToLower(string(exitErr.Stderr))
		stderr = shortStderr(string(exitErr.Stderr))
	}
	switch {
	case strings.Contains(message, "rate limit"), strings.Contains(message, "secondary rate"):
		return &Error{Code: "rate_limited", Remedy: "wait for the GitHub API rate limit to reset", Stderr: stderr}
	case strings.Contains(message, "not logged in"), strings.Contains(message, "authentication required"), strings.Contains(message, "authentication failed"), strings.Contains(message, "requires authentication"), strings.Contains(message, "bad credentials"), strings.Contains(message, "http 401"), strings.Contains(message, "gh auth login"):
		return &Error{Code: "gh_auth", Remedy: "run gh auth login for github.com", Stderr: stderr}
	case strings.Contains(message, "http 404"), strings.Contains(message, "404 not found"), strings.Contains(message, "not found (http 404)"):
		return &Error{Code: "not_found", Remedy: "check the issue reference and your repository access", Stderr: stderr}
	default:
		return &Error{Code: "github", Remedy: "check gh and your network connection, then retry", Stderr: stderr}
	}
}

func shortStderr(raw string) string {
	oneLine := strings.Join(strings.Fields(raw), " ")
	runes := []rune(oneLine)
	if len(runes) > 240 {
		return string(runes[:240]) + "…"
	}
	return oneLine
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

// commentsEndpoint only accepts the GitHub API URL returned by GetIssue.
// In particular, no arbitrary host or URL credentials reach gh api.
func commentsEndpoint(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host != "api.github.com" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || !strings.HasPrefix(u.Path, "/repos/") || !strings.HasSuffix(u.Path, "/comments") {
		return "", fmt.Errorf("github: invalid comments_url in issue response")
	}
	return strings.TrimPrefix(u.Path, "/"), nil
}

func (c CLI) ListComments(ctx context.Context, commentsURL string) ([]Comment, error) {
	endpoint, err := commentsEndpoint(commentsURL)
	if err != nil {
		return nil, err
	}
	out, err := c.call(ctx, nil, "api", endpoint, "--paginate", "--slurp")
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

func (c CLI) CreateComment(ctx context.Context, commentsURL, body string) (Comment, error) {
	endpoint, err := commentsEndpoint(commentsURL)
	if err != nil {
		return Comment{}, err
	}
	input, err := json.Marshal(map[string]string{"body": body})
	if err != nil {
		return Comment{}, err
	}
	out, err := c.call(ctx, input, "api", endpoint, "--method", "POST", "--input", "-")
	if err != nil {
		return Comment{}, err
	}
	var comment Comment
	if err := json.Unmarshal(out, &comment); err != nil {
		return Comment{}, fmt.Errorf("github: invalid comment response: %w", err)
	}
	return comment, nil
}
