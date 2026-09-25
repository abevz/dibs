package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRootAndGroupHelpWithoutDaemon(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "dibs")
	if output, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, output)
	}
	for _, tt := range []struct {
		args []string
		want []string
	}{
		{nil, []string{"Usage: dibs", "project"}},
		{[]string{"--help"}, []string{"Usage: dibs", "Manage issues.", "Add, remove, or list issue tags."}},
		{[]string{"-help"}, []string{"Usage: dibs"}},
		{[]string{"project"}, []string{"Usage: dibs project <subcommand>", "add", "list"}},
		{[]string{"project", "--help"}, []string{"Usage: dibs project <subcommand>", "add", "list"}},
		{[]string{"project", "-help"}, []string{"Usage: dibs project <subcommand>"}},
		{[]string{"project", "add", "-help"}, []string{"Usage: dibs project add", "Create a project with a key and display name."}},
		{[]string{"issue", "--help"}, []string{"Usage: dibs issue <subcommand>", "claim                Acquire a lease", "dependency           Manage dependencies", "operator-release     Clear a stuck lease"}},
		{[]string{"issue", "dependency", "--help"}, []string{"Usage: dibs issue dependency <subcommand>", "Add a dependency or relation", "Remove a dependency or relation"}},
		{[]string{"issue", "note", "--help"}, []string{"Usage: dibs issue note <subcommand>", "Add a note", "List notes"}},
		{[]string{"dependency", "--help"}, []string{"Usage: dibs dependency <subcommand>", "Add a dependency or relation"}},
		{[]string{"dependency", "add", "--help"}, []string{"Usage: dibs issue dependency add", "Add a dependency or relation"}},
		{[]string{"ls", "--help"}, []string{"Usage: dibs issue list", "List issues with optional filters."}},
		{[]string{"show", "--help"}, []string{"Usage: dibs issue get", "Show one issue by ID or short ID."}},
	} {
		cmd := exec.Command(bin, tt.args...)
		cmd.Env = append(os.Environ(), "DIBS_SOCKET="+filepath.Join(t.TempDir(), "absent.sock"))
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		if err := cmd.Run(); err != nil || stderr.Len() != 0 {
			t.Fatalf("dibs %v: err=%v, stderr=%q", tt.args, err, stderr.String())
		}
		for _, want := range tt.want {
			if !strings.Contains(stdout.String(), want) {
				t.Errorf("dibs %v: missing %q in %q", tt.args, want, stdout.String())
			}
		}
	}
	for key := range commandRoutes {
		args := append(strings.Fields(key), "--help")
		cmd := exec.Command(bin, args...)
		cmd.Env = append(os.Environ(), "DIBS_SOCKET="+filepath.Join(t.TempDir(), "absent.sock"))
		output, err := cmd.CombinedOutput()
		if err != nil || !strings.Contains(string(output), commandDescriptions[key]) ||
			!strings.Contains(string(output), "Usage: dibs ") {
			t.Errorf("dibs %v: err=%v, unexpected local help %q", args, err, output)
		}
	}
	cmd := exec.Command(bin, "projects", "-help")
	cmd.Env = append(os.Environ(), "DIBS_SOCKET="+filepath.Join(t.TempDir(), "absent.sock"))
	output, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "Did you mean: dibs project --help?") {
		t.Fatalf("projects typo: err=%v, output=%q", err, output)
	}
	for _, tt := range []struct {
		args []string
		hint string
	}{
		{[]string{"project", "--h"}, "Use: dibs project --help"},
		{[]string{"project", "create"}, "Use: dibs project --help"},
		{[]string{"project", "add", "--h"}, "Use: dibs project add --help"},
		{[]string{"--h"}, "Use: dibs --help"},
	} {
		cmd := exec.Command(bin, tt.args...)
		cmd.Env = append(os.Environ(), "DIBS_SOCKET="+filepath.Join(t.TempDir(), "absent.sock"))
		output, err := cmd.CombinedOutput()
		if err == nil || !strings.Contains(string(output), tt.hint) {
			t.Errorf("dibs %v: err=%v, missing %q in %q", tt.args, err, tt.hint, output)
		}
		if len(tt.args) > 0 && tt.args[len(tt.args)-1] == "--h" &&
			!strings.Contains(string(output), "unknown flag: --h") {
			t.Errorf("dibs %v: expected unknown flag, got %q", tt.args, output)
		}
	}
}

func TestCommandCatalogCoversRootGroupAndLeafHelp(t *testing.T) {
	root := rootHelp()
	for key := range commandRoutes {
		description := commandDescriptions[key]
		if description == "" || !strings.Contains(root, description) {
			t.Errorf("root help lacks description for %q: %q", key, description)
		}
		leaf, ok := leafHelp(strings.Fields(key))
		if !ok || !strings.Contains(leaf, description) {
			t.Errorf("leaf help lacks description for %q: %q", key, leaf)
		}
		parts := strings.Fields(key)
		for end := 1; end < len(parts); end++ {
			groupPath := strings.Join(parts[:end], " ")
			childPath := strings.Join(parts[:end+1], " ")
			childDescription := commandDescriptions[childPath]
			group, ok := groupHelp(parts[:end])
			if !ok || childDescription == "" || !strings.Contains(group, childDescription) {
				t.Errorf("group %q lacks description for %q: %q", groupPath, childPath, group)
			}
		}
	}
	for _, alias := range []string{"dependency", "ls", "show"} {
		if commandDescriptions[alias] == "" || !strings.Contains(root, commandDescriptions[alias]) {
			t.Errorf("root help lacks alias %q", alias)
		}
	}
	alias, ok := groupHelp([]string{"dependency"})
	if !ok || !strings.Contains(alias, commandDescriptions["issue dependency add"]) ||
		!strings.Contains(alias, commandDescriptions["issue dependency remove"]) {
		t.Errorf("dependency alias help is incomplete: %q", alias)
	}
}

