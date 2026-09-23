package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/abevz/dibs/internal/client"
	"github.com/abevz/dibs/internal/config"
	"github.com/abevz/dibs/internal/doctor"
)

func runDoctor(ctx context.Context, c *client.Client, cfg config.Config, args []string) error {
	results := doctor.RunAll(ctx, c, cfg)

	if jsonOutput {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(results)
	} else {
		for _, r := range results {
			if r.Status == "ok" {
				fmt.Printf("[ok]   %-22s: %s\n", r.Name, r.Message)
			} else {
				fmt.Printf("[WARN] %-22s: %s\n", r.Name, r.Message)
				if r.Hint != "" {
					fmt.Printf("       Hint: %s\n", r.Hint)
				}
			}
		}
	}

	for _, r := range results {
		if r.Status != "ok" {
			os.Exit(1)
		}
	}
	return nil
}
