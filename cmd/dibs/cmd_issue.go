package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/abevz/dibs/internal/client"
	"github.com/abevz/dibs/internal/config"
	"github.com/abevz/dibs/internal/core"
)

// ─── Issue ───────────────────────────────────────────────────────────────────

const issueUsage = "Usage: dibs issue <create|create-form|import|get|list|ready|claim|heartbeat|release|handoff|run|edit|update|close|operator-close|operator-reopen|operator-release|cancel|link|unlink|dependency|note|tag|events>"

// hasHelpFlag reports whether args requests help before a -- separator.
// Arguments after the separator belong to a child command, not dibs.
func hasHelpFlag(args []string) bool {
	for _, a := range args {
		if a == "--" {
			break
		}
		if a == "--help" || a == "-help" || a == "-h" {
			return true
		}
	}
	return false
}

// lifecycleHint points an agent stuck on a partial or incorrect invocation
// of a claim/close/handoff-family command at the authoritative lifecycle
// contract, instead of leaving it to rediscover required flags one error at
// a time.
const lifecycleHint = "run: dibs protocol   (full issue lifecycle: ready -> claim -> heartbeat -> close/handoff)\nAgents: use `dibs issue run`; never put a lease token in argv. Manual commands read DIBS_LEASE_TOKEN or DIBS_LEASE_TOKEN_FILE when --lease-token is omitted."

// usageErr builds a validation error that always shows the full usage line,
// not just the one missing or malformed flag, so a caller sees every
// requirement at once. detail is appended as an additional line when
// non-empty; fail() in main.go already prefixes the result with "error: ",
// so detail must not repeat that prefix.
func usageErr(usage, detail string) error {
	if detail == "" {
		return fmt.Errorf("%s", usage)
	}
	return fmt.Errorf("%s\n%s", usage, detail)
}

// resolveExpectedVersion fills version with the issue's current version when
// it is still the unset sentinel -1, which is the case when --expected-version
// is omitted, set to "latest", or --force is given. Resolution is a CLI
// convenience only: the request sent to the API always carries a concrete
// version number, so the server-side optimistic-concurrency check is
// unchanged. Each caller keeps its own post-resolution guard, preserving the
// per-command sentinel semantics (update: < 0, operator-*: <= 0).
func resolveExpectedVersion(ctx context.Context, c *client.Client, usage, issueID string, version *int) error {
	if *version != -1 {
		return nil
	}
	issue, _, err := c.GetIssue(ctx, issueID)
	if err != nil {
		return usageErr(usage, fmt.Sprintf("failed to fetch current issue version: %v", err))
	}
	*version = issue.Version
	return nil
}

func runIssue(ctx context.Context, c *client.Client, args []string) error {
	if len(args) < 1 {
		return usageErr(issueUsage, "")
	}
	if args[0] == "--help" || args[0] == "-h" || args[0] == "help" {
		fmt.Println(issueUsage)
		return nil
	}

	switch args[0] {
	case "create":
		return runIssueCreate(ctx, c, args[1:])
	case "create-form":
		return runIssueCreateForm(ctx, c, args[1:])
	case "import":
		return runIssueImport(ctx, c, args[1:])
	case "get":
		return runIssueGet(ctx, c, args[1:])
	case "list":
		return runIssueList(ctx, c, args[1:])
	case "ready":
		return runIssueReady(ctx, c, args[1:])
	case "claim":
		return runIssueClaim(ctx, c, args[1:])
	case "heartbeat":
		return runIssueHeartbeat(ctx, c, args[1:])
	case "release":
		return runIssueRelease(ctx, c, args[1:])
	case "handoff":
		return runIssueHandoff(ctx, c, args[1:])
	case "run":
		return runIssueRun(ctx, c, args[1:])
	case "edit":
		return runIssueUpdate(ctx, c, args[1:])
	case "update":
		return runIssueUpdate(ctx, c, args[1:])
	case "close":
		return runIssueClose(ctx, c, args[1:])
	case "operator-close":
		return runIssueOperatorClose(ctx, c, args[1:])
	case "operator-reopen":
		return runIssueOperatorReopen(ctx, c, args[1:])
	case "operator-release":
		return runIssueOperatorRelease(ctx, c, args[1:])
	case "cancel":
		return runIssueCancel(ctx, c, args[1:])
	case "link":
		return runIssueLink(ctx, c, args[1:])
	case "unlink":
		return runIssueUnlink(ctx, c, args[1:])
	case "dependency":
		return runIssueDependency(ctx, c, args[1:])
	case "note":
		return runIssueNote(ctx, c, args[1:])
	case "tag":
		return runIssueTag(ctx, c, args[1:])
	case "events":
		return runIssueEvents(ctx, c, args[1:])
	default:
		return usageErr(issueUsage, fmt.Sprintf("unknown issue subcommand: %s", args[0]))
	}
}

const issueCreateUsage = "Usage: dibs issue create --project <key> --scope-kind <project|repository|worktree> --title <title> [--type <task|bug|feature|epic|chore>] [--repo <repo>] [--worktree <worktree>] [--external-key <key>] [--description <desc>] [--acceptance <criteria>] [--priority <n>] [--tag <namespace/value>]... [--allow-duplicate] [--operation-id <id> | --retry-last]"

func runIssueCreate(ctx context.Context, c *client.Client, args []string) error {
	if hasHelpFlag(args) {
		fmt.Println(issueCreateUsage)
		return nil
	}
	if len(args) < 4 {
		return usageErr(issueCreateUsage, "")
	}

	var req core.CreateIssueRequest
	allowDuplicate := false
	retryLast := false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--allow-duplicate":
			allowDuplicate = true
		case "--retry-last":
			retryLast = true
		case "--operation-id":
			if i+1 < len(args) {
				req.OperationID = args[i+1]
				i++
			}
		case "--project":
			if i+1 < len(args) {
				req.Project = args[i+1]
				i++
			}
		case "--scope-kind":
			if i+1 < len(args) {
				req.ScopeKind = args[i+1]
				i++
			}
		case "--type":
			if i+1 < len(args) {
				req.IssueType = args[i+1]
				i++
			}
		case "--title":
			if i+1 < len(args) {
				req.Title = args[i+1]
				i++
			}
		case "--external-key":
			if i+1 < len(args) {
				req.ExternalKey = args[i+1]
				i++
			}
		case "--repo":
			if i+1 < len(args) {
				req.Repo = args[i+1]
				i++
			}
		case "--worktree":
			if i+1 < len(args) {
				req.Worktree = args[i+1]
				i++
			}
		case "--description":
			if i+1 < len(args) {
				req.Description = args[i+1]
				i++
			}
		case "--acceptance":
			if i+1 < len(args) {
				req.AcceptanceCriteria = args[i+1]
				i++
			}
		case "--priority":
			if i+1 < len(args) {
				fmt.Sscanf(args[i+1], "%d", &req.Priority)
				i++
			}
		case "--tag":
			if i+1 < len(args) {
				req.Tags = append(req.Tags, args[i+1])
				i++
			}
		}
	}

	actor, err := resolveActor("")
	if err != nil {
		return usageErr(issueCreateUsage, err.Error())
	}
	req.Actor = actor
	if retryLast && req.OperationID != "" {
		return usageErr(issueCreateUsage, "--retry-last and --operation-id are mutually exclusive")
	}
	if err := core.ValidateOperationID(req.OperationID); err != nil {
		return usageErr(issueCreateUsage, err.Error())
	}
	journalTarget := req.Project + "-" + core.OperationFingerprint(core.CreateFingerprintFields(req.Project, req))
	journalPath := ""
	if retryLast {
		journaled, path, err := readJournaledOperationID("create", journalTarget)
		if err != nil {
			return err
		}
		if journaled == "" {
			return fmt.Errorf("no journaled create operation for this request (expected %s)", path)
		}
		req.OperationID, journalPath = journaled, path
	}
	if req.OperationID == "" {
		req.OperationID = newOperationID()
	}
	previousOperationID := ""
	journaled := false
	if journalPath == "" {
		path, previous, err := journalOperationID("create", journalTarget, req.OperationID)
		if err != nil {
			return err
		}
		journalPath, previousOperationID, journaled = path, previous, true
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
					fmt.Fprintf(os.Stderr, "Warning: an %s issue with the same title already exists: %s (%s)\n", iss.Status, iss.ShortID, iss.ID)
					break
				}
			}
		}
	}

	issue, err := c.CreateIssue(ctx, req)
	if err != nil {
		if createFailureDefinitelyRejected(err) {
			if journaled {
				restoreJournaledOperationID(journalPath, previousOperationID)
			}
			fail(err)
		}
		guidance := fmt.Sprintf("create outcome is unconfirmed; operation_id: %s; journaled at: %s; retry safely: dibs issue create [same arguments] --retry-last", req.OperationID, journalPath)
		var clientErr *client.ClientError
		if errors.As(err, &clientErr) {
			fail(&client.ClientError{Code: clientErr.Code, Message: clientErr.Message + "; " + guidance})
		}
		if jsonOutput {
			fail(fmt.Errorf("%s: %w", guidance, err))
		}
		fmt.Fprintln(os.Stderr, guidance)
		fail(err)
	}
	if jsonOutput {
		json.NewEncoder(os.Stdout).Encode(struct {
			core.Issue
			OperationID string `json:"operation_id"`
		}{issue, req.OperationID})
		return nil
	}
	fmt.Printf("Operation ID: %s\n", req.OperationID)
	printIssue(issue)
	return nil
}

