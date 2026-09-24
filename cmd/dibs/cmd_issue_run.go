package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/abevz/dibs/internal/client"
	"github.com/abevz/dibs/internal/core"
)

const issueRunUsage = "Usage: dibs issue run <issue-id> [--actor <name>] [--ttl <seconds>] [--close-resolution done|cancelled] [--branch <name>] [--pr-url <url>] [--commit-sha <sha>] [--note <text>] [--invocation-mode interactive|scheduled|unknown] -- <command> [args...]\n" + lifecycleHint +
	"\nOwns claim -> heartbeat -> close/handoff around a single subprocess; the child receives the lease token in its environment, never in argv. On exit 0, closes with --close-resolution (default done). On any other exit, or on Ctrl-C, hands the lease off with an auto-generated HANDOFF: note instead of closing. On confirmed lease ownership loss (heartbeat rejected with lease_expired, or the lease window closing without proof), the child is terminated and the CLI exits non-zero with lease_expired without sending a close request."

// runIssueRun claims issueID, execs the given command with the lease
// exported as environment variables, heartbeats in the background for the
// duration of the run, and closes or hands off the issue based on how the
// command exited. If the background heartbeat proves lease ownership was
// lost, the child is terminated and no close or handoff is sent: the run
// returns a distinct lease_expired error instead. See issueRunUsage for the
// full contract.
func runIssueRun(ctx context.Context, c *client.Client, args []string) error {
	if hasHelpFlag(args) {
		fmt.Println(issueRunUsage)
		return nil
	}

	sepIdx := -1
	for i, a := range args {
		if a == "--" {
			sepIdx = i
			break
		}
	}
	if sepIdx == -1 {
		return usageErr(issueRunUsage, "missing -- separator before the command to run")
	}
	flagArgs := args[:sepIdx]
	cmdArgs := args[sepIdx+1:]
	if len(cmdArgs) == 0 {
		return usageErr(issueRunUsage, "no command given after --")
	}
	if len(flagArgs) < 1 {
		return usageErr(issueRunUsage, "")
	}

	issueID := flagArgs[0]
	actor := ""
	ttl := 900
	closeResolution := "done"
	var branch, prURL, commitSHA, note string
	invocationMode := ""

	for i := 1; i < len(flagArgs); i++ {
		switch flagArgs[i] {
		case "--actor":
			if i+1 < len(flagArgs) {
				actor = flagArgs[i+1]
				i++
			}
		case "--ttl":
			if i+1 < len(flagArgs) {
				fmt.Sscanf(flagArgs[i+1], "%d", &ttl)
				i++
			}
		case "--close-resolution":
			if i+1 < len(flagArgs) {
				closeResolution = flagArgs[i+1]
				i++
			}
		case "--branch":
			if i+1 < len(flagArgs) {
				branch = flagArgs[i+1]
				i++
			}
		case "--pr-url":
			if i+1 < len(flagArgs) {
				prURL = flagArgs[i+1]
				i++
			}
		case "--commit-sha":
			if i+1 < len(flagArgs) {
				commitSHA = flagArgs[i+1]
				i++
			}
		case "--note":
			if i+1 < len(flagArgs) {
				note = flagArgs[i+1]
				i++
			}
		case "--invocation-mode":
			if i+1 < len(flagArgs) {
				invocationMode = flagArgs[i+1]
				i++
			}
		default:
			return usageErr(issueRunUsage, fmt.Sprintf("unknown flag: %s", flagArgs[i]))
		}
	}
	if closeResolution != "done" && closeResolution != "cancelled" {
		return usageErr(issueRunUsage, "--close-resolution must be done or cancelled")
	}
	if ttl <= 0 {
		return usageErr(issueRunUsage, "--ttl must be positive")
	}

	holder, err := resolveActor(actor)
	if err != nil {
		return usageErr(issueRunUsage, err.Error())
	}
	if invocationMode != "" {
		normalized, nerr := core.NormalizeInvocationMode(invocationMode)
		if nerr != nil {
			return usageErr(issueRunUsage, nerr.Error())
		}
		invocationMode = normalized
	}

	claim, err := c.ClaimIssueWithSessionAndMode(ctx, issueID, holder, ttl, "", invocationMode)
	if err != nil {
		fail(err)
	}
	fmt.Printf("Claimed %s (version %d, expires %s)\n", issueID, claim.Version, claim.ExpiresAt)

	heartbeatInterval := time.Duration(ttl) * time.Second / 3

	// knownExpiry is the last deadline the daemon gave us, from the claim or
	// a successful heartbeat. Transient heartbeat failures may be retried only
	// while now < knownExpiry; once that window closes without proof,
	// ownership is treated as lost and the child is stopped.
	knownExpiry, err := time.Parse(time.RFC3339, claim.ExpiresAt)
	if err != nil {
		return fmt.Errorf("issue run: invalid claim lease expiry: %w", err)
	}

	// childCtx lets the heartbeat goroutine cancel the child the moment
	// ownership is lost; exec.CommandContext turns that cancel into SIGTERM
	// followed by a bounded WaitDelay kill.
	childCtx, childCancel := context.WithCancel(ctx)
	defer childCancel()

	// lostCh carries the confirmed ownership-loss error to the run loop. It is
	// buffered so the heartbeat goroutine never blocks on delivery; the run
	// loop drains it once more after joining the goroutine so a loss that
	// raced with child completion still wins over close/handoff.
	lostCh := make(chan *client.ClientError, 1)

	hbCtx, hbCancel := context.WithCancel(ctx)
	defer hbCancel()

	var hbWG sync.WaitGroup
	hbWG.Add(1)
	go func() {
		defer hbWG.Done()
		runHeartbeat(hbCtx, c, issueID, claim.LeaseToken, claim.LeaseGeneration, ttl, heartbeatInterval, knownExpiry, childCancel, lostCh)
	}()

	cmd := exec.CommandContext(childCtx, cmdArgs[0], cmdArgs[1:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	// Isolate the launched workload from dibs's own process group. Cancelling
	// only the shell leader can otherwise leave its current child running and
	// prevent the shell from observing SIGTERM until WaitDelay kills it.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Env = append(os.Environ(),
		"DIBS_LEASE_TOKEN="+claim.LeaseToken,
		fmt.Sprintf("DIBS_LEASE_GENERATION=%d", claim.LeaseGeneration),
		"DIBS_ATTEMPT_ID="+claim.AttemptID,
		"DIBS_ISSUE_ID="+issueID,
		fmt.Sprintf("DIBS_EXPECTED_VERSION=%d", claim.Version),
		"AF_LEASE_TOKEN="+claim.LeaseToken,
		fmt.Sprintf("AF_LEASE_GENERATION=%d", claim.LeaseGeneration),
		"AF_ATTEMPT_ID="+claim.AttemptID,
		"AF_ISSUE_ID="+issueID,
		fmt.Sprintf("AF_EXPECTED_VERSION=%d", claim.Version),
	)
	// exec.CommandContext's default cancellation is an immediate SIGKILL of
	// only the process leader. Signal the isolated workload group so shells and
	// their current children all get the same graceful-stop request.
	cmd.Cancel = func() error {
		return signalCommandProcessGroup(cmd, syscall.SIGTERM)
	}
	cmd.WaitDelay = 5 * time.Second

	runErrCh := make(chan error, 1)
	go func() {
		runErrCh <- cmd.Run()
	}()

	var runErr error
	var lossErr *client.ClientError
	select {
	case runErr = <-runErrCh:
	case lossErr = <-lostCh:
		// Ownership loss already cancelled the child; wait for it to finish
		// terminating so no orphan process is left behind.
		runErr = <-runErrCh
	}
	// WaitDelay can force the process leader to exit while a descendant that
	// ignored SIGTERM remains in the isolated group. Ensure no such descendant
	// survives a cancelled run.
	var cleanupErr error
	if childCtx.Err() != nil {
		cleanupErr = signalCommandProcessGroup(cmd, syscall.SIGKILL)
		if errors.Is(cleanupErr, os.ErrProcessDone) {
			cleanupErr = nil
		}
	}

	// Stop the heartbeat and wait for it to return so no heartbeat goroutine
	// survives command exit.
	hbCancel()
	hbWG.Wait()

	// Final race-free check: the heartbeat goroutine has stopped, so if it
	// detected ownership loss it already queued the error before returning. A
	// loss that raced with a clean child exit still wins over close/handoff.
	select {
	case lossErr = <-lostCh:
	default:
	}

	if lossErr != nil {
		if cleanupErr != nil {
			return fmt.Errorf("%w; process-group cleanup failed: %v", lossErr, cleanupErr)
		}
		return lossErr
	}
	if cleanupErr != nil {
		return fmt.Errorf("clean up cancelled issue run process group: %w", cleanupErr)
	}

	background := context.Background()

	if runErr == nil {
		result, err := c.CloseIssue(background, issueID, core.CloseIssueRequest{
			Resolution:      closeResolution,
			Branch:          branch,
			PRURL:           prURL,
			CommitSHA:       commitSHA,
			ExpectedVersion: claim.Version,
			LeaseToken:      claim.LeaseToken,
			LeaseGeneration: claim.LeaseGeneration,
			Actor:           holder,
			Note:            note,
			InvocationMode:  invocationMode,
		})
		if err != nil {
			return fmt.Errorf("command succeeded but close failed: %w", err)
		}
		if jsonOutput {
			json.NewEncoder(os.Stdout).Encode(result)
			return nil
		}
		fmt.Println("Issue closed.")
		return nil
	}

	exitCode := 1
	var exitErr *exec.ExitError
	if errors.As(runErr, &exitErr) {
		exitCode = exitErr.ExitCode()
	}
	handoffNote := fmt.Sprintf("HANDOFF: issue run command failed (exit %d)", exitCode)
	if ctx.Err() != nil {
		handoffNote = "HANDOFF: issue run cancelled"
	}
	if _, err := c.HandoffLeaseWithMode(background, issueID, claim.LeaseToken, claim.LeaseGeneration, handoffNote, invocationMode); err != nil {
		return fmt.Errorf("command failed (%v) and handoff also failed: %w", runErr, err)
	}
	fmt.Fprintf(os.Stderr, "issue run: command failed, lease handed off with note: %s\n", handoffNote)
	os.Exit(exitCode)
	return nil // unreachable
}

func signalCommandProcessGroup(cmd *exec.Cmd, signal syscall.Signal) error {
	if cmd.Process == nil {
		return os.ErrProcessDone
	}
	if err := syscall.Kill(-cmd.Process.Pid, signal); err != nil {
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	return nil
}

// runHeartbeat heartbeats the lease until ownership is lost, the known lease
// window closes without proof of ownership, or ctx is cancelled. On confirmed
// loss it terminates the child (stopChild) and reports a distinct
// ownership-loss error on lostCh before returning, so the run loop can join it
// and never close after ownership moved.
func runHeartbeat(ctx context.Context, c *client.Client, issueID, leaseToken string, leaseGeneration int64, ttlSeconds int, interval time.Duration, knownExpiry time.Time, stopChild func(), lostCh chan<- *client.ClientError) {
	const initialRetryDelay = time.Second
	const maxRetryDelay = 5 * time.Second

	reportLoss := func(detail string) {
		stopChild()
		lostCh <- &client.ClientError{Code: core.ErrLeaseExpired, Message: "lease ownership lost: " + detail}
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	retryDelay := initialRetryDelay
	attempt := func() bool {
		operationID := newOperationID()
		ambiguous := false
		for {
			if ctx.Err() != nil {
				return false
			}
			if !time.Now().Before(knownExpiry) {
				reportLoss("known lease window closed before a fresh heartbeat proved ownership")
				return false
			}
			requestCtx, cancel := context.WithDeadline(ctx, knownExpiry)
			newExpiry, err := c.HeartbeatLeaseWithOperation(requestCtx, issueID, core.HeartbeatRequest{
				LeaseToken: leaseToken, LeaseGeneration: leaseGeneration,
				TTLSeconds: ttlSeconds, OperationID: operationID,
			})
			cancel()
			if err == nil {
				if ambiguous {
					// This exact-ID response may be an old committed outcome. It
					// resolves ambiguity but says nothing about current ownership.
					operationID = newOperationID()
					ambiguous = false
					continue
				}
				parsed, perr := time.Parse(time.RFC3339, newExpiry)
				if perr != nil || !time.Now().Before(parsed) {
					reportLoss("fresh heartbeat returned no future lease deadline")
					return false
				}
				knownExpiry = parsed
				retryDelay = initialRetryDelay
				return true
			}
			if ctx.Err() != nil {
				return false
			}
			var clientErr *client.ClientError
			if errors.As(err, &clientErr) && clientErr.Code == core.ErrLeaseExpired {
				reportLoss(fmt.Sprintf("heartbeat rejected: %s", clientErr.Message))
				return false
			}
			if errors.As(err, &clientErr) && clientErr.Code != "internal_error" {
				reportLoss(fmt.Sprintf("heartbeat rejected with %s", clientErr.Code))
				return false
			}
			// A transient failure (transport or daemon hiccup) is retried with
			// backoff only while the known lease window is still open; once the
			// deadline passes without proof, ownership is gone.
			if !time.Now().Before(knownExpiry) {
				reportLoss(fmt.Sprintf("heartbeat could not prove ownership before lease expiry: %v", err))
				return false
			}
			fmt.Fprintf(os.Stderr, "issue run: heartbeat failed, retrying within lease window: %v\n", err)
			ambiguous = true
			select {
			case <-ctx.Done():
				return false
			case <-time.After(min(retryDelay, time.Until(knownExpiry))):
			}
			retryDelay = min(retryDelay*2, maxRetryDelay)
		}
	}

	for {
		remaining := time.Until(knownExpiry)
		if remaining <= 0 {
			reportLoss("known lease window closed without fresh proof")
			return
		}
		deadline := time.NewTimer(remaining)
		select {
		case <-ctx.Done():
			deadline.Stop()
			return
		case <-deadline.C:
			reportLoss("known lease window closed without fresh proof")
			return
		case <-ticker.C:
			deadline.Stop()
			if !attempt() {
				return
			}
		}
	}
}
