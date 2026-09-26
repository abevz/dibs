package api

import (
	"context"
	"strings"
	"syscall"
	"testing"

	"github.com/abevz/dibs/internal/config"
)

func TestRunDaemonRejectsLongSocketBeforeSetup(t *testing.T) {
	path := "/" + strings.Repeat("x", len(syscall.RawSockaddrUnix{}.Path))
	err := RunDaemon(context.Background(), nil, config.Config{SocketPath: path}, nil)
	if err == nil || !strings.Contains(err.Error(), "DIBS_SOCKET") {
		t.Fatalf("RunDaemon error = %v, want path-length hint", err)
	}
}
