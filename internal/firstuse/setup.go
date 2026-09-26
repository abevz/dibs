package firstuse

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/abevz/dibs/internal/client"
	"github.com/abevz/dibs/internal/core"
)

type GitContext struct {
	Project       string `json:"project"`
	Repo          string `json:"repo"`
	Worktree      string `json:"worktree"`
	GitDir        string `json:"git_dir"`
	Branch        string `json:"branch"`
	DefaultBranch string `json:"default_branch"`
	HeadCommit    string `json:"head_commit,omitempty"`
	IsMain        bool   `json:"is_main"`
}

type Options struct {
	Project       string
	Repo          string
	DefaultBranch string
}

var invalidKey = regexp.MustCompile(`[^a-z0-9]+`)

func Discover(ctx context.Context, dir string, opts Options) (GitContext, error) {
	git := func(args ...string) (string, error) {
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir = dir
		out, err := cmd.Output()
		return strings.TrimSpace(string(out)), err
	}
	root, err := git("rev-parse", "--show-toplevel")
	if err != nil {
		return GitContext{}, fmt.Errorf("run dibs init inside a Git repository: %w", err)
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return GitContext{}, err
	}
	common, err := git("rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return GitContext{}, fmt.Errorf("read Git common directory: %w", err)
	}
	common, err = filepath.EvalSymlinks(common)
	if err != nil {
		return GitContext{}, err
	}
	workGit, err := git("rev-parse", "--path-format=absolute", "--git-dir")
	if err != nil {
		return GitContext{}, fmt.Errorf("read Git worktree directory: %w", err)
	}
	workGit, err = filepath.EvalSymlinks(workGit)
	if err != nil {
		return GitContext{}, err
	}
	branch, err := git("symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil {
		return GitContext{}, fmt.Errorf("detached HEAD: choose a branch before dibs init")
	}
	defaultBranch := opts.DefaultBranch
	if defaultBranch == "" {
		remoteHead, _ := git("symbolic-ref", "--quiet", "--short", "refs/remotes/origin/HEAD")
		defaultBranch = strings.TrimPrefix(remoteHead, "origin/")
	}
	if defaultBranch == "" {
		if branch != "main" && branch != "master" {
			return GitContext{}, fmt.Errorf("which branch is the repository default? rerun with --default-branch <name>")
		}
		defaultBranch = branch
	}
	name := opts.Repo
	if name == "" {
		name = filepath.Base(root)
		if filepath.Base(common) == ".git" || filepath.Base(common) == ".bare" {
			name = filepath.Base(filepath.Dir(common))
		}
	}
	key := opts.Project
	if key == "" {
		key = invalidKey.ReplaceAllString(strings.ToLower(name), "-")
		key = strings.Trim(key, "-")
		if key == "" {
			return GitContext{}, fmt.Errorf("cannot derive a project key; rerun with --project <key>")
		}
		if key[0] >= '0' && key[0] <= '9' {
			key = "p-" + key
		}
		if len(key) > 16 {
			return GitContext{}, fmt.Errorf("derived project key %q exceeds 16 characters; rerun with --project <key>", key)
		}
	}
	if err := core.ValidateCreateProject(key, name); err != nil {
		return GitContext{}, err
	}
	head, _ := git("rev-parse", "--verify", "HEAD")
	return GitContext{Project: key, Repo: name, Worktree: root, GitDir: common,
		Branch: branch, DefaultBranch: defaultBranch, HeadCommit: head, IsMain: common == workGit}, nil
}

