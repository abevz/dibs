package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/abevz/dibs/internal/client"
)

const hooksUsage = "Usage: dibs hooks <install|session-start|complete>\nRun an agent with: dibs issue run <issue-id> --require-complete -- <agent command>"

func runHooks(ctx context.Context, c *client.Client, args []string) error {
	if len(args) == 0 {
		return usageErr(hooksUsage, "")
	}
	switch args[0] {
	case "install":
		return installHook(args[1:])
	case "session-start":
		return hookSessionStart(ctx, c, args[1:])
	case "complete":
		return hookComplete(args[1:])
	default:
		return usageErr(hooksUsage, "unknown hooks subcommand")
	}
}

func hookComplete(args []string) error {
	if len(args) != 0 {
		return usageErr("Usage: dibs hooks complete", "no arguments expected")
	}
	path := os.Getenv("DIBS_COMPLETION_FILE")
	if path == "" || os.Getenv("DIBS_LEASE_TOKEN") == "" || os.Getenv("DIBS_ISSUE_ID") == "" {
		return fmt.Errorf("hooks complete requires an active dibs issue run --require-complete child")
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return fmt.Errorf("create completion marker: %w", err)
	}
	if _, err = f.WriteString("done\n"); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	fmt.Println("Completion recorded; the issue closes when the agent command exits successfully.")
	return nil
}

func hookSessionStart(ctx context.Context, c *client.Client, args []string) error {
	agent, project := "", ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--agent":
			i++
			if i < len(args) {
				agent = args[i]
			}
		case "--project":
			i++
			if i < len(args) {
				project = args[i]
			}
		}
	}
	if agent != "claude" && agent != "codex" {
		return usageErr("Usage: dibs hooks session-start --agent claude|codex [--project key]", "invalid agent")
	}
	issues, err := c.ListReadyIssues(ctx, project, "", nil)
	if err != nil {
		return err
	}
	var b strings.Builder
	b.WriteString("Dibs ready issues (read-only; no claim):\n")
	for i, issue := range issues {
		if i >= 10 {
			fmt.Fprintf(&b, "...and %d more\n", len(issues)-i)
			break
		}
		fmt.Fprintf(&b, "- %s: %s\n", issue.ShortID, issue.Title)
	}
	if len(issues) == 0 {
		b.WriteString("No ready issues.\n")
	}
	bin, err := os.Executable()
	if err != nil {
		return err
	}
	b.WriteString("Choose one issue explicitly. Run it using `dibs issue run <issue-id> --require-complete -- <agent command>`. The run owns claim and heartbeat. Within that run call `" + bin + " hooks complete` only after acceptance criteria are met; otherwise exit leaves an atomic HANDOFF. Do not claim from this hook.")
	out := map[string]any{"hookSpecificOutput": map[string]string{"hookEventName": "SessionStart", "additionalContext": b.String()}}
	return json.NewEncoder(os.Stdout).Encode(out)
}

func installHook(args []string) error {
	agent, path := "", ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--agent":
			i++
			if i < len(args) {
				agent = args[i]
			}
		case "--path":
			i++
			if i < len(args) {
				path = args[i]
			}
		}
	}
	if agent != "claude" && agent != "codex" {
		return usageErr("Usage: dibs hooks install --agent claude|codex [--path config.json]", "invalid agent")
	}
	if path == "" {
		root, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
		if err != nil {
			return fmt.Errorf("find Git root: %w; use --path", err)
		}
		name := ".codex/hooks.json"
		if agent == "claude" {
			name = ".claude/settings.json"
		}
		path = filepath.Join(strings.TrimSpace(string(root)), name)
	}
	if err := writeSessionStartHook(path, agent); err != nil {
		return err
	}
	fmt.Printf("Installed %s SessionStart hook in %s\n", agent, path)
	return nil
}

func writeSessionStartHook(path, agent string) error {
	bin, err := os.Executable()
	if err != nil {
		return err
	}
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refusing symlink config: %s", path)
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	var doc map[string]json.RawMessage
	if data, err := os.ReadFile(path); err == nil {
		if err = json.Unmarshal(data, &doc); err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if doc == nil {
		doc = map[string]json.RawMessage{}
	}
	hooks := map[string]json.RawMessage{}
	if raw := doc["hooks"]; len(raw) > 0 {
		if err := json.Unmarshal(raw, &hooks); err != nil {
			return fmt.Errorf("parse hooks in %s: %w", path, err)
		}
	}
	if hooks == nil {
		hooks = map[string]json.RawMessage{}
	}
	var groups []json.RawMessage
	if raw := hooks["SessionStart"]; len(raw) > 0 {
		if err := json.Unmarshal(raw, &groups); err != nil {
			return fmt.Errorf("parse SessionStart in %s: %w", path, err)
		}
	}
	command := "'" + strings.ReplaceAll(bin, "'", "'\\''") + "' hooks session-start --agent " + agent
	found := false
	for i, raw := range groups {
		var group map[string]json.RawMessage
		if err := json.Unmarshal(raw, &group); err != nil {
			return err
		}
		var handlers []map[string]json.RawMessage
		if err := json.Unmarshal(group["hooks"], &handlers); err != nil {
			return err
		}
		changed := false
		for _, handler := range handlers {
			var existing string
			if err := json.Unmarshal(handler["command"], &existing); err != nil {
				continue
			}
			if isStandaloneDibsHookCommand(existing, agent) {
				found = true
				if existing == command {
					return nil
				}
				handler["command"], _ = json.Marshal(command)
				changed = true
			}
		}
		if changed {
			groups[i], _ = json.Marshal(group)
			break
		}
	}
	if !found {
		group, _ := json.Marshal(map[string]any{"hooks": []any{map[string]any{"type": "command", "command": command}}})
		groups = append(groups, group)
	}
	hooks["SessionStart"], _ = json.Marshal(groups)
	doc["hooks"], _ = json.Marshal(hooks)
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	mode := os.FileMode(0600)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".dibs-hooks-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err = tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if _, err = tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func isStandaloneDibsHookCommand(command, agent string) bool {
	suffix := " hooks session-start --agent " + agent
	if command == "dibs"+suffix {
		return true
	}
	token, ok := strings.CutSuffix(command, suffix)
	if !ok || len(token) < 3 || token[0] != '\'' || token[len(token)-1] != '\'' {
		return false
	}
	inside := token[1 : len(token)-1]
	// The installer emits one single-quoted absolute executable path. Reject
	// wrappers, extra shell commands, and malformed quote escapes.
	if strings.Contains(strings.ReplaceAll(inside, "'\\''", ""), "'") {
		return false
	}
	return filepath.IsAbs(strings.ReplaceAll(inside, "'\\''", "'"))
}
