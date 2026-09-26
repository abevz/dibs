package relocate

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abevz/dibs/internal/core"
)

func git(t *testing.T, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

func TestPlanSameGitWorktreesAndRejectsOtherCheckout(t *testing.T) {
	root := t.TempDir()
	newParent := filepath.Join(root, "new")
	main := filepath.Join(newParent, "main")
	if err := os.MkdirAll(main, 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, "-C", main, "init", "-b", "main")
	git(t, "-C", main, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-m", "initial")
	other := filepath.Join(newParent, "other")
	git(t, "-C", main, "worktree", "add", "-b", "other", other)
	oldParent := filepath.Join(root, "old")
	if err := os.Symlink(newParent, oldParent); err != nil {
		t.Fatal(err)
	}
	oldMain := filepath.Join(oldParent, "main")
	changes, err := Plan(context.Background(), oldMain, main, []core.Worktree{
		{ID: "main", AbsolutePath: oldMain},
		{ID: "other", AbsolutePath: filepath.Join(oldParent, "other")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 2 || changes[0].NewPath != main || changes[1].NewPath != other {
		t.Fatalf("unexpected mapping: %+v", changes)
	}
	foreign := filepath.Join(root, "foreign")
	git(t, "init", foreign)
	if _, err := Plan(context.Background(), oldMain, foreign, nil); err == nil || !strings.Contains(err.Error(), "different Git checkouts") {
		t.Fatalf("foreign checkout accepted: %v", err)
	}
	if _, err := Plan(context.Background(), oldMain, main, []core.Worktree{{ID: "missing", AbsolutePath: filepath.Join(oldParent, "missing")}}); err == nil {
		t.Fatal("stale worktree registration accepted")
	}
}
