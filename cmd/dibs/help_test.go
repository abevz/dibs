package main

import (
	"strings"
	"testing"
)

func TestEveryCLILeafHasLocalHelp(t *testing.T) {
	for key, route := range commandRoutes {
		args := strings.Fields(key)
		help, ok := leafHelp(args)
		if !ok || !strings.Contains(help, "Usage: dibs "+key) {
			t.Errorf("%s has no leaf help: %q", key, help)
		}
		for _, flag := range strings.Fields(route.flags) {
			if flag == "--lease-token" {
				continue
			}
			if !strings.Contains(help, strings.TrimSuffix(flag, "?")) {
				t.Errorf("%s help omits %s", key, flag)
			}
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
