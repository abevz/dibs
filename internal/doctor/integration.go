package doctor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/abevz/dibs/internal/client"
	"github.com/abevz/dibs/internal/core"
	"github.com/abevz/dibs/internal/integration"
)

const integrationCheck = "Integration block"

func checkIntegrationBlocks(ctx context.Context, c *client.Client, e OSExec, daemonReachable bool) []Result {
	var repos []core.Repository
	var err error
	if daemonReachable {
		repos, err = c.ListRepos(ctx, "")
	}

	results := evaluateIntegrationBlocks(e, repos)
	if err != nil {
		return append(results, Result{Name: integrationCheck, Status: "WARN", Message: "Could not list registered repositories"})
	}
	if len(results) == 0 {
		message := "No older blocks found in local registered checkouts"
		if !daemonReachable {
			message = "No older block found in the current checkout; registered checkouts unavailable"
		}
		return []Result{{Name: integrationCheck, Status: "ok", Message: message}}
	}
	return results
}

func evaluateIntegrationBlocks(e OSExec, repos []core.Repository) []Result {
	paths := map[string]struct{}{}
	if out, err := e.Command("git", "rev-parse", "--show-toplevel"); err == nil {
		paths[strings.TrimSuffix(string(out), "\n")] = struct{}{}
	}
	for _, repo := range repos {
		out, err := e.Command("git", "-C", repo.CanonicalGitDir, "worktree", "list", "--porcelain", "-z")
		if err != nil {
			continue
		}
		for record := range strings.SplitSeq(string(out), "\x00\x00") {
			fields := strings.Split(record, "\x00")
			path, ok := strings.CutPrefix(fields[0], "worktree ")
			if ok && !slices.Contains(fields, "bare") {
				paths[path] = struct{}{}
			}
		}
	}

	var results []Result
	for path := range paths {
		file := filepath.Join(path, "AGENTS.md")
		info, err := os.Stat(file)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		data, err := os.ReadFile(file)
		if err != nil {
			continue
		}
		var version string
		switch content := string(data); {
		case strings.Contains(content, integration.PreviousMarker):
			version = "v:2"
		case strings.Contains(content, integration.LegacyBeginMarker):
			version = "v:1"
		default:
			continue
		}
		results = append(results, Result{
			Name: integrationCheck, Status: "WARN",
			Message: fmt.Sprintf("%s has an older integration block (%s; current v:3)", file, version),
			Hint:    "Run dibs init in that checkout to update AGENTS.md",
		})
	}
	slices.SortFunc(results, func(a, b Result) int { return strings.Compare(a.Message, b.Message) })
	return results
}
