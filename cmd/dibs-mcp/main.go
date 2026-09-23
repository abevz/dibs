package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/abevz/dibs/internal/build"
	"github.com/abevz/dibs/internal/client"
	"github.com/abevz/dibs/internal/compat"
	"github.com/abevz/dibs/internal/config"
	"github.com/abevz/dibs/internal/mcp"
)

func main() {
	compat.WarnLegacyBinary("afc-mcp", "dibs-mcp")
	cfg := config.Default()
	actor := config.EnvOrDefault("DIBS_ACTOR", "AF_COORDINATOR_ACTOR", "")

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	server := mcp.NewServer(client.New(cfg.SocketPath), actor, build.Revision)
	if err := server.Run(ctx, os.Stdin, os.Stdout); err != nil && err != context.Canceled {
		fmt.Fprintf(os.Stderr, "dibs-mcp: %v\n", err)
		os.Exit(1)
	}
}
