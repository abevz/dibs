package main

import (
	"context"
	"strings"
	"testing"
)

func TestResolveDependencyEdge(t *testing.T) {
	cases := []struct {
		name          string
		issue         string
		args          []string
		wantTarget    string
		wantDependsOn string
		wantKind      string
		wantMessage   string
		wantErr       bool
	}{
		{
			name:          "blocked-by",
			issue:         "afc-180",
			args:          []string{"--blocked-by", "afc-186"},
			wantTarget:    "afc-180",
			wantDependsOn: "afc-186",
			wantKind:      "blocks",
			wantMessage:   "afc-180 is now blocked by afc-186",
		},
		{
			name:  "blocks reverses ownership",
			issue: "afc-180",
			args:  []string{"--blocks", "afc-186"},
			// afc-180 blocks afc-186 => the edge is stored on afc-186 (afc-186
			// depends_on afc-180).
			wantTarget:    "afc-186",
			wantDependsOn: "afc-180",
			wantKind:      "blocks",
			wantMessage:   "afc-186 is now blocked by afc-180",
		},
		{
			name:          "depends-on blocks",
			issue:         "afc-1",
			args:          []string{"--depends-on", "afc-2", "--kind", "blocks"},
			wantTarget:    "afc-1",
			wantDependsOn: "afc-2",
			wantKind:      "blocks",
			wantMessage:   "afc-1 is now blocked by afc-2",
		},
		{
			name:          "depends-on default kind",
			issue:         "afc-1",
			args:          []string{"--depends-on", "afc-2"},
			wantTarget:    "afc-1",
			wantDependsOn: "afc-2",
			wantKind:      "",
			wantMessage:   "afc-1 now depends on afc-2",
		},
		{
			name:          "depends-on parent",
			issue:         "afc-1",
			args:          []string{"--depends-on", "afc-epic", "--kind", "parent"},
			wantTarget:    "afc-1",
			wantDependsOn: "afc-epic",
			wantKind:      "parent",
			wantMessage:   "afc-1 now has a parent dependency on afc-epic",
		},
		{name: "no form", issue: "afc-1", args: nil, wantErr: true},
		{name: "mutually exclusive", issue: "afc-1", args: []string{"--blocks", "afc-2", "--blocked-by", "afc-3"}, wantErr: true},
		{name: "kind with directional", issue: "afc-1", args: []string{"--blocked-by", "afc-2", "--kind", "blocks"}, wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			edge, err := resolveDependencyEdge(tc.issue, tc.args)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got edge %+v", edge)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if edge.target != tc.wantTarget || edge.dependsOn != tc.wantDependsOn || edge.kind != tc.wantKind {
				t.Fatalf("edge = %+v, want target=%q dependsOn=%q kind=%q", edge, tc.wantTarget, tc.wantDependsOn, tc.wantKind)
			}
			if edge.message != tc.wantMessage {
				t.Fatalf("message = %q, want %q", edge.message, tc.wantMessage)
			}
		})
	}
}

// TestTopLevelDependencyRoutesToIssueNamespace verifies that a misplaced
// `dibs dependency ...` invocation lands in the issue dependency namespace
// (canonical: `dibs issue dependency`) so errors name the working path
// instead of the global usage block.
func TestTopLevelDependencyRoutesToIssueNamespace(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "no subcommand", args: nil},
		{name: "unknown subcommand", args: []string{"bogus"}},
		{name: "add missing everything", args: []string{"add"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := runDependency(context.Background(), nil, tt.args)
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), "Usage: dibs issue dependency") {
				t.Errorf("error = %q, want it to name the issue dependency path", err.Error())
			}
		})
	}

	t.Run("help short-circuits", func(t *testing.T) {
		if err := runDependency(context.Background(), nil, []string{"-h"}); err != nil {
			t.Errorf("runDependency(-h) = %v, want nil", err)
		}
		if err := runDependency(context.Background(), nil, []string{"--help"}); err != nil {
			t.Errorf("runDependency(--help) = %v, want nil", err)
		}
	})
}