// Only a documented create rejection proves that the transaction did not
// commit. A server internal_error can follow an uncertain Commit result.
func createFailureDefinitelyRejected(err error) bool {
	var clientErr *client.ClientError
	if !errors.As(err, &clientErr) {
		return false
	}
	switch clientErr.Code {
	case "validation_failed", "not_found", "idempotency_conflict", "short_id_taken":
		return true
	default:
		return false
	}
}

const issueGetUsage = "Usage: dibs issue get <issue-id-or-short-id> [--full]"

func runIssueGet(ctx context.Context, c *client.Client, args []string) error {
	if hasHelpFlag(args) {
		fmt.Println(issueGetUsage)
		return nil
	}
	if len(args) < 1 {
		return usageErr(issueGetUsage, "")
	}

	fullView := false
	issueID := ""

	for i := 0; i < len(args); i++ {
		if args[i] == "--full" {
			fullView = true
		} else if issueID == "" {
			issueID = args[i]
		}
	}

	if issueID == "" {
		return usageErr(issueGetUsage, "")
	}

	issue, lease, err := c.GetIssue(ctx, issueID)
	if err != nil {
		fail(err)
	}

	var events []core.Event
	var notes []core.Note
	var links []core.ArtifactRef

	if fullView {
		events, err = c.ListEvents(ctx, issueID)
		if err != nil {
			fail(err)
		}

		notes, err = c.ListNotes(ctx, issueID)
		if err != nil {
			fail(err)
		}

		links, err = c.ListIssueLinks(ctx, issueID)
		if err != nil {
			fail(err)
		}
	}

	if jsonOutput {
		resp := map[string]any{
			"issue": issue,
		}
		if fullView {
			resp["events"] = events
			resp["notes"] = notes
			resp["links"] = links
		}
		if lease != nil {
			resp["lease"] = lease
		}
		json.NewEncoder(os.Stdout).Encode(resp)
		return nil
	}

	if fullView {
		printIssueFull(issue, lease, events, notes, links)
	} else {
		printIssueDetailed(issue, lease)
	}
	return nil
}

const issueListUsage = `Usage: dibs issue list [filters]
       dibs ls [filters]

Filters:
  --project <key[,key...]>       Project key(s); values are ORed
  --type <task|bug|feature|epic|chore[,..]>
                                  Issue type(s); values are ORed
  --status <status[,status...]>  Status value(s); values are ORed
  --repo <repo>                  Repository ID or logical name
  --worktree <worktree>          Worktree ID or path
  --assignee <actor>             Exact assignee
  --external-key <key>           Exact external key
  --tag <namespace/value[,..]>   Tag(s); an issue must carry every listed
                                  tag (AND), repeatable
  --limit <n> --offset <n>       Page size (max 1000) and zero-based offset
  --columns <key[,key...]>       Table columns to display, in order
                                  (default: id,short,status,type,title,
                                  assignee,claimed,blocked_by,deps,tags)
`

func runIssueList(ctx context.Context, c *client.Client, args []string) error {
	params, columns, help, err := parseIssueListArgs(args)
	if err != nil {
		return err
	}
	if help {
		fmt.Fprint(os.Stdout, issueListUsage)
		return nil
	}

	issues, err := c.ListIssuesWithFilters(ctx, params)
	if err != nil {
		fail(err)
	}
	if jsonOutput {
		json.NewEncoder(os.Stdout).Encode(issues)
		return nil
	}
	if len(issues) == 0 {
		fmt.Println("No issues found.")
		return nil
	}
	printIssuesTable(issues, columns)
	return nil
}

func parseIssueListArgs(args []string) (core.IssueListParams, []string, bool, error) {
	var params core.IssueListParams
	var columns []string
	for i := 0; i < len(args); i++ {
		flag := args[i]
		if flag == "--help" || flag == "-h" {
			return core.IssueListParams{}, nil, true, nil
		}
		switch flag {
		case "--project", "--status", "--type", "--repo", "--worktree", "--assignee", "--external-key", "--tag", "--limit", "--offset", "--columns":
		default:
			return core.IssueListParams{}, nil, false, fmt.Errorf("unknown flag: %s", flag)
		}

		value, err := issueListFlagValue(args, i, flag)
		if err != nil {
			return core.IssueListParams{}, nil, false, err
		}

		switch flag {
		case "--project":
			var values []string
			values, err = core.NormalizeIssueListValues([]string{value})
			params.Projects = append(params.Projects, values...)
		case "--status":
			var values []string
			values, err = core.NormalizeIssueListValues([]string{value})
			params.Statuses = append(params.Statuses, values...)
		case "--type":
			var values []string
			values, err = core.NormalizeIssueListValues([]string{value})
			params.IssueTypes = append(params.IssueTypes, values...)
		case "--repo":
			params.Repo = value
		case "--worktree":
			params.Worktree = value
		case "--assignee":
			params.Assignee = value
		case "--external-key":
			params.ExternalKey = value
		case "--tag":
			var values []string
			values, err = core.NormalizeIssueListValues([]string{value})
			params.Tags = append(params.Tags, values...)
		case "--limit", "--offset":
			n, parseErr := strconv.Atoi(value)
			if parseErr != nil || n < 0 || (flag == "--limit" && n > 1000) {
				return core.IssueListParams{}, nil, false, fmt.Errorf("%s requires an integer", flag)
			}
			if flag == "--limit" {
				params.Limit = n
			} else {
				params.Offset = n
			}
		case "--columns":
			columns, err = parseIssueColumns(value)
		}
		if err != nil {
			return core.IssueListParams{}, nil, false, fmt.Errorf("%s %w", flag, err)
		}
		i++
	}
	return params, columns, false, nil
}

func issueListFlagValue(args []string, index int, flag string) (string, error) {
	if index+1 >= len(args) || strings.HasPrefix(args[index+1], "--") {
		return "", fmt.Errorf("%s requires a value", flag)
	}
	return args[index+1], nil
}

const issueReadyUsage = "Usage: dibs issue ready [--project <key>] [--repo <repo>] [--tag <namespace/value[,..]>]... [--columns <key[,key...]>]"

func runIssueReady(ctx context.Context, c *client.Client, args []string) error {
	if hasHelpFlag(args) {
		fmt.Println(issueReadyUsage)
		return nil
	}
	project := ""
	repo := ""
	var rawTags []string
	var columns []string
	for i := 0; i < len(args); i++ {
		if args[i] == "--project" && i+1 < len(args) {
			project = args[i+1]
			i++
		} else if args[i] == "--repo" && i+1 < len(args) {
			repo = args[i+1]
			i++
		} else if args[i] == "--tag" && i+1 < len(args) {
			rawTags = append(rawTags, args[i+1])
			i++
		} else if args[i] == "--columns" && i+1 < len(args) {
			var err error
			columns, err = parseIssueColumns(args[i+1])
			if err != nil {
				return err
			}
			i++
		}
	}
	tags, err := core.NormalizeIssueListValues(rawTags)
	if err != nil {
		return fmt.Errorf("--tag %w", err)
	}

	issues, err := c.ListReadyIssues(ctx, project, repo, tags)
	if err != nil {
		fail(err)
	}
	if jsonOutput {
		json.NewEncoder(os.Stdout).Encode(issues)
		return nil
	}
	if len(issues) == 0 {
		fmt.Println("No ready issues found.")
		return nil
	}
	printIssuesTable(issues, columns)
	return nil
}