// Register reconciles the discovered mapping via the daemon API only.
func Register(ctx context.Context, c *client.Client, info GitContext, explicitProject bool) (core.Project, core.Repository, core.Worktree, error) {
	projects, err := c.ListProjects(ctx)
	if err != nil {
		return core.Project{}, core.Repository{}, core.Worktree{}, err
	}
	var project core.Project
	for _, p := range projects {
		if p.Key == info.Project {
			project = p
			break
		}
	}
	allRepos, err := c.ListRepos(ctx, "")
	if err != nil {
		return project, core.Repository{}, core.Worktree{}, err
	}
	for _, existing := range allRepos {
		if !sameRegisteredGitDir(ctx, existing.CanonicalGitDir, info.GitDir) {
			continue
		}
		if existing.ProjectID != project.ID {
			for _, p := range projects {
				if p.ID == existing.ProjectID {
					return project, core.Repository{}, core.Worktree{}, fmt.Errorf("this Git repository is already registered under project %q; rerun with --project %s", p.Key, p.Key)
				}
			}
			return project, core.Repository{}, core.Worktree{}, fmt.Errorf("this Git repository is already registered in another project; inspect dibs repo list")
		}
	}
	if project.ID == "" {
		project, err = c.CreateProject(ctx, info.Project, info.Repo, "")
		if err != nil {
			// Concurrent init may have created the same project.
			projects, listErr := c.ListProjects(ctx)
			if listErr != nil {
				return project, core.Repository{}, core.Worktree{}, err
			}
			for _, p := range projects {
				if p.Key == info.Project {
					project = p
					break
				}
			}
			if project.ID == "" {
				return project, core.Repository{}, core.Worktree{}, err
			}
		}
	}
	repos, err := c.ListRepos(ctx, info.Project)
	if err != nil {
		return project, core.Repository{}, core.Worktree{}, err
	}
	var repo core.Repository
	for _, r := range repos {
		if sameRegisteredGitDir(ctx, r.CanonicalGitDir, info.GitDir) {
			repo = r
			break
		}
	}
	if repo.ID != "" && repo.LogicalName != info.Repo {
		return project, repo, core.Worktree{}, fmt.Errorf("this Git repository is registered as %q; rerun with --repo %s", repo.LogicalName, repo.LogicalName)
	}
	if repo.ID == "" {
		if len(repos) > 0 && !explicitProject {
			return project, repo, core.Worktree{}, fmt.Errorf("project %q already contains another repository; rerun with --project %s to confirm this mapping, or choose another key", info.Project, info.Project)
		}
		for _, r := range repos {
			if r.LogicalName == info.Repo {
				return project, repo, core.Worktree{}, fmt.Errorf("repository name %q already points to another Git directory; choose --repo <name>", info.Repo)
			}
		}
		repo, _, err = c.CreateRepo(ctx, core.CreateRepoRequest{Project: info.Project, LogicalName: info.Repo,
			CanonicalGitDir: info.GitDir, DefaultBranch: info.DefaultBranch})
		if err != nil {
			repos, listErr := c.ListRepos(ctx, info.Project)
			if listErr != nil {
				return project, repo, core.Worktree{}, err
			}
			for _, r := range repos {
				if sameRegisteredGitDir(ctx, r.CanonicalGitDir, info.GitDir) {
					repo = r
					break
				}
			}
			if repo.ID == "" {
				return project, repo, core.Worktree{}, err
			}
		}
	}
	wt, err := c.RegisterWorktree(ctx, core.CreateWorktreeRequest{Repo: repo.ID, AbsolutePath: info.Worktree,
		Branch: info.Branch, HeadCommit: info.HeadCommit, IsMain: info.IsMain})
	return project, repo, wt, err
}

// Legacy registrations may point at a checkout, whereas init records the Git
// common directory. After relocation both forms must identify one repository.
func sameRegisteredGitDir(ctx context.Context, registered, common string) bool {
	if filepath.Clean(registered) == filepath.Clean(common) {
		return true
	}
	cmd := exec.CommandContext(ctx, "git", "-C", registered, "rev-parse", "--path-format=absolute", "--git-common-dir")
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	got, err := filepath.EvalSymlinks(strings.TrimSpace(string(out)))
	if err != nil {
		return false
	}
	want, err := filepath.EvalSymlinks(common)
	return err == nil && got == want
}
