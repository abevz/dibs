package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/abevz/dibs/internal/client"
	"github.com/abevz/dibs/internal/core"
	"github.com/abevz/dibs/internal/github"
)

const issuePublishUsage = "Usage: dibs issue publish <issue-id>\nPublish the latest close result as a comment on its GitHub source. The closing note and branch are posted to GitHub publicly. Requires gh >= 2.48.0."

type publishError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type reportedError struct{ cause error }

func (e *reportedError) Error() string { return e.cause.Error() }

func requireGitHubExternalKey(ctx context.Context, c *client.Client, issueID string) error {
	issue, _, err := c.GetIssue(ctx, issueID)
	if err != nil {
		return err
	}
	if !strings.HasPrefix(issue.ExternalKey, "github:") {
		return fmt.Errorf("issue %s has no GitHub external key; import or link a GitHub issue before --publish", issue.ShortID)
	}
	_, err = github.ParseIssueRef(strings.TrimPrefix(issue.ExternalKey, "github:"))
	if err != nil {
		return fmt.Errorf("invalid GitHub external key: %w", err)
	}
	return nil
}

type publishResult struct {
	OK         bool          `json:"ok"`
	Already    bool          `json:"already"`
	CommentURL string        `json:"comment_url"`
	Error      *publishError `json:"error"`
}

func runIssuePublish(ctx context.Context, c *client.Client, args []string) error {
	if hasHelpFlag(args) {
		fmt.Println(issuePublishUsage)
		return nil
	}
	if len(args) != 1 {
		return usageErr(issuePublishUsage, "one issue ID is required")
	}
	issue, _, err := c.GetIssue(ctx, args[0])
	if err != nil {
		if jsonOutput {
			return reportPublishJSON(args[0], failedPublish(err), err)
		}
		return err
	}
	result, err := publishForIssue(ctx, c, github.CLI{}, issue)
	if err != nil {
		if jsonOutput {
			return reportPublishJSON(issue.ShortID, failedPublish(err), err)
		}
		return err
	}
	if jsonOutput {
		return json.NewEncoder(os.Stdout).Encode(struct {
			Issue string `json:"issue"`
			publishResult
		}{issue.ShortID, result})
	}
	printPublishResult(result, issue.ShortID)
	return nil
}

func reportPublishJSON(shortID string, result publishResult, failure error) error {
	if err := json.NewEncoder(os.Stdout).Encode(struct {
		Issue string `json:"issue"`
		publishResult
	}{shortID, result}); err != nil {
		return err
	}
	return &reportedError{failure}
}

// publishAfterClose never changes the outcome of a successful local close.
// A failure is reported with a runnable retry command and in JSON output.
func publishAfterClose(ctx context.Context, c *client.Client, issueID, leaseToken string) publishResult {
	issue, _, err := c.GetIssue(ctx, issueID)
	shortID := issue.ShortID
	if shortID == "" {
		shortID = issueID
	}
	if err == nil {
		var result publishResult
		result, err = publishForIssue(ctx, c, github.CLI{}, issue, leaseToken)
		if err == nil {
			return result
		}
	}
	fmt.Fprintf(os.Stderr, "publish failed: %v; retry: dibs issue publish %s\n", err, shortID)
	return failedPublish(err)
}

func failedPublish(err error) publishResult {
	code, message := "publish_failed", err.Error()
	var ghErr *github.Error
	if errors.As(err, &ghErr) {
		code, message = ghErr.Code, ghErr.Message()
	}
	return publishResult{Error: &publishError{Code: code, Message: message}}
}

func printPublishResult(result publishResult, shortID string) {
	if !result.OK {
		return // publishAfterClose already wrote the failure and retry command.
	}
	if result.Already {
		fmt.Printf("Already published %s to GitHub: %s\n", shortID, result.CommentURL)
	} else {
		fmt.Printf("Published %s to GitHub: %s\n", shortID, result.CommentURL)
	}
}