const issueClaimUsage = "Usage: dibs issue claim <issue-id> [--holder <name>|--actor <name>] [--ttl <seconds>] [--session-id <id>] [--invocation-mode interactive|scheduled|unknown] [--operation-id <id>] [--retry-last]\n" +
	"\n" +
	"  --operation-id  reuse an explicit idempotency key; an exact retry returns the\n" +
	"                  original claim outcome instead of claiming again\n" +
	"  --retry-last    retry the previous claim for this issue using its journaled\n" +
	"                  operation ID; use this when a claim's response was lost\n" + lifecycleHint

func runIssueClaim(ctx context.Context, c *client.Client, args []string) error {
	if hasHelpFlag(args) {
		fmt.Println(issueClaimUsage)
		return nil
	}
	if len(args) < 1 {
		return usageErr(issueClaimUsage, "")
	}

	issueID := args[0]
	holder := ""
	ttl := 3600
	sessionID := ""
	invocationMode := ""
	operationID := ""
	retryLast := false
	holderSet, ttlSet, sessionSet, modeSet := false, false, false, false

	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--holder", "--actor":
			if i+1 < len(args) {
				holder = args[i+1]
				holderSet = true
				i++
			}
		case "--ttl":
			if i+1 < len(args) {
				fmt.Sscanf(args[i+1], "%d", &ttl)
				ttlSet = true
				i++
			}
		case "--session-id":
			if i+1 < len(args) {
				sessionID = args[i+1]
				sessionSet = true
				i++
			}
		case "--invocation-mode":
			if i+1 < len(args) {
				invocationMode = args[i+1]
				modeSet = true
				i++
			}
		case "--operation-id":
			if i+1 < len(args) {
				operationID = args[i+1]
				i++
			}
		case "--retry-last":
			retryLast = true
		}
	}

	if invocationMode != "" {
		normalized, nerr := core.NormalizeInvocationMode(invocationMode)
		if nerr != nil {
			return usageErr(issueClaimUsage, nerr.Error())
		}
		invocationMode = normalized
	}
	// Resolve the idempotency key before sending. --retry-last reuses the key
	// journaled by the previous attempt, which is what makes a lost response
	// recoverable; anything else is a new logical operation and gets a new key.
	journalPath := ""
	var saved claimJournalRecord
	if retryLast || operationID != "" {
		var path string
		var jerr error
		saved, path, jerr = readJournaledClaimOperation(issueID)
		if jerr != nil {
			return jerr
		}
		journalPath = path
	}
	if retryLast {
		if operationID != "" {
			return usageErr(issueClaimUsage, "--retry-last and --operation-id are mutually exclusive")
		}
		if saved.OperationID == "" {
			return fmt.Errorf("no journaled claim operation for %s (expected %s)", issueID, journalPath)
		}
		operationID = saved.OperationID
	}
	replaySaved := saved.OperationID != "" && saved.OperationID == operationID
	if replaySaved && saved.Holder != "" {
		if holderSet && holder != saved.Holder || ttlSet && ttl != saved.TTLSeconds || sessionSet && sessionID != saved.SessionID {
			return usageErr(issueClaimUsage, "claim flags differ from the journaled operation")
		}
		if modeSet {
			requested, _ := core.NormalizeInvocationMode(invocationMode)
			stored, _ := core.NormalizeInvocationMode(saved.InvocationMode)
			if requested != stored {
				return usageErr(issueClaimUsage, "--invocation-mode differs from the journaled claim")
			}
		}
		holder, ttl, sessionID, invocationMode = saved.Holder, saved.TTLSeconds, saved.SessionID, saved.InvocationMode
	} else {
		var err error
		holder, err = resolveActor(holder)
		if err != nil {
			return usageErr(issueClaimUsage, err.Error())
		}
		if !replaySaved && sessionID == "" {
			host, _ := os.Hostname()
			sessionID = manualClaimSessionID("", host, claimCallerPID())
		}
	}
	if operationID == "" {
		operationID = newOperationID()
	}
	previousOperationID := ""
	journaled := false
	if !replaySaved {
		path, previous, jerr := journalClaimOperation(issueID, core.ClaimRequest{
			Holder: holder, TTLSeconds: ttl, SessionID: sessionID,
			InvocationMode: invocationMode, OperationID: operationID,
		})
		if jerr != nil {
			return fmt.Errorf("%s", jerr)
		}
		journalPath = path
		previousOperationID = previous
		journaled = true
	}

	resp, err := c.ClaimIssueWithRequest(ctx, issueID, core.ClaimRequest{
		Holder:         holder,
		TTLSeconds:     ttl,
		SessionID:      sessionID,
		InvocationMode: invocationMode,
		OperationID:    operationID,
	})
	if err != nil {
		// A typed error means the daemon answered: the claim definitively did
		// not commit, so this operation ID is worthless and must not keep
		// displacing an earlier key whose outcome is still unknown.
		var clientErr *client.ClientError
		if errors.As(err, &clientErr) {
			if journaled {
				restoreJournaledOperationID(journalPath, previousOperationID)
			}
			fail(err)
		}

		// No answer arrived. This is the ambiguous case the operation ID
		// exists for: the claim may already have committed. Point at the
		// durable copy rather than letting the caller assume nothing happened.
		fmt.Fprintf(os.Stderr, "\nclaim outcome is unconfirmed; it may already have committed.\n")
		fmt.Fprintf(os.Stderr, "operation_id: %s\n", operationID)
		fmt.Fprintf(os.Stderr, "journaled at: %s\n", journalPath)
		fmt.Fprintf(os.Stderr, "retry safely: dibs issue claim %s --retry-last\n", issueID)
		fail(err)
	}
	if jsonOutput {
		json.NewEncoder(os.Stdout).Encode(resp)
		return nil
	}
	fmt.Printf("Operation ID: %s\n", operationID)
	fmt.Printf("Lease Token: %s\n", resp.LeaseToken)
	fmt.Printf("Attempt ID:  %s\n", resp.AttemptID)
	fmt.Printf("Generation:  %d\n", resp.LeaseGeneration)
	fmt.Printf("Expires At:  %s\n", resp.ExpiresAt)
	fmt.Printf("Version:     %d  (use this for --expected-version on close/handoff, not a value read from `issue get`)\n", resp.Version)
	return nil
}

func manualClaimSessionID(explicit, host string, pid int) string {
	if explicit != "" {
		return explicit
	}
	if len(host) == 0 || len(host) > 253 || pid <= 1 {
		return ""
	}
	for _, c := range host {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '.' || c == '-' || c == '_') {
			return ""
		}
	}
	return fmt.Sprintf("dibs-claim:v1:%s:%d", host, pid)
}

func claimCallerPID() int {
	if agent := getParentAgent(); agent != "" {
		if split := strings.LastIndexByte(agent, '-'); split >= 0 {
			if pid, err := strconv.Atoi(agent[split+1:]); err == nil && pid > 1 {
				return pid
			}
		}
	}
	// /proc is unavailable on macOS. The immediate parent is still a useful
	// diagnostic, though it may exit while the manual lease remains active.
	return os.Getppid()
}

const issueHeartbeatUsage = "Usage: dibs issue heartbeat <issue-id> --lease-generation <generation> [--ttl <seconds>] [--operation-id <id>] [--lease-token <token>]\n" + lifecycleHint

func runIssueHeartbeat(ctx context.Context, c *client.Client, args []string) error {
	if hasHelpFlag(args) {
		fmt.Println(issueHeartbeatUsage)
		return nil
	}
	if len(args) < 1 {
		return usageErr(issueHeartbeatUsage, "")
	}

	issueID := args[0]
	leaseToken := ""
	var leaseGeneration int64
	ttl := 3600
	operationID := ""

	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--lease-token":
			if i+1 < len(args) {
				leaseToken = args[i+1]
				i++
			}
		case "--lease-generation":
			if i+1 < len(args) {
				fmt.Sscanf(args[i+1], "%d", &leaseGeneration)
				i++
			}
		case "--ttl":
			if i+1 < len(args) {
				fmt.Sscanf(args[i+1], "%d", &ttl)
				i++
			}
		case "--operation-id":
			if i+1 < len(args) {
				operationID = args[i+1]
				i++
			}
		}
	}

	if leaseToken == "" {
		var err error
		leaseToken, err = leaseTokenFromEnvironment()
		if err != nil {
			return usageErr(issueHeartbeatUsage, err.Error())
		}
	}
	if leaseGeneration <= 0 {
		return usageErr(issueHeartbeatUsage, "--lease-generation is required")
	}

	expiresAt, err := c.HeartbeatLeaseWithOperation(ctx, issueID, core.HeartbeatRequest{LeaseToken: leaseToken, LeaseGeneration: leaseGeneration, TTLSeconds: ttl, OperationID: operationID})
	if err != nil {
		fail(err)
	}
	if jsonOutput {
		json.NewEncoder(os.Stdout).Encode(map[string]string{"expires_at": expiresAt})
		return nil
	}
	fmt.Printf("Expires At: %s\n", expiresAt)
	return nil
}

