package main

import (
	"fmt"
	"testing"

	"github.com/abevz/dibs/internal/client"
)

func TestMapExitCodeErr(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{"nil error", nil, 0},
		{"not found", &client.ClientError{Code: "not_found", Message: "x"}, 5},
		{"lease held", &client.ClientError{Code: "lease_held", Message: "x"}, 3},
		{"lease expired", &client.ClientError{Code: "lease_expired", Message: "x"}, 4},
		{"version conflict", &client.ClientError{Code: "version_conflict", Message: "x"}, 2},
		{"issue not ready", &client.ClientError{Code: "issue_not_ready", Message: "x"}, 7},
		{"dependency cycle", &client.ClientError{Code: "dependency_cycle", Message: "x"}, 6},
		{"validation failed", &client.ClientError{Code: "validation_failed", Message: "x"}, 1},
		{"unknown error", fmt.Errorf("some transport error"), 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mapExitCodeErr(tt.err)
			if got != tt.want {
				t.Errorf("mapExitCodeErr(%v) = %d, want %d", tt.err, got, tt.want)
			}
		})
	}
}
