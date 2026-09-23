package main

import (
	"context"
	"fmt"
	"os"

	"github.com/abevz/dibs/internal/client"
)

func runExport(ctx context.Context, c *client.Client, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("%s", "Usage: dibs export jsonl")
	}

	switch args[0] {
	case "jsonl":
		return c.ExportJSONL(ctx, os.Stdout)
	default:
		return fmt.Errorf("unknown export subcommand: %s", args[0])
	}
}