const issueReleaseUsage = "Usage: dibs issue release <issue-id> --lease-generation <generation> [--operation-id <id>] [--lease-token <token>]\n" + lifecycleHint

func runIssueRelease(ctx context.Context, c *client.Client, args []string) error {
	if hasHelpFlag(args) {
		fmt.Println(issueReleaseUsage)
		return nil
	}
	if len(args) < 1 {
		return usageErr(issueReleaseUsage, "")
	}

	issueID := args[0]
	leaseToken := ""
	var leaseGeneration int64
	operationID := ""

	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--lease-token":
			if i+1 < len(args) {
				leaseToken = args[i+1]
				i++
			}
		case "--lease-generation":
			if i+1 < len(args) {
				fmt.Sscanf(args[i+1], "%d", &leaseGeneration)
				i++
			}
		case "--operation-id":
			if i+1 < len(args) {
				operationID = args[i+1]
				i++
			}
		}
	}

	if leaseToken == "" {
		var err error
		leaseToken, err = leaseTokenFromEnvironment()
		if err != nil {
			return usageErr(issueReleaseUsage, err.Error())
		}
	}
	if leaseGeneration <= 0 {
		return usageErr(issueReleaseUsage, "--lease-generation is required")
	}

	if err := c.ReleaseLeaseWithOperation(ctx, issueID, core.ReleaseRequest{LeaseToken: leaseToken, LeaseGeneration: leaseGeneration, OperationID: operationID}); err != nil {
		fail(err)
	}
	if jsonOutput {
		fmt.Println(`{"status":"ok"}`)
		return nil
	}
	fmt.Println("Lease released.")
	return nil
}

const issueHandoffUsage = "Usage: dibs issue handoff <issue-id> --lease-generation <generation> --note \"HANDOFF: next steps\" [--invocation-mode interactive|scheduled|unknown] [--operation-id <id>] [--lease-token <token>]\n" + lifecycleHint

func runIssueHandoff(ctx context.Context, c *client.Client, args []string) error {
	if hasHelpFlag(args) {
		fmt.Println(issueHandoffUsage)
		return nil
	}
	if len(args) < 1 {
		return usageErr(issueHandoffUsage, "")
	}

	issueID := args[0]
	leaseToken := ""
	var leaseGeneration int64
	note := ""
	invocationMode := ""
	operationID := ""
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--lease-generation":
			if i+1 < len(args) {
				fmt.Sscanf(args[i+1], "%d", &leaseGeneration)
				i++
			}
		case "--lease-token":
			if i+1 < len(args) {
				leaseToken = args[i+1]
				i++
			}
		case "--note":
			if i+1 < len(args) {
				note = args[i+1]
				i++
			}
		case "--invocation-mode":
			if i+1 < len(args) {
				invocationMode = args[i+1]
				i++
			}
		case "--operation-id":
			if i+1 < len(args) {
				operationID = args[i+1]
				i++
			}
		default:
			return usageErr(issueHandoffUsage, fmt.Sprintf("unknown flag: %s", args[i]))
		}
	}
	if leaseToken == "" {
		var err error
		leaseToken, err = leaseTokenFromEnvironment()
		if err != nil {
			return usageErr(issueHandoffUsage, err.Error())
		}
	}
	if leaseGeneration <= 0 {
		return usageErr(issueHandoffUsage, "--lease-generation is required")
	}
	if err := core.ValidateHandoffRequest(core.HandoffRequest{Note: note}); err != nil {
		return usageErr(issueHandoffUsage, err.Error())
	}

	if invocationMode != "" {
		normalized, nerr := core.NormalizeInvocationMode(invocationMode)
		if nerr != nil {
			return usageErr(issueHandoffUsage, nerr.Error())
		}
		invocationMode = normalized
	}

	resp, err := c.HandoffLeaseWithOperation(ctx, issueID, core.HandoffRequest{LeaseToken: leaseToken, LeaseGeneration: leaseGeneration, Note: note, InvocationMode: invocationMode, OperationID: operationID})
	if err != nil {
		fail(err)
	}
	if jsonOutput {
		json.NewEncoder(os.Stdout).Encode(resp)
		return nil
	}
	fmt.Printf("Handoff note recorded: %s\nLease released.\n", resp.Note.ID)
	return nil
}

const issueUpdateUsage = "Usage: dibs issue update <issue-id> [--title ...] [--type <task|bug|feature|epic|chore>] [--external-key ...] [--description ...] [--acceptance ...] [--priority N] [--assignee ...] [--status ...] [--expected-version N|latest] [--force] [--lease-generation <generation>] [--release] [--operation-id <id>] [--lease-token <token>]\n  --operation-id requires a numeric --expected-version for exact replay\n  Agents never pass a lease token in argv; use issue run or DIBS_LEASE_TOKEN_FILE."

func runIssueUpdate(ctx context.Context, c *client.Client, args []string) error {
	if hasHelpFlag(args) {
		fmt.Println(issueUpdateUsage)
		return nil
	}
	if len(args) < 1 {
		return usageErr(issueUpdateUsage, "")
	}

	issueID := args[0]
	var req core.UpdateIssueRequest
	req.ExpectedVersion = -1

	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--title":
			if i+1 < len(args) {
				req.Title = args[i+1]
				i++
			}
		case "--type":
			if i+1 < len(args) {
				req.IssueType = args[i+1]
				i++
			}
		case "--external-key":
			if i+1 < len(args) {
				req.ExternalKey = args[i+1]
				i++
			}
		case "--description":
			if i+1 < len(args) {
				req.Description = args[i+1]
				i++
			}
		case "--acceptance":
			if i+1 < len(args) {
				req.AcceptanceCriteria = args[i+1]
				i++
			}
		case "--priority":
			if i+1 < len(args) {
				fmt.Sscanf(args[i+1], "%d", &req.Priority)
				i++
			}
		case "--assignee":
			if i+1 < len(args) {
				req.Assignee = args[i+1]
				i++
			}
		case "--status":
			if i+1 < len(args) {
				req.Status = args[i+1]
				i++
			}
		case "--expected-version":
			if i+1 < len(args) {
				if args[i+1] == "latest" {
					req.ExpectedVersion = -1
				} else {
					fmt.Sscanf(args[i+1], "%d", &req.ExpectedVersion)
				}
				i++
			}
		case "--lease-token":
			if i+1 < len(args) {
				req.LeaseToken = args[i+1]
				i++
			}
		// Paired with --lease-token: the daemon fences a leased issue on both
		// (afc-105). An unleased issue needs neither, so this stays optional
		// and is only required alongside a token.
		case "--lease-generation":
			if i+1 < len(args) {
				fmt.Sscanf(args[i+1], "%d", &req.LeaseGeneration)
				i++
			}
		case "--release":
			req.ReleaseLease = true
		case "--operation-id":
			if i+1 < len(args) {
				req.OperationID = args[i+1]
				i++
			}
		case "--force":
			req.ExpectedVersion = -1
		}
	}

	// An operation retry must send the original expected version, not resolve
	// the issue's later version after the first response was lost.
	if req.OperationID != "" && req.ExpectedVersion < 0 {
		return usageErr(issueUpdateUsage, "--operation-id requires an explicit numeric --expected-version")
	}
	// Auto-resolve the version when omitted, --expected-version latest, or --force.
	if err := resolveExpectedVersion(ctx, c, issueUpdateUsage, issueID, &req.ExpectedVersion); err != nil {
		return err
	}
	if req.ExpectedVersion < 0 {
		return usageErr(issueUpdateUsage, "--expected-version is required")
	}

	actor, err := resolveActor("")
	if err != nil {
		return usageErr(issueUpdateUsage, err.Error())
	}
	req.Actor = actor

	if req.LeaseToken == "" && req.LeaseGeneration > 0 {
		var tokenErr error
		req.LeaseToken, tokenErr = leaseTokenFromEnvironment()
		if tokenErr != nil {
			return usageErr(issueUpdateUsage, tokenErr.Error())
		}
	}
	if req.LeaseToken != "" && req.LeaseGeneration <= 0 {
		return usageErr(issueUpdateUsage, "--lease-generation is required with --lease-token (from `issue claim`)")
	}
	issue, err := c.UpdateIssue(ctx, issueID, req)
	if err != nil {
		fail(err)
	}
	if jsonOutput {
		json.NewEncoder(os.Stdout).Encode(issue)
		return nil
	}
	printIssue(issue)
	return nil
}

