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

func TestPlanInitGitDirAndRepairLinkedWorktreeAfterMove(t *testing.T) {
	root := t.TempDir()
	oldParent := filepath.Join(root, "old")
	oldMain := filepath.Join(oldParent, "main")
	if err := os.MkdirAll(oldMain, 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, "-C", oldMain, "init", "-b", "main")
	git(t, "-C", oldMain, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-m", "initial")
	git(t, "-C", oldMain, "worktree", "add", "-b", "feature", filepath.Join(oldParent, "feature"))
	newParent := filepath.Join(root, "new")
	if err := os.Rename(oldParent, newParent); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(newParent, oldParent); err != nil {
		t.Fatal(err)
	}
	newMain := filepath.Join(newParent, "main")
	newFeature := filepath.Join(newParent, "feature")
	changes, err := Plan(context.Background(), filepath.Join(oldMain, ".git"), filepath.Join(newMain, ".git"), []core.Worktree{
		{ID: "main", AbsolutePath: oldMain, IsMain: true},
		{ID: "feature", AbsolutePath: filepath.Join(oldParent, "feature")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 2 || changes[0].NewPath != newMain || changes[1].NewPath != newFeature {
		t.Fatalf("init-created mapping = %+v", changes)
	}
	git(t, "-C", newMain, "worktree", "repair", newFeature)
	if err := os.Remove(oldParent); err != nil {
		t.Fatal(err)
	}
	git(t, "-C", newFeature, "rev-parse", "--git-common-dir")
}

func TestPlanIgnoresGitEnvironmentOverrides(t *testing.T) {
	root := t.TempDir()
	registered := filepath.Join(root, "registered")
	foreign := filepath.Join(root, "foreign")
	git(t, "init", registered)
	git(t, "init", foreign)
	t.Setenv("GIT_DIR", filepath.Join(registered, ".git"))
	if _, err := Plan(context.Background(), registered, foreign, nil); err == nil || !strings.Contains(err.Error(), "different Git checkouts") {
		t.Fatalf("Git environment overrode path identity: %v", err)
	}
}
