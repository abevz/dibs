// Package relocate validates a move of registered Git checkout paths.
package relocate

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/abevz/dibs/internal/core"
)

// Plan proves that the old and new paths refer to the same Git checkout and
// maps every registered worktree. The old path must remain accessible while
// the move is committed; a temporary symlink is sufficient.
func Plan(ctx context.Context, oldCanonical, newCanonical string, worktrees []core.Worktree) ([]core.WorktreePathChange, error) {
	if !filepath.IsAbs(oldCanonical) || !filepath.IsAbs(newCanonical) ||
		filepath.Clean(oldCanonical) != oldCanonical || filepath.Clean(newCanonical) != newCanonical {
		return nil, fmt.Errorf("repository paths must be clean absolute paths")
	}
	if oldCanonical == newCanonical {
		changes := make([]core.WorktreePathChange, 0, len(worktrees))
		for _, wt := range worktrees {
			changes = append(changes, core.WorktreePathChange{ID: wt.ID, OldPath: wt.AbsolutePath, NewPath: wt.AbsolutePath})
		}
		return changes, nil
	}
	if err := sameGitPath(ctx, oldCanonical, newCanonical, "--git-common-dir"); err != nil {
		return nil, fmt.Errorf("repository identity: %w", err)
	}
	oldParent, newParent := filepath.Dir(oldCanonical), filepath.Dir(newCanonical)
	oldMain, newMain := "", ""
	// init records a checkout's .git directory as canonical. In that layout
	// its main worktree is the parent of canonical, not a sibling of it.
	for _, wt := range worktrees {
		if !wt.IsMain || wt.AbsolutePath != oldParent {
			continue
		}
		if filepath.Base(oldCanonical) != filepath.Base(newCanonical) {
			return nil, fmt.Errorf("new Git directory must retain %s basename", filepath.Base(oldCanonical))
		}
		oldParent = filepath.Dir(wt.AbsolutePath)
		newParent = filepath.Dir(filepath.Dir(newCanonical))
		oldMain, newMain = wt.AbsolutePath, filepath.Dir(newCanonical)
		break
	}
	changes := make([]core.WorktreePathChange, 0, len(worktrees))
	seen := map[string]bool{}
	for _, wt := range worktrees {
		rel, err := filepath.Rel(oldParent, wt.AbsolutePath)
		if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return nil, fmt.Errorf("worktree %s is outside old repository parent; relocate it explicitly first", wt.ID)
		}
		newPath := filepath.Join(newParent, rel)
		if wt.AbsolutePath == oldCanonical {
			newPath = newCanonical
		} else if wt.AbsolutePath == oldMain {
			newPath = newMain
		}
		if seen[newPath] {
			return nil, fmt.Errorf("multiple worktrees would use %s", newPath)
		}
		seen[newPath] = true
		if err := sameCheckout(ctx, wt.AbsolutePath, newPath); err != nil {
			return nil, fmt.Errorf("worktree %s identity: %w", wt.ID, err)
		}
		changes = append(changes, core.WorktreePathChange{ID: wt.ID, OldPath: wt.AbsolutePath, NewPath: newPath})
	}
	return changes, nil
}

func sameCheckout(ctx context.Context, oldPath, newPath string) error {
	for _, arg := range []string{"--git-common-dir", "--absolute-git-dir"} {
		if err := sameGitPath(ctx, oldPath, newPath, arg); err != nil {
			return err
		}
	}
	return nil
}

func sameGitPath(ctx context.Context, oldPath, newPath, arg string) error {
	old, err := gitPath(ctx, oldPath, arg)
	if err != nil {
		return fmt.Errorf("old path %s: %w", oldPath, err)
	}
	newValue, err := gitPath(ctx, newPath, arg)
	if err != nil {
		return fmt.Errorf("new path %s: %w", newPath, err)
	}
	if old != newValue {
		return fmt.Errorf("%s and %s are different Git checkouts", oldPath, newPath)
	}
	return nil
}

func gitPath(ctx context.Context, path, arg string) (string, error) {
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("path must be an accessible directory: %s", path)
	}
	cmd := exec.CommandContext(ctx, "git", "-C", path, "rev-parse", "--path-format=absolute", arg)
	// Git environment overrides can make a foreign path look like the same
	// checkout, defeating the identity check. Probe the path itself.
	for _, env := range os.Environ() {
		if !strings.HasPrefix(env, "GIT_") {
			cmd.Env = append(cmd.Env, env)
		}
	}
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git rev-parse %s failed: %w", arg, err)
	}
	value := strings.TrimSpace(string(output))
	if !filepath.IsAbs(value) {
		value = filepath.Join(path, value)
	}
	return filepath.EvalSymlinks(value)
}
