package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/abevz/dibs/internal/client"
	"github.com/abevz/dibs/internal/config"
	"github.com/abevz/dibs/internal/core"
	"github.com/abevz/dibs/internal/operatorcontrol"
)

const controlLookupTimeout = 500 * time.Millisecond

func runControl(ctx context.Context, c *client.Client, args []string) (int, error) {
	switch args[0] {
	case "pause", "stop", "resume":
		return 0, runControlChange(ctx, c, args)
	case "hook":
		return runControlHook(ctx, c, len(args) == 2 && args[1] == "--serial"), nil
	case "status", "enroll":
		cwd, err := os.Getwd()
		if err != nil {
			return 0, err
		}
		req, err := controlRequest(ctx, cwd)
		if err != nil {
			return 0, err
		}
		if args[0] == "status" {
			out, err := c.ControlStatus(ctx, req)
			if err != nil {
				return 0, err
			}
			return 0, json.NewEncoder(os.Stdout).Encode(out)
		}
		gate, err := localControlGate(c)
		if err != nil {
			return 0, err
		}
		out, err := enrollControl(ctx, c, gate, req)
		if err != nil {
			return 0, err
		}
		return 0, json.NewEncoder(os.Stdout).Encode(out)
	default:
		return 0, fmt.Errorf("unknown control command")
	}
}

func runControlChange(ctx context.Context, c *client.Client, args []string) error {
	req := core.ControlChangeRequest{Action: args[0], OperationID: newOperationID()}
	count := 0
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--global":
			req.Kind, req.ID = "global", ""
			count++
		case "--issue", "--repo", "--worktree":
			req.Kind, req.ID = args[i][2:], args[i+1]
			count++
			i++
		case "--reason":
			req.Reason = args[i+1]
			i++
		case "--operation-id":
			req.OperationID = args[i+1]
			i++
		}
	}
	if count != 1 {
		return argumentError("choose exactly one of --issue, --repo, --worktree, or --global")
	}
	actor, err := resolveActor("")
	if err != nil {
		return err
	}
	req.Actor = actor
	if err := req.Normalize(); err != nil {
		return argumentError(err.Error())
	}
	token := config.EnvOrDefault("DIBS_OPERATOR_TOKEN", "")
	if token == "" {
		return argumentError("DIBS_OPERATOR_TOKEN is required in the operator environment")
	}
	c.SetOperatorToken(token)
	out, err := c.ChangeControl(ctx, req)
	if err != nil {
		return err
	}
	if jsonOutput {
		return json.NewEncoder(os.Stdout).Encode(out)
	}
	fmt.Printf("%s %s %s (revision %d)\n", out.Action, out.Kind, out.ID, out.Revision)
	return nil
}

func localControlGate(c *client.Client) (operatorcontrol.Gate, error) {
	stateDir := os.Getenv("XDG_STATE_HOME")
	if stateDir == "" {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return operatorcontrol.Gate{}, err
		}
		stateDir = filepath.Join(homeDir, ".local", "state")
	}
	if !filepath.IsAbs(stateDir) {
		return operatorcontrol.Gate{}, fmt.Errorf("control state directory must be absolute")
	}
	socket, err := filepath.Abs(config.Default().SocketPath)
	if err != nil {
		return operatorcontrol.Gate{}, err
	}
	return operatorcontrol.Gate{
		Client: c, Cache: operatorcontrol.NewCache(filepath.Join(stateDir, "dibs", "control"), socket),
		Timeout: controlLookupTimeout,
	}, nil
}

func controlRequest(ctx context.Context, cwd string) (core.ControlStatusRequest, error) {
	req := core.ControlStatusRequest{IssueID: os.Getenv("DIBS_ISSUE_ID")}
	if req.IssueID != "" {
		token, err := leaseTokenFromEnvironment()
		if err != nil {
			return req, err
		}
		generation, err := strconv.ParseInt(os.Getenv("DIBS_LEASE_GENERATION"), 10, 64)
		if err != nil || generation <= 0 {
			return req, fmt.Errorf("DIBS_LEASE_GENERATION is required for a claimed session")
		}
		req.LeaseGeneration, req.LeaseToken = generation, token
	}
	root, err := controlCheckoutRoot(ctx, cwd)
	req.WorktreePath = root
	return req, err
}

func controlCheckoutRoot(ctx context.Context, cwd string) (string, error) {
	if !filepath.IsAbs(cwd) {
		return "", fmt.Errorf("hook cwd must be absolute")
	}
	ctx, cancel := context.WithTimeout(ctx, controlLookupTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "rev-parse", "--show-toplevel")
	cmd.Dir = cwd
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		switch key {
		case "GIT_DIR", "GIT_WORK_TREE", "GIT_COMMON_DIR":
			continue
		}
		cmd.Env = append(cmd.Env, entry)
	}
	cmd.Env = append(cmd.Env, "LC_ALL=C")
	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if ctx.Err() == nil && errors.As(err, &exitErr) && exitErr.ExitCode() == 128 &&
			strings.Contains(string(exitErr.Stderr), "not a git repository") {
			return "", nil
		}
		return "", err
	}
	return filepath.EvalSymlinks(strings.TrimRight(string(out), "\n"))
}

func enrollControl(ctx context.Context, c *client.Client, gate operatorcontrol.Gate, req core.ControlStatusRequest) (core.ControlStatus, error) {
	out, err := c.EnrollControl(ctx, req)
	if err != nil {
		return out, err
	}
	expiry, err := time.Parse(time.RFC3339, out.ExpiresAt)
	if err != nil || !time.Now().Before(expiry) {
		return out, fmt.Errorf("enrollment returned no live lease deadline")
	}
	return out, gate.Enrolled(ctx, req, out, expiry)
}

func runControlHook(ctx context.Context, c *client.Client, serial bool) int {
	var input struct {
		Event   string `json:"hook_event_name"`
		Cwd     string `json:"cwd"`
		AgentID string `json:"agent_id"`
	}
	decision := operatorcontrol.Decision{Denied: true, Message: operatorcontrol.CheckFailed}
	if err := json.NewDecoder(io.LimitReader(os.Stdin, 4<<20)).Decode(&input); err == nil && input.Event == "PreToolUse" {
		gate, gateErr := localControlGate(c)
		req, reqErr := controlRequest(ctx, input.Cwd)
		if gateErr == nil && reqErr == nil && (req.IssueID != "" || req.WorktreePath != "") {
			decision = gate.Check(ctx, req, serial && input.AgentID == "")
		}
	}
	if decision.Denied {
		fmt.Fprintln(os.Stderr, decision.Message)
		return 2
	}
	if decision.Warning != "" {
		if err := json.NewEncoder(os.Stdout).Encode(map[string]string{"systemMessage": decision.Warning}); err != nil {
			return 1
		}
	}
	return 0
}
