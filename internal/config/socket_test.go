package config

import (
	"strings"
	"syscall"
	"testing"
)

func TestValidateSocketPath(t *testing.T) {
	capacity := len(syscall.RawSockaddrUnix{}.Path)
	for _, tc := range []struct {
		name      string
		path      string
		wantError bool
	}{
		{name: "last usable byte", path: strings.Repeat("a", capacity-1)},
		{name: "capacity needs NUL", path: strings.Repeat("a", capacity), wantError: true},
		{name: "count bytes", path: strings.Repeat("a", capacity-2) + "é", wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateSocketPath(tc.path)
			if tc.wantError {
				if err == nil || !strings.Contains(err.Error(), tc.path) || !strings.Contains(err.Error(), "DIBS_SOCKET") || !strings.Contains(err.Error(), "sun_path limit") {
					t.Fatalf("error = %v, want path, limit, and DIBS_SOCKET hint", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
		})
	}
}