func publishForIssue(ctx context.Context, c *client.Client, gh github.Client, issue core.Issue, leaseTokens ...string) (publishResult, error) {
	if issue.Status != "done" && issue.Status != "cancelled" {
		return publishResult{}, fmt.Errorf("issue %s is not closed; close it before publishing", issue.ShortID)
	}
	if !strings.HasPrefix(issue.ExternalKey, "github:") {
		return publishResult{}, fmt.Errorf("issue %s has no GitHub external key", issue.ShortID)
	}
	ref, err := github.ParseIssueRef(strings.TrimPrefix(issue.ExternalKey, "github:"))
	if err != nil {
		return publishResult{}, fmt.Errorf("invalid GitHub external key: %w", err)
	}
	source, err := gh.GetIssue(ctx, ref)
	if err != nil {
		return publishResult{}, err
	}
	if source.Locked {
		return publishResult{}, &github.Error{Code: "locked", Remedy: "unlock the GitHub issue or ask a repository maintainer to do so"}
	}
	if source.IsPullRequest() {
		return publishResult{}, fmt.Errorf("GitHub source is a pull request, not an issue")
	}
	events, err := c.ListEvents(ctx, issue.ID)
	if err != nil {
		return publishResult{}, err
	}
	closed, err := latestClose(events)
	if err != nil {
		return publishResult{}, err
	}
	if closed.Event.ID == "" {
		return publishResult{}, fmt.Errorf("close event has no ID")
	}
	note := ""
	if hasCloseNoteEvent(events, closed.Event) {
		notes, err := c.ListNotes(ctx, issue.ID)
		if err != nil {
			return publishResult{}, err
		}
		note = closingNote(events, notes, closed.Event)
	}
	if err := validatePublicText(note, closed.Branch, leaseTokens...); err != nil {
		return publishResult{}, err
	}
	marker := fmt.Sprintf("<!-- dibs:publish issue=%s close_event=%s -->", issue.ID, closed.Event.ID)
	comments, err := gh.ListComments(ctx, source.CommentsURL)
	if err != nil {
		return publishResult{}, err
	}
	for _, comment := range comments {
		if strings.Contains(comment.Body, marker) {
			return publishResult{OK: true, Already: true, CommentURL: comment.HTMLURL}, nil
		}
	}
	body := renderPublishComment(issue.ShortID, closed, note, marker)
	comment, err := gh.CreateComment(ctx, source.CommentsURL, body)
	if err != nil {
		return publishResult{}, err
	}
	return publishResult{OK: true, CommentURL: comment.HTMLURL}, nil
}

// Only exact currently configured token values are checked. The note and
// branch are text deliberately supplied by the closer and otherwise remain
// unchanged for publication.
func validatePublicText(note, branch string, leaseTokens ...string) error {
	for _, key := range []string{"DIBS_LEASE_TOKEN", "DIBS_OPERATOR_TOKEN", "AF_OPERATOR_TOKEN"} {
		value := os.Getenv(key)
		if value != "" && (strings.Contains(note, value) || strings.Contains(branch, value)) {
			return fmt.Errorf("closing note or branch contains the current %s value; remove it before publishing", key)
		}
	}
	for _, value := range leaseTokens {
		if value != "" && (strings.Contains(note, value) || strings.Contains(branch, value)) {
			return fmt.Errorf("closing note or branch contains the active lease token; remove it before publishing")
		}
	}
	return nil
}

func renderPublishComment(shortID string, closed closeRecord, note, marker string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "**dibs:** `%s` closed as **%s**.\n", shortID, closed.Resolution)
	if closed.PRURL != "" || closed.CommitSHA != "" || closed.Branch != "" {
		b.WriteString("\n")
	}
	if closed.PRURL != "" {
		fmt.Fprintf(&b, "- PR: %s\n", closed.PRURL)
	}
	if closed.CommitSHA != "" {
		fmt.Fprintf(&b, "- Commit: `%s`", closed.CommitSHA)
		if closed.Branch != "" {
			fmt.Fprintf(&b, " on `%s`", closed.Branch)
		}
		b.WriteString("\n")
	} else if closed.Branch != "" {
		fmt.Fprintf(&b, "- Branch: `%s`\n", closed.Branch)
	}
	if note != "" {
		b.WriteString("\n")
		runes := []rune(note)
		if len(runes) > 2000 {
			note = string(runes[:1999]) + "…"
		}
		for _, line := range strings.Split(note, "\n") {
			fmt.Fprintf(&b, "> %s\n", line)
		}
	}
	fmt.Fprintf(&b, "\n%s", marker)
	return b.String()
}
