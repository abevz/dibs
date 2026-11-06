package main

import (
	"strings"
	"testing"
)

func TestEvidenceCommandArguments(t *testing.T) {
	tests := []struct {
		args  []string
		valid bool
	}{
		{[]string{"artifact", "register", "--issue", "app-1", "--kind", "screenshot", "--file", "shot.png", "--diff-hash", strings.Repeat("a", 64)}, true},
		{[]string{"artifact", "register", "--repo", "app", "--kind", "spec", "--relative-path", "docs/spec.md"}, true},
		{[]string{"artifact", "register", "--issue", "app-1", "--kind", "review"}, false},
		{[]string{"artifact", "register", "--issue", "app-1", "--kind", "requirements", "--file", "report", "--diff-hash", strings.Repeat("a", 64)}, false},
		{[]string{"artifact", "register", "--issue", "app-1", "--kind", "review", "--file", "report", "--repo", "app"}, false},
		{[]string{"artifact", "list", "--repo", "app", "--issue", "app-1"}, false},
		{[]string{"artifact", "prune", "--issue", "app-1", "--days", "30", "--apply"}, true},
		{[]string{"artifact", "cleanup", "--issue", "app-1"}, true},
		{[]string{"issue", "publish", "app-1", "--require-evidence", "review,log", "--diff-hash", strings.Repeat("b", 64)}, true},
	}
	for _, tc := range tests {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			if err := validateCommandArgs(tc.args); (err == nil) != tc.valid {
				t.Fatalf("err=%v valid=%v", err, tc.valid)
			}
		})
	}
}