const issueCloseUsage = "Usage: dibs issue close <issue-id> --resolution done|cancelled --expected-version N --lease-generation <generation> [--branch <name>] [--pr-url <url>] [--commit-sha <sha>] [--note \"what was done\"] [--invocation-mode interactive|scheduled|unknown] [--operation-id <id>] [--lease-token <token>]\n" + lifecycleHint

func runIssueClose(ctx context.Context, c *client.Client, args []string) error {
	if hasHelpFlag(args) {
		fmt.Println(issueCloseUsage)
		return nil
	}
	if len(args) < 1 {
		return usageErr(issueCloseUsage, "")
	}

	issueID := args[0]
	var req core.CloseIssueRequest
	req.ExpectedVersion = -1

	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--resolution":
			if i+1 < len(args) {
				req.Resolution = args[i+1]
				i++
			}
		case "--branch":
			if i+1 < len(args) {
				req.Branch = args[i+1]
				i++
			}
		case "--pr-url":
			if i+1 < len(args) {
				req.PRURL = args[i+1]
				i++
			}
		case "--commit-sha":
			if i+1 < len(args) {
				req.CommitSHA = args[i+1]
				i++
			}
		case "--expected-version":
			if i+1 < len(args) {
				fmt.Sscanf(args[i+1], "%d", &req.ExpectedVersion)
				i++
			}
		case "--lease-token":
			if i+1 < len(args) {
				req.LeaseToken = args[i+1]
				i++
			}
		case "--lease-generation":
			if i+1 < len(args) {
				fmt.Sscanf(args[i+1], "%d", &req.LeaseGeneration)
				i++
			}
		case "--invocation-mode":
			if i+1 < len(args) {
				req.InvocationMode = args[i+1]
				i++
			}
		case "--note":
			if i+1 < len(args) {
				req.Note = args[i+1]
				i++
			}
		case "--operation-id":
			if i+1 < len(args) {
				req.OperationID = args[i+1]
				i++
			}
		}
	}

	if req.Resolution == "" {
		return usageErr(issueCloseUsage, "--resolution is required (done or cancelled)")
	}
	if req.ExpectedVersion < 0 {
		return usageErr(issueCloseUsage, "--expected-version is required (use the Version from your most recent `issue claim` response)")
	}
	if req.LeaseToken == "" {
		var tokenErr error
		req.LeaseToken, tokenErr = leaseTokenFromEnvironment()
		if tokenErr != nil {
			return usageErr(issueCloseUsage, tokenErr.Error())
		}
	}
	if req.LeaseGeneration <= 0 {
		return usageErr(issueCloseUsage, "--lease-generation is required (from `issue claim`)")
	}

	actor, err := resolveActor("")
	if err != nil {
		return usageErr(issueCloseUsage, err.Error())
	}
	req.Actor = actor
	if req.InvocationMode != "" {
		normalized, nerr := core.NormalizeInvocationMode(req.InvocationMode)
		if nerr != nil {
			return usageErr(issueCloseUsage, nerr.Error())
		}
		req.InvocationMode = normalized
	}

	result, err := c.CloseIssue(ctx, issueID, req)
	if err != nil {
		fail(err)
	}
	if jsonOutput {
		json.NewEncoder(os.Stdout).Encode(result)
		return nil
	}
	fmt.Println("Issue closed.")
	if result.Branch != "" {
		fmt.Printf("Branch:      %s\n", result.Branch)
	}
	if result.PRURL != "" {
		fmt.Printf("PR URL:      %s\n", result.PRURL)
	}
	if result.CommitSHA != "" {
		fmt.Printf("Commit SHA:  %s\n", result.CommitSHA)
	}
	if result.ExternalKey != "" {
		fmt.Printf("External:    %s\n", result.ExternalKey)
	}
	return nil
}

const issueOperatorCloseUsage = "Usage: dibs issue operator-close <issue-id> --resolution done|cancelled [--expected-version N|latest] [--force] --reason \"why operator closure is needed\" [--branch \u003cname\u003e] [--pr-url \u003curl\u003e] [--commit-sha \u003csha\u003e] [--note \"what was done\"]\n" + lifecycleHint

func runIssueOperatorClose(ctx context.Context, c *client.Client, args []string) error {
	if hasHelpFlag(args) {
		fmt.Println(issueOperatorCloseUsage)
		return nil
	}
	if len(args) < 1 {
		return usageErr(issueOperatorCloseUsage, "")
	}

	issueID := args[0]
	req := core.OperatorCloseIssueRequest{ExpectedVersion: -1}
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--resolution":
			if i+1 < len(args) {
				req.Resolution = args[i+1]
				i++
			}
		case "--branch":
			if i+1 < len(args) {
				req.Branch = args[i+1]
				i++
			}
		case "--pr-url":
			if i+1 < len(args) {
				req.PRURL = args[i+1]
				i++
			}
		case "--commit-sha":
			if i+1 < len(args) {
				req.CommitSHA = args[i+1]
				i++
			}
		case "--expected-version":
			if i+1 < len(args) {
				if args[i+1] == "latest" {
					req.ExpectedVersion = -1
				} else {
					fmt.Sscanf(args[i+1], "%d", &req.ExpectedVersion)
				}
				i++
			}
		case "--reason":
			if i+1 < len(args) {
				req.Reason = args[i+1]
				i++
			}
		case "--note":
			if i+1 < len(args) {
				req.Note = args[i+1]
				i++
			}
		case "--force":
			req.ExpectedVersion = -1
		default:
			return usageErr(issueOperatorCloseUsage, fmt.Sprintf("unknown flag: %s", args[i]))
		}
	}
	if req.Resolution == "" {
		return usageErr(issueOperatorCloseUsage, "--resolution is required (done or cancelled)")
	}
	// Auto-resolve the version when omitted, --expected-version latest, or --force.
	if err := resolveExpectedVersion(ctx, c, issueOperatorCloseUsage, issueID, &req.ExpectedVersion); err != nil {
		return err
	}
	if req.ExpectedVersion <= 0 {
		return usageErr(issueOperatorCloseUsage, "--expected-version is required")
	}
	if strings.TrimSpace(req.Reason) == "" {
		return usageErr(issueOperatorCloseUsage, "--reason is required")
	}
	actor, err := resolveActor("")
	if err != nil {
		return usageErr(issueOperatorCloseUsage, err.Error())
	}
	req.Actor = actor

	token := config.EnvOrDefault("DIBS_OPERATOR_TOKEN", "AF_OPERATOR_TOKEN", "")
	if token == "" {
		return usageErr(issueOperatorCloseUsage, "DIBS_OPERATOR_TOKEN environment variable is required")
	}
	c.SetOperatorToken(token)

	result, err := c.OperatorCloseIssue(ctx, issueID, req)
	if err != nil {
		fail(err)
	}
	if jsonOutput {
		json.NewEncoder(os.Stdout).Encode(result)
		return nil
	}
	fmt.Println("Issue closed by operator.")
	if result.Branch != "" {
		fmt.Printf("Branch:      %s\n", result.Branch)
	}
	if result.PRURL != "" {
		fmt.Printf("PR URL:      %s\n", result.PRURL)
	}
	if result.CommitSHA != "" {
		fmt.Printf("Commit SHA:  %s\n", result.CommitSHA)
	}
	if result.ExternalKey != "" {
		fmt.Printf("External:    %s\n", result.ExternalKey)
	}
	return nil
}

const issueOperatorReopenUsage = "Usage: dibs issue operator-reopen <issue-id> [--expected-version N|latest] [--force] --reason \"why work is reopening\"\n" + lifecycleHint

