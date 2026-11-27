package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/abevz/dibs/internal/build"
	"github.com/abevz/dibs/internal/client"
	"github.com/abevz/dibs/internal/config"
	"github.com/abevz/dibs/internal/mcp"
)

func main() {
	cfg := config.Default()
	actor := config.EnvOrDefault("DIBS_ACTOR", "")

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	operator := len(os.Args) == 2 && os.Args[1] == "--operator"
	if len(os.Args) > 1 && !operator {
		fmt.Fprintln(os.Stderr, "Usage: dibs-mcp [--operator]")
		os.Exit(1)
	}
	token := config.EnvOrDefault("DIBS_OPERATOR_TOKEN", "")
	_ = os.Unsetenv("DIBS_OPERATOR_TOKEN")
	coordinator := client.New(cfg.SocketPath)
	server := mcp.NewServer(coordinator, actor, build.Revision)
	if operator {
		if token == "" {
			fmt.Fprintln(os.Stderr, "dibs-mcp --operator requires DIBS_OPERATOR_TOKEN in its separate operator environment")
			os.Exit(1)
		}
		coordinator.SetOperatorToken(token)
		server.EnableOperatorControl()
	}
	if err := server.Run(ctx, os.Stdin, os.Stdout); err != nil && err != context.Canceled {
		fmt.Fprintf(os.Stderr, "dibs-mcp: %v\n", err)
		os.Exit(1)
	}
}