func TestOperatorHelpNamesTokenSource(t *testing.T) {
	root := rootHelp()
	for _, command := range []string{"cancel", "operator-close", "operator-reopen", "operator-release"} {
		key := "issue " + command
		help, ok := leafHelp([]string{"issue", command})
		if !ok || !strings.Contains(help, "Authorization: set DIBS_OPERATOR_TOKEN in the environment.") {
			t.Errorf("%s leaf help omits operator token source: %q", key, help)
		}
		if !strings.Contains(root, commandDescriptions[key]) || !strings.Contains(commandDescriptions[key], "DIBS_OPERATOR_TOKEN") {
			t.Errorf("%s root help omits operator token source", key)
		}
	}
	release, _ := leafHelp([]string{"issue", "operator-release"})
	if !strings.Contains(release, "return the issue to open") {
		t.Errorf("operator-release help omits its status transition: %q", release)
	}
}

func TestEveryCLILeafHasLocalHelp(t *testing.T) {
	for key, route := range commandRoutes {
		args := strings.Fields(key)
		help, ok := leafHelp(args)
		if !ok || !strings.Contains(help, "Usage: dibs "+key) {
			t.Errorf("%s has no leaf help: %q", key, help)
		}
		for _, flag := range strings.Fields(route.flags) {
			bare := strings.TrimSuffix(flag, "?")
			info, defined := helpForFlag(key, bare)
			if !defined || info.description == "" || (info.value == "" && !strings.HasSuffix(flag, "?")) ||
				(info.value != "" && strings.HasSuffix(flag, "?")) {
				t.Errorf("%s has incomplete help metadata for %s: %+v", key, flag, info)
			}
			if flag == "--lease-token" {
				continue
			}
			if !strings.Contains(help, strings.TrimSuffix(flag, "?")) {
				t.Errorf("%s help omits %s", key, flag)
			}
		}
		if strings.Contains(help, "<value>") || strings.Contains(help, "Required: none") {
			t.Errorf("%s has generic help: %q", key, help)
		}
		for _, flag := range strings.Fields(requiredCommandFlags[key]) {
			if flag == "--lease-token" {
				continue
			}
			if !strings.Contains(help, "Required:") || !strings.Contains(help, flag) {
				t.Errorf("%s does not mark %s required", key, flag)
			}
		}
	}
}

func TestProjectAddHelpExplainsFields(t *testing.T) {
	help, ok := leafHelp([]string{"project", "add"})
	if !ok {
		t.Fatal("project add help missing")
	}
	for _, want := range []string{
		"--key <project-key>", "lowercase project slug", "prefixes issue IDs",
		"--name <display-name>", "Human-readable project name",
		`dibs project add --key myapp --name "My App"`,
	} {
		if !strings.Contains(help, want) {
			t.Errorf("project add help missing %q: %s", want, help)
		}
	}
}

func TestLifecycleHelpUsesTokenEnvironment(t *testing.T) {
	for _, command := range []string{"heartbeat", "release", "handoff", "close"} {
		help, ok := leafHelp([]string{"issue", command})
		if !ok || !strings.Contains(help, "DIBS_LEASE_TOKEN_FILE") || !strings.Contains(help, "never put a token in argv") {
			t.Errorf("%s help omits safe token source: %q", command, help)
		}
		if strings.Contains(help, "--lease-token <value>") {
			t.Errorf("%s help suggests token argv", command)
		}
	}
	help, _ := leafHelp([]string{"issue", "claim"})
	if !strings.Contains(help, "actor via --holder") {
		t.Errorf("claim help omits conditional actor requirement: %q", help)
	}
}

func TestIssueRunChildHelpIsNotCLIHelp(t *testing.T) {
	if printLocalHelp([]string{"issue", "run", "afc-145", "--", "go", "test", "--help"}) {
		t.Fatal("child --help belongs to the child process")
	}
	if printLocalHelp([]string{"issue", "run", "afc-145", "--", "go", "test", "-help"}) {
		t.Fatal("child -help belongs to the child process")
	}
	if printLocalHelp([]string{"issue", "run", "afc-145", "--", "go", "test", "--h"}) {
		t.Fatal("child --h belongs to the child process")
	}
}

func TestIssueRunChildHelpReachesHandlerValidation(t *testing.T) {
	for _, childFlag := range []string{"--help", "-help"} {
		err := runIssueRun(context.Background(), nil, []string{
			"afc-1", "--close-resolution", "invalid", "--", "child", childFlag,
		})
		if err == nil || !strings.Contains(err.Error(), "--close-resolution must be done or cancelled") {
			t.Errorf("child %s: expected handler validation, got %v", childFlag, err)
		}
	}
}

func TestLeafHelpShowsAlternativeRequiredTargets(t *testing.T) {
	for _, args := range [][]string{
		{"issue", "link"}, {"issue", "unlink"},
		{"issue", "dependency", "add"}, {"issue", "dependency", "remove"},
	} {
		help, ok := leafHelp(args)
		if !ok || !strings.Contains(help, "one of") {
			t.Errorf("%q misses required target alternative: %q", args, help)
		}
	}
}

func TestPaginationBoundsFailBeforeDaemon(t *testing.T) {
	for _, args := range [][]string{
		{"issue", "list", "--limit", "1001"},
		{"issue", "list", "--offset", "1000001"},
	} {
		if err := validateCommandArgs(args); err == nil {
			t.Errorf("accepted out-of-range page: %q", args)
		}
	}
}