func runIssueOperatorReopen(ctx context.Context, c *client.Client, args []string) error {
	if hasHelpFlag(args) {
		fmt.Println(issueOperatorReopenUsage)
		return nil
	}
	if len(args) < 1 {
		return usageErr(issueOperatorReopenUsage, "")
	}

	issueID := args[0]
	req := core.OperatorReopenIssueRequest{ExpectedVersion: -1}
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--expected-version":
			if i+1 < len(args) {
				if args[i+1] == "latest" {
					req.ExpectedVersion = -1
				} else {
					fmt.Sscanf(args[i+1], "%d", &req.ExpectedVersion)
				}
				i++
			}
		case "--reason":
			if i+1 < len(args) {
				req.Reason = args[i+1]
				i++
			}
		case "--force":
			req.ExpectedVersion = -1
		default:
			return usageErr(issueOperatorReopenUsage, fmt.Sprintf("unknown flag: %s", args[i]))
		}
	}
	if strings.TrimSpace(req.Reason) == "" {
		return usageErr(issueOperatorReopenUsage, "--reason is required")
	}
	// Auto-resolve the version when omitted, --expected-version latest, or --force.
	if err := resolveExpectedVersion(ctx, c, issueOperatorReopenUsage, issueID, &req.ExpectedVersion); err != nil {
		return err
	}
	if req.ExpectedVersion <= 0 {
		return usageErr(issueOperatorReopenUsage, "--expected-version is required")
	}
	actor, err := resolveActor("")
	if err != nil {
		return usageErr(issueOperatorReopenUsage, err.Error())
	}
	req.Actor = actor

	token := config.EnvOrDefault("DIBS_OPERATOR_TOKEN", "AF_OPERATOR_TOKEN", "")
	if token == "" {
		return usageErr(issueOperatorReopenUsage, "DIBS_OPERATOR_TOKEN environment variable is required")
	}
	c.SetOperatorToken(token)

	issue, err := c.OperatorReopenIssue(ctx, issueID, req)
	if err != nil {
		fail(err)
	}
	if jsonOutput {
		json.NewEncoder(os.Stdout).Encode(issue)
		return nil
	}
	fmt.Println("Issue reopened by operator.")
	return nil
}

const issueOperatorReleaseUsage = "Usage: dibs issue operator-release <issue-id> [--expected-version N|latest] [--force] --reason \"why the lease is being force-cleared\"\n" +
	"Recovers a stuck in_progress claim whose lease token was lost before its TTL expired: clears the lease and returns the issue to open, without closing it.\n" + lifecycleHint

func runIssueOperatorRelease(ctx context.Context, c *client.Client, args []string) error {
	if hasHelpFlag(args) {
		fmt.Println(issueOperatorReleaseUsage)
		return nil
	}
	if len(args) < 1 {
		return usageErr(issueOperatorReleaseUsage, "")
	}

	issueID := args[0]
	req := core.OperatorReleaseIssueRequest{ExpectedVersion: -1}
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--expected-version":
			if i+1 < len(args) {
				if args[i+1] == "latest" {
					req.ExpectedVersion = -1
				} else {
					fmt.Sscanf(args[i+1], "%d", &req.ExpectedVersion)
				}
				i++
			}
		case "--reason":
			if i+1 < len(args) {
				req.Reason = args[i+1]
				i++
			}
		case "--force":
			req.ExpectedVersion = -1
		default:
			return usageErr(issueOperatorReleaseUsage, fmt.Sprintf("unknown flag: %s", args[i]))
		}
	}
	if strings.TrimSpace(req.Reason) == "" {
		return usageErr(issueOperatorReleaseUsage, "--reason is required")
	}
	// Auto-resolve the version when omitted, --expected-version latest, or --force.
	if err := resolveExpectedVersion(ctx, c, issueOperatorReleaseUsage, issueID, &req.ExpectedVersion); err != nil {
		return err
	}
	if req.ExpectedVersion <= 0 {
		return usageErr(issueOperatorReleaseUsage, "--expected-version is required")
	}
	actor, err := resolveActor("")
	if err != nil {
		return usageErr(issueOperatorReleaseUsage, err.Error())
	}
	req.Actor = actor

	token := config.EnvOrDefault("DIBS_OPERATOR_TOKEN", "AF_OPERATOR_TOKEN", "")
	if token == "" {
		return usageErr(issueOperatorReleaseUsage, "DIBS_OPERATOR_TOKEN environment variable is required")
	}
	c.SetOperatorToken(token)

	issue, err := c.OperatorReleaseIssue(ctx, issueID, req)
	if err != nil {
		fail(err)
	}
	if jsonOutput {
		json.NewEncoder(os.Stdout).Encode(issue)
		return nil
	}
	fmt.Println("Issue lease force-released by operator; issue is open again.")
	return nil
}

const issueCancelUsage = "Usage: dibs issue cancel <issue-id> [--note \"why cancelled\"]\n" + lifecycleHint

func runIssueCancel(ctx context.Context, c *client.Client, args []string) error {
	if hasHelpFlag(args) {
		fmt.Println(issueCancelUsage)
		return nil
	}
	if len(args) < 1 {
		return usageErr(issueCancelUsage, "")
	}

	issueID := args[0]
	req := core.OperatorCloseIssueRequest{
		Resolution:      "cancelled",
		ExpectedVersion: -1,
		Reason:          "operator cancellation",
	}
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--note":
			if i+1 < len(args) {
				req.Note = args[i+1]
				i++
			}
		default:
			return usageErr(issueCancelUsage, fmt.Sprintf("unknown flag: %s", args[i]))
		}
	}

	// Auto-resolve version (always; cancel has no --expected-version flag).
	issue, _, err := c.GetIssue(ctx, issueID)
	if err != nil {
		return usageErr(issueCancelUsage, fmt.Sprintf("failed to fetch current issue version: %v", err))
	}
	req.ExpectedVersion = issue.Version
	if req.ExpectedVersion <= 0 {
		return usageErr(issueCancelUsage, "failed to resolve issue version")
	}

	act, err := resolveActor("")
	if err != nil {
		return usageErr(issueCancelUsage, err.Error())
	}
	req.Actor = act

	token := config.EnvOrDefault("DIBS_OPERATOR_TOKEN", "AF_OPERATOR_TOKEN", "")
	if token == "" {
		return usageErr(issueCancelUsage, "DIBS_OPERATOR_TOKEN environment variable is required")
	}
	c.SetOperatorToken(token)

	result, err := c.OperatorCloseIssue(ctx, issueID, req)
	if err != nil {
		fail(err)
	}
	if jsonOutput {
		json.NewEncoder(os.Stdout).Encode(result)
		return nil
	}
	fmt.Println("Issue cancelled by operator.")
	if result.ExternalKey != "" {
		fmt.Printf("External:    %s\n", result.ExternalKey)
	}
	return nil
}

const issueLinkUsage = "Usage: dibs issue link <issue-id> [--artifact <id-or-path> | --path <relative-path>] [--repo <name>] [--kind spec] [--relation implements]"

func runIssueLink(ctx context.Context, c *client.Client, args []string) error {
	if hasHelpFlag(args) {
		fmt.Println(issueLinkUsage)
		return nil
	}
	if len(args) < 1 {
		return usageErr(issueLinkUsage, "")
	}

	issueID := args[0]
	var req core.LinkArtifactRequest
	var path, repo, kind string

	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--artifact":
			if i+1 < len(args) {
				req.Artifact = args[i+1]
				i++
			}
		case "--path":
			if i+1 < len(args) {
				path = args[i+1]
				i++
			}
		case "--repo":
			if i+1 < len(args) {
				repo = args[i+1]
				i++
			}
		case "--kind":
			if i+1 < len(args) {
				kind = args[i+1]
				i++
			}
		case "--relation":
			if i+1 < len(args) {
				req.Relation = args[i+1]
				i++
			}
		}
	}

	if req.Artifact == "" && path == "" {
		return usageErr(issueLinkUsage, "--artifact or --path is required")
	}
	if req.Artifact != "" && path != "" {
		return usageErr(issueLinkUsage, "cannot specify both --artifact and --path")
	}

	if path != "" {
		if repo == "" {
			issue, _, err := c.GetIssue(ctx, issueID)
			if err != nil {
				return fmt.Errorf("failed to get issue: %w", err)
			}
			if issue.RepositoryID == "" {
				return usageErr(issueLinkUsage, "issue is not repository-scoped, --repo is required with --path")
			}
			repo = issue.RepositoryID
		}
		if kind == "" {
			kind = "spec"
		}

		art, err := c.CreateArtifact(ctx, core.CreateArtifactRequest{
			Repo:         repo,
			RelativePath: path,
			Kind:         kind,
		})
		if err != nil {
			return fmt.Errorf("failed to upsert artifact: %w", err)
		}
		req.Artifact = art.ID
	}

	if err := c.LinkArtifact(ctx, issueID, req); err != nil {
		fail(err)
	}
	if jsonOutput {
		fmt.Println(`{"status":"ok"}`)
		return nil
	}
	fmt.Println("Artifact linked.")
	return nil
}

const issueUnlinkUsage = "Usage: dibs issue unlink <issue-id> (--path <relative-path> | --artifact <id-or-path>) [--relation implements]"

