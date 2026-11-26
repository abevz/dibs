package doctor

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abevz/dibs/internal/core"
)

func TestDoctorReportsV2InRegisteredBareRepoCheckout(t *testing.T) {
	dir := t.TempDir()
	bare := filepath.Join(dir, "registered.git")
	checkout := filepath.Join(dir, "linked checkout")
	for _, cmd := range [][]string{
		{"init", "--bare", "-q", "-b", "main", bare},
		{"--git-dir", bare, "worktree", "add", "--orphan", "-b", "main", checkout},
	} {
		if out, err := exec.Command("git", cmd...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", cmd, err, out)
		}
	}
	file := filepath.Join(checkout, "AGENTS.md")
	repos := []core.Repository{{CanonicalGitDir: bare}}
	for _, version := range []string{"v:2", "v:3"} {
		content := "<!-- BEGIN DIBS INTEGRATION " + version + " -->\n<!-- END DIBS INTEGRATION -->\n"
		if err := os.WriteFile(file, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
		results := evaluateIntegrationBlocks(realExec{}, repos)
		if version == "v:2" {
			found := false
			for _, result := range results {
				if strings.Contains(result.Message, file) && strings.Contains(result.Message, "v:2") &&
					result.Status == "WARN" && strings.Contains(result.Hint, "dibs init") {
					found = true
				}
			}
			if !found {
				t.Fatalf("missing v:2 warning for %s: %+v", file, results)
			}
		} else {
			for _, result := range results {
				if strings.Contains(result.Message, file) {
					t.Fatalf("unexpected warning for v:3: %+v", result)
				}
			}
		}
	}
}
