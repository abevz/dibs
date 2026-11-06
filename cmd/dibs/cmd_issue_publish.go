package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/abevz/dibs/internal/client"
	"github.com/abevz/dibs/internal/core"
	"github.com/abevz/dibs/internal/ghsync"
	"github.com/abevz/dibs/internal/github"
)

const issuePublishUsage = "Usage: dibs issue publish <issue-id>\nPublish the latest close result as a comment on its GitHub source. The closing note and branch are posted to GitHub publicly. Requires gh >= 2.48.0."

type publishError = ghsync.PublishError
type publishResult = ghsync.PublishResult

type reportedError struct{ cause error }

func (e *reportedError) Error() string { return e.cause.Error() }

func requireGitHubExternalKey(ctx context.Context, c *client.Client, issueID string) error {
	return ghsync.RequireGitHubExternalKey(ctx, c, issueID)
}

func runIssuePublish(ctx context.Context, c *client.Client, args []string) error {
	if hasHelpFlag(args) {
		fmt.Println(issuePublishUsage)
		return nil
	}
	if len(args) < 1 {
		return usageErr(issuePublishUsage, "one issue ID is required")
	}
	f := evidenceFlags(args[1:])
	if err := checkIssueEvidence(ctx, c, args[0], f["--require-evidence"], f["--diff-hash"]); err != nil {
		return err
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
		return encodePublishJSON(issue.ShortID, result)
	}
	printPublishResult(result, issue.ShortID)
	return nil
}

func encodePublishJSON(shortID string, result publishResult) error {
	return json.NewEncoder(os.Stdout).Encode(struct {
		Issue string `json:"issue"`
		publishResult
	}{shortID, result})
}

func reportPublishJSON(shortID string, result publishResult, failure error) error {
	if err := encodePublishJSON(shortID, result); err != nil {
		return err
	}
	return &reportedError{failure}
}

// publishAfterClose only computes a result. Callers print their successful
// local close before printing any publication failure or retry guidance.
func publishAfterClose(ctx context.Context, c *client.Client, issueID, leaseToken string) publishResult {
	issue, _, err := c.GetIssue(ctx, issueID)
	shortID := issue.ShortID
	if shortID == "" {
		shortID = issueID
	}
	if err == nil {
		result, publishErr := ghsync.PublishIssue(ctx, c, github.CLI{}, issue, leaseToken)
		if publishErr == nil {
			result.ShortID = shortID
			return result
		}
		err = publishErr
	}
	result := failedPublish(err)
	result.ShortID = shortID
	return result
}

func failedPublish(err error) publishResult { return ghsync.Failure(err) }

func printPublishResult(result publishResult, issueID string) {
	shortID := result.ShortID
	if shortID == "" {
		shortID = issueID
	}
	if !result.OK {
		printPublishFailure(result, shortID)
		return
	}
	if result.Already {
		fmt.Printf("Already published %s to GitHub: %s\n", shortID, result.CommentURL)
	} else {
		fmt.Printf("Published %s to GitHub: %s\n", shortID, result.CommentURL)
	}
}

func printPublishFailure(result publishResult, shortID string) {
	if result.Error == nil {
		return
	}
	if result.Error.Code == "secret_in_text" {
		fmt.Fprintf(os.Stderr, "publish failed: %s\n", result.Error.Message)
		return
	}
	fmt.Fprintf(os.Stderr, "publish failed: %s; retry: dibs issue publish %s\n", result.Error.Message, shortID)
}

func publishForIssue(ctx context.Context, c *client.Client, gh github.Client, issue core.Issue, leaseTokens ...string) (publishResult, error) {
	return ghsync.PublishIssue(ctx, c, gh, issue, leaseTokens...)
}

func validatePublicText(note, branch string, leaseTokens ...string) error {
	return ghsync.ValidatePublicText(note, branch, leaseTokens...)
}

func renderPublishComment(shortID string, closed closeRecord, note, marker string) string {
	return ghsync.RenderPublishComment(shortID, closed, note, marker)
}