func runIssueUnlink(ctx context.Context, c *client.Client, args []string) error {
	if hasHelpFlag(args) {
		fmt.Println(issueUnlinkUsage)
		return nil
	}
	if len(args) < 1 {
		return usageErr(issueUnlinkUsage, "")
	}

	issueID := args[0]
	var req core.UnlinkArtifactRequest

	flagValue := func(i int) (string, error) {
		if i+1 >= len(args) || strings.HasPrefix(args[i+1], "--") {
			return "", usageErr(issueUnlinkUsage, fmt.Sprintf("%s requires a value", args[i]))
		}
		return args[i+1], nil
	}

	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--path", "--artifact":
			value, err := flagValue(i)
			if err != nil {
				return err
			}
			req.Artifact = value
			i++
		case "--relation":
			value, err := flagValue(i)
			if err != nil {
				return err
			}
			req.Relation = value
			i++
		}
	}

	if req.Artifact == "" {
		return usageErr(issueUnlinkUsage, "--path or --artifact is required")
	}

	act, err := resolveActor("")
	if err != nil {
		return err
	}
	req.Actor = act

	if err := c.UnlinkArtifact(ctx, issueID, req); err != nil {
		fail(err)
	}
	if jsonOutput {
		fmt.Println(`{"status":"ok"}`)
		return nil
	}
	fmt.Println("Artifact unlinked.")
	return nil
}

const issueDependencyUsage = "Usage: dibs issue dependency <add|remove> <issue-id> --depends-on <other-issue> [--kind blocks|parent|related|discovered-from]"

func runIssueDependency(ctx context.Context, c *client.Client, args []string) error {
	if len(args) < 1 {
		return usageErr(issueDependencyUsage, "")
	}
	if args[0] == "--help" || args[0] == "-h" || args[0] == "help" {
		fmt.Println(issueDependencyUsage)
		return nil
	}

	switch args[0] {
	case "add":
		return runIssueDependencyAdd(ctx, c, args[1:])
	case "remove":
		return runIssueDependencyRemove(ctx, c, args[1:])
	default:
		return usageErr(issueDependencyUsage, fmt.Sprintf("unknown dependency subcommand: %s", args[0]))
	}
}

const dependencyAddUsage = "Usage: dibs issue dependency add <issue-id> (--blocked-by <id> | --blocks <id> | --depends-on <id> [--kind blocks|parent|related|discovered-from])"

// dependencyEdge is the resolved, direction-unambiguous form of a dependency
// command: the issue that owns the stored edge, the issue it depends on, the
// kind, and a human-readable confirmation.
type dependencyEdge struct {
	target    string
	dependsOn string
	kind      string
	message   string
}

// resolveDependencyEdge maps the directional flags of `dependency add` onto the
// single stored edge shape (owner depends_on target, kind). Exactly one of
// --blocked-by, --blocks, or --depends-on must be given, so an author never has
// to reason about which side the word "blocks" refers to.
func resolveDependencyEdge(issueID string, args []string) (dependencyEdge, error) {
	var dependsOn, kind, blockedBy, blocks string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--depends-on":
			if i+1 < len(args) {
				dependsOn = args[i+1]
				i++
			}
		case "--kind":
			if i+1 < len(args) {
				kind = args[i+1]
				i++
			}
		case "--blocked-by":
			if i+1 < len(args) {
				blockedBy = args[i+1]
				i++
			}
		case "--blocks":
			if i+1 < len(args) {
				blocks = args[i+1]
				i++
			}
		}
	}

	forms := 0
	for _, v := range []string{dependsOn, blockedBy, blocks} {
		if v != "" {
			forms++
		}
	}
	if forms == 0 {
		return dependencyEdge{}, fmt.Errorf("one of --blocked-by, --blocks, or --depends-on is required")
	}
	if forms > 1 {
		return dependencyEdge{}, fmt.Errorf("--blocked-by, --blocks, and --depends-on are mutually exclusive")
	}
	if (blockedBy != "" || blocks != "") && kind != "" {
		return dependencyEdge{}, fmt.Errorf("--kind cannot be combined with --blocked-by or --blocks (both mean kind=blocks)")
	}

	edge := dependencyEdge{target: issueID}
	switch {
	case blockedBy != "":
		edge.dependsOn, edge.kind = blockedBy, "blocks"
		edge.message = fmt.Sprintf("%s is now blocked by %s", issueID, blockedBy)
	case blocks != "":
		edge.target, edge.dependsOn, edge.kind = blocks, issueID, "blocks"
		edge.message = fmt.Sprintf("%s is now blocked by %s", blocks, issueID)
	default: // --depends-on
		edge.dependsOn, edge.kind = dependsOn, kind
		switch kind {
		case "blocks":
			edge.message = fmt.Sprintf("%s is now blocked by %s", issueID, dependsOn)
		case "":
			edge.message = fmt.Sprintf("%s now depends on %s", issueID, dependsOn)
		default:
			edge.message = fmt.Sprintf("%s now has a %s dependency on %s", issueID, kind, dependsOn)
		}
	}
	return edge, nil
}

func runIssueDependencyAdd(ctx context.Context, c *client.Client, args []string) error {
	if hasHelpFlag(args) {
		fmt.Println(dependencyAddUsage)
		return nil
	}
	if len(args) < 1 {
		return usageErr(dependencyAddUsage, "")
	}

	edge, err := resolveDependencyEdge(args[0], args[1:])
	if err != nil {
		return usageErr(dependencyAddUsage, err.Error())
	}
	act, err := resolveActor("")
	if err != nil {
		return usageErr(dependencyAddUsage, err.Error())
	}
	if err := c.AddDependency(ctx, edge.target, core.AddDependencyRequest{
		DependsOn: edge.dependsOn,
		Kind:      edge.kind,
		Actor:     act,
	}); err != nil {
		fail(err)
	}
	if jsonOutput {
		fmt.Println(`{"status":"ok"}`)
		return nil
	}
	fmt.Println("Dependency added.")
	fmt.Println(edge.message)
	return nil
}

const dependencyRemoveUsage = "Usage: dibs issue dependency remove <issue-id> (--blocked-by <id> | --blocks <id> | --depends-on <id> [--kind blocks])"

func runIssueDependencyRemove(ctx context.Context, c *client.Client, args []string) error {
	if hasHelpFlag(args) {
		fmt.Println(dependencyRemoveUsage)
		return nil
	}
	if len(args) < 1 {
		return usageErr(dependencyRemoveUsage, "")
	}

	issueID := args[0]
	var dependsOn, blockedBy, blocks string
	kind := "blocks"
	kindSet := false

	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--depends-on":
			if i+1 < len(args) {
				dependsOn = args[i+1]
				i++
			}
		case "--kind":
			if i+1 < len(args) {
				kind = args[i+1]
				kindSet = true
				i++
			}
		case "--blocked-by":
			if i+1 < len(args) {
				blockedBy = args[i+1]
				i++
			}
		case "--blocks":
			if i+1 < len(args) {
				blocks = args[i+1]
				i++
			}
		}
	}

	forms := 0
	for _, v := range []string{dependsOn, blockedBy, blocks} {
		if v != "" {
			forms++
		}
	}
	if forms == 0 {
		return usageErr(dependencyRemoveUsage, "one of --blocked-by, --blocks, or --depends-on is required")
	}
	if forms > 1 {
		return usageErr(dependencyRemoveUsage, "--blocked-by, --blocks, and --depends-on are mutually exclusive")
	}
	if (blockedBy != "" || blocks != "") && kindSet {
		return usageErr(dependencyRemoveUsage, "--kind cannot be combined with --blocked-by or --blocks (both mean kind=blocks)")
	}

	// Mirror the add direction so removal targets the same stored edge.
	target := issueID
	switch {
	case blockedBy != "":
		dependsOn, kind = blockedBy, "blocks"
	case blocks != "":
		target, dependsOn, kind = blocks, issueID, "blocks"
	}

	act, err := resolveActor("")
	if err != nil {
		return usageErr(dependencyRemoveUsage, err.Error())
	}

	if err := c.RemoveDependency(ctx, target, core.RemoveDependencyRequest{
		DependsOn: dependsOn,
		Kind:      kind,
		Actor:     act,
	}); err != nil {
		fail(err)
	}
	if jsonOutput {
		fmt.Println(`{"status":"ok"}`)
		return nil
	}
	fmt.Println("Dependency removed.")
	return nil
}

// ─── Issue Note ─────────────────────────────────────────────────────────────

const issueNoteUsage = "Usage: dibs issue note <add|list> <issue-id> [--author <name> --body <text>]"

func runIssueNote(ctx context.Context, c *client.Client, args []string) error {
	if len(args) < 1 {
		return usageErr(issueNoteUsage, "")
	}
	if args[0] == "--help" || args[0] == "-h" || args[0] == "help" {
		fmt.Println(issueNoteUsage)
		return nil
	}

	switch args[0] {
	case "add":
		return runIssueNoteAdd(ctx, c, args[1:])
	case "list":
		return runIssueNoteList(ctx, c, args[1:])
	default:
		return usageErr(issueNoteUsage, fmt.Sprintf("unknown note subcommand: %s", args[0]))
	}
}

