package firstuse

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gitTest(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

func TestDiscover(t *testing.T) {
	ctx := context.Background()
	t.Run("outside repository", func(t *testing.T) {
		_, err := Discover(ctx, t.TempDir(), Options{})
		if err == nil || !strings.Contains(err.Error(), "inside a Git repository") {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("main and linked worktree", func(t *testing.T) {
		base := t.TempDir()
		repo := filepath.Join(base, "hello")
		if err := os.Mkdir(repo, 0o700); err != nil {
			t.Fatal(err)
		}
		gitTest(t, repo, "init", "-b", "main")
		gitTest(t, repo, "config", "user.name", "Test")
		gitTest(t, repo, "config", "user.email", "test@example.invalid")
		gitTest(t, repo, "commit", "--allow-empty", "-m", "initial")
		main, err := Discover(ctx, repo, Options{})
		if err != nil {
			t.Fatal(err)
		}
		if main.Project != "hello" || main.Repo != "hello" || !main.IsMain || main.DefaultBranch != "main" {
			t.Fatalf("main = %+v", main)
		}
		linked := filepath.Join(base, "linked")
		gitTest(t, repo, "worktree", "add", "-b", "feature", linked)
		got, err := Discover(ctx, linked, Options{DefaultBranch: "main"})
		if err != nil {
			t.Fatal(err)
		}
		if got.GitDir != main.GitDir || got.Worktree != linked || got.IsMain || got.Branch != "feature" || got.Repo != "hello" {
			t.Fatalf("linked = %+v", got)
		}
	})
	t.Run("ambiguous default branch and long key", func(t *testing.T) {
		repo := filepath.Join(t.TempDir(), "very-long-repository-name")
		if err := os.Mkdir(repo, 0o700); err != nil {
			t.Fatal(err)
		}
		gitTest(t, repo, "init", "-b", "feature")
		_, err := Discover(ctx, repo, Options{})
		if err == nil || !strings.Contains(err.Error(), "--default-branch") {
			t.Fatalf("error = %v", err)
		}
		_, err = Discover(ctx, repo, Options{DefaultBranch: "main"})
		if err == nil || !strings.Contains(err.Error(), "--project") {
			t.Fatalf("error = %v", err)
		}
		got, err := Discover(ctx, repo, Options{DefaultBranch: "main", Project: "sample"})
		if err != nil || got.Project != "sample" {
			t.Fatalf("context = %+v, error = %v", got, err)
		}
	})
}

func TestLegacyCheckoutRegistrationMatchesGitCommonDir(t *testing.T) {
	root := t.TempDir()
	main := filepath.Join(root, "main")
	if err := os.Mkdir(main, 0o755); err != nil {
		t.Fatal(err)
	}
	gitTest(t, main, "init", "-b", "main")
	info, err := Discover(context.Background(), main, Options{Project: "demo", Repo: "app"})
	if err != nil {
		t.Fatal(err)
	}
	if !SameRegisteredGitDir(context.Background(), main, info.GitDir) {
		t.Fatal("legacy checkout path did not match its Git common directory")
	}
	foreign := filepath.Join(root, "foreign")
	if err := os.Mkdir(foreign, 0o755); err != nil {
		t.Fatal(err)
	}
	gitTest(t, foreign, "init", "-b", "main")
	if SameRegisteredGitDir(context.Background(), foreign, info.GitDir) {
		t.Fatal("foreign repository matched the registered Git directory")
	}
}