const issueNoteAddUsage = "Usage: dibs issue note add <issue-id> [--author <name>|--actor <name>] --body <text> [--invocation-mode interactive|scheduled|unknown]"

func runIssueNoteAdd(ctx context.Context, c *client.Client, args []string) error {
	if hasHelpFlag(args) {
		fmt.Println(issueNoteAddUsage)
		return nil
	}
	if len(args) < 1 {
		return usageErr(issueNoteAddUsage, "")
	}

	issueID := args[0]
	author := ""
	body := ""
	invocationMode := ""

	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--author", "--actor":
			if i+1 < len(args) {
				author = args[i+1]
				i++
			}
		case "--body":
			if i+1 < len(args) {
				body = args[i+1]
				i++
			}
		case "--invocation-mode":
			if i+1 < len(args) {
				invocationMode = args[i+1]
				i++
			}
		}
	}

	var err error
	author, err = resolveActor(author)
	if err != nil {
		return usageErr(issueNoteAddUsage, err.Error())
	}
	if body == "" {
		return usageErr(issueNoteAddUsage, "--body is required")
	}

	if invocationMode != "" {
		normalized, nerr := core.NormalizeInvocationMode(invocationMode)
		if nerr != nil {
			return usageErr(issueNoteAddUsage, nerr.Error())
		}
		invocationMode = normalized
	}

	note, err := c.CreateNoteWithMode(ctx, issueID, author, body, invocationMode)
	if err != nil {
		fail(err)
	}
	if jsonOutput {
		json.NewEncoder(os.Stdout).Encode(note)
		return nil
	}
	fmt.Printf("Note ID:    %s\n", note.ID)
	fmt.Printf("Issue ID:   %s\n", note.IssueID)
	fmt.Printf("Author:     %s\n", note.Author)
	fmt.Printf("Body:       %s\n", note.Body)
	fmt.Printf("Created At: %s\n", note.CreatedAt)
	return nil
}

const issueNoteListUsage = "Usage: dibs issue note list <issue-id>"

func runIssueNoteList(ctx context.Context, c *client.Client, args []string) error {
	if hasHelpFlag(args) {
		fmt.Println(issueNoteListUsage)
		return nil
	}
	if len(args) < 1 {
		return usageErr(issueNoteListUsage, "")
	}

	issueID := args[0]

	notes, err := c.ListNotes(ctx, issueID)
	if err != nil {
		fail(err)
	}
	if jsonOutput {
		json.NewEncoder(os.Stdout).Encode(notes)
		return nil
	}
	if len(notes) == 0 {
		fmt.Println("No notes found.")
		return nil
	}
	for _, n := range notes {
		fmt.Printf("Note ID:    %s\n", n.ID)
		fmt.Printf("Author:     %s\n", n.Author)
		fmt.Printf("Body:       %s\n", n.Body)
		fmt.Printf("Created At: %s\n\n", n.CreatedAt)
	}
	return nil
}

// ─── Issue Tags ────────────────────────────────────────────────────────────

const issueTagUsage = "Usage: dibs issue tag <add|remove|list>"

func runIssueTag(ctx context.Context, c *client.Client, args []string) error {
	if len(args) < 1 {
		return usageErr(issueTagUsage, "")
	}
	if args[0] == "--help" || args[0] == "-h" || args[0] == "help" {
		fmt.Println(issueTagUsage)
		return nil
	}

	switch args[0] {
	case "add":
		return runIssueTagAdd(ctx, c, args[1:])
	case "remove":
		return runIssueTagRemove(ctx, c, args[1:])
	case "list":
		return runIssueTagList(ctx, c, args[1:])
	default:
		return usageErr(issueTagUsage, fmt.Sprintf("unknown tag subcommand: %s", args[0]))
	}
}

const issueTagAddUsage = "Usage: dibs issue tag add <issue-id> --tag <namespace/value>"

func runIssueTagAdd(ctx context.Context, c *client.Client, args []string) error {
	if hasHelpFlag(args) {
		fmt.Println(issueTagAddUsage)
		return nil
	}
	if len(args) < 1 {
		return usageErr(issueTagAddUsage, "")
	}

	issueID := args[0]
	tag := ""
	for i := 1; i < len(args); i++ {
		if args[i] == "--tag" && i+1 < len(args) {
			tag = args[i+1]
			i++
		}
	}
	if tag == "" {
		return usageErr(issueTagAddUsage, "--tag is required")
	}

	actor, err := resolveActor("")
	if err != nil {
		return usageErr(issueTagAddUsage, err.Error())
	}

	if err := c.AddTag(ctx, issueID, core.AddTagRequest{Tag: tag, Actor: actor}); err != nil {
		fail(err)
	}
	if jsonOutput {
		fmt.Println(`{"status":"ok"}`)
		return nil
	}
	fmt.Printf("Tag added: %s\n", tag)
	return nil
}

const issueTagRemoveUsage = "Usage: dibs issue tag remove <issue-id> --tag <namespace/value>"

func runIssueTagRemove(ctx context.Context, c *client.Client, args []string) error {
	if hasHelpFlag(args) {
		fmt.Println(issueTagRemoveUsage)
		return nil
	}
	if len(args) < 1 {
		return usageErr(issueTagRemoveUsage, "")
	}

	issueID := args[0]
	tag := ""
	for i := 1; i < len(args); i++ {
		if args[i] == "--tag" && i+1 < len(args) {
			tag = args[i+1]
			i++
		}
	}
	if tag == "" {
		return usageErr(issueTagRemoveUsage, "--tag is required")
	}

	actor, err := resolveActor("")
	if err != nil {
		return usageErr(issueTagRemoveUsage, err.Error())
	}

	if err := c.RemoveTag(ctx, issueID, tag, actor); err != nil {
		fail(err)
	}
	if jsonOutput {
		fmt.Println(`{"status":"ok"}`)
		return nil
	}
	fmt.Printf("Tag removed: %s\n", tag)
	return nil
}

const issueTagListUsage = "Usage: dibs issue tag list <issue-id>"

func runIssueTagList(ctx context.Context, c *client.Client, args []string) error {
	if hasHelpFlag(args) {
		fmt.Println(issueTagListUsage)
		return nil
	}
	if len(args) < 1 {
		return usageErr(issueTagListUsage, "")
	}

	issueID := args[0]
	issue, _, err := c.GetIssue(ctx, issueID)
	if err != nil {
		fail(err)
	}
	if jsonOutput {
		json.NewEncoder(os.Stdout).Encode(issue.Tags)
		return nil
	}
	if len(issue.Tags) == 0 {
		fmt.Println("No tags.")
		return nil
	}
	for _, tag := range issue.Tags {
		fmt.Println(tag)
	}
	return nil
}

// ─── Issue Events ──────────────────────────────────────────────────────────

const issueEventsUsage = "Usage: dibs issue events list <issue-id>"

func runIssueEvents(ctx context.Context, c *client.Client, args []string) error {
	if len(args) < 1 {
		return usageErr(issueEventsUsage, "")
	}
	if args[0] == "--help" || args[0] == "-h" || args[0] == "help" {
		fmt.Println(issueEventsUsage)
		return nil
	}

	switch args[0] {
	case "list":
		return runIssueEventsList(ctx, c, args[1:])
	default:
		return usageErr(issueEventsUsage, fmt.Sprintf("unknown events subcommand: %s", args[0]))
	}
}

func runIssueEventsList(ctx context.Context, c *client.Client, args []string) error {
	if hasHelpFlag(args) {
		fmt.Println(issueEventsUsage)
		return nil
	}
	if len(args) < 1 {
		return usageErr(issueEventsUsage, "")
	}

	issueID := args[0]

	events, err := c.ListEvents(ctx, issueID)
	if err != nil {
		fail(err)
	}
	if jsonOutput {
		json.NewEncoder(os.Stdout).Encode(events)
		return nil
	}
	if len(events) == 0 {
		fmt.Println("No events found.")
		return nil
	}
	for _, e := range events {
		fmt.Printf("Event ID:    %s\n", e.ID)
		fmt.Printf("Actor:       %s\n", e.Actor)
		fmt.Printf("Type:        %s\n", e.EventType)
		fmt.Printf("Payload:     %s\n", e.PayloadJSON)
		fmt.Printf("Created At:  %s\n\n", e.CreatedAt)
	}
	return nil
}
