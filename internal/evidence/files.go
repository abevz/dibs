// Package evidence manages private durable copies, not Git artifacts.
package evidence

import (
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/abevz/dibs/internal/core"
)

func Root() (string, error) {
	root := os.Getenv("DIBS_EVIDENCE_DIR")
	if root == "" {
		state := os.Getenv("XDG_STATE_HOME")
		if state == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return "", err
			}
			state = filepath.Join(home, ".local", "state")
		}
		root = filepath.Join(state, "dibs", "evidence")
	}
	if !filepath.IsAbs(root) {
		return "", fmt.Errorf("evidence root must be absolute")
	}
	return filepath.Clean(root), nil
}

// OpenRegular rejects symlinks and detects replacement between lstat and open.
func OpenRegular(path string) (*os.File, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() {
		return nil, fmt.Errorf("evidence source must be a regular file")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	after, err := f.Stat()
	if err != nil || !os.SameFile(before, after) {
		f.Close()
		return nil, fmt.Errorf("evidence source changed while opening")
	}
	return f, nil
}
func Hash(path string) (string, error) {
	f, err := OpenRegular(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err = io.Copy(h, f); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

func Copy(source, projectID, issueID, id string) (core.Evidence, error) {
	root, err := Root()
	if err != nil {
		return core.Evidence{}, err
	}
	source, err = filepath.Abs(source)
	if err != nil {
		return core.Evidence{}, err
	}
	f, err := OpenRegular(source)
	if err != nil {
		return core.Evidence{}, err
	}
	defer f.Close()
	// All directory components after root are daemon-generated UUIDs.
	if err = os.MkdirAll(root, 0700); err != nil {
		return core.Evidence{}, err
	}
	r, err := os.OpenRoot(root)
	if err != nil {
		return core.Evidence{}, err
	}
	defer r.Close()
	if err = r.MkdirAll(filepath.Join(projectID, issueID), 0700); err != nil {
		return core.Evidence{}, err
	}
	relative := filepath.Join(projectID, issueID, id)
	dst, err := r.OpenFile(relative, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return core.Evidence{}, err
	}
	h := sha256.New()
	n, copyErr := io.Copy(io.MultiWriter(dst, h), f)
	if copyErr == nil {
		copyErr = dst.Sync()
	}
	closeErr := dst.Close()
	if copyErr == nil {
		copyErr = closeErr
	}
	if copyErr != nil {
		_ = r.Remove(relative)
		return core.Evidence{}, copyErr
	}
	return core.Evidence{ID: id, IssueID: issueID, Path: filepath.Join(root, relative), SourcePath: source, SHA256: fmt.Sprintf("%x", h.Sum(nil)), Size: n}, nil
}

// Remove only deletes managed paths beneath the configured root.
func Remove(path string) error {
	root, err := Root()
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return fmt.Errorf("evidence path outside managed root")
	}
	r, err := os.OpenRoot(root)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer r.Close()
	err = r.Remove(rel)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// Check verifies bytes as well as the caller-declared revision and review status.
func Check(items []core.Evidence, kinds []string, diff string) error {
	if err := core.ValidateEvidenceRequirement(kinds, diff); err != nil {
		return err
	}
	for _, kind := range kinds {
		found := false
		for _, e := range items {
			if e.Kind != kind || e.DiffHash != diff || e.PrunedAt != "" || (kind == "review" && e.Status != "PASS") {
				continue
			}
			hash, err := Hash(e.Path)
			if err == nil && hash == e.SHA256 {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("missing intact %s evidence for diff %s (review requires PASS)", kind, diff)
		}
	}
	return nil
}

// CleanupSource only removes a redundant, verified temporary copy. Unknown files
// have no registry proof and are deliberately outside this operation.
func CleanupSource(e core.Evidence, apply bool) (bool, error) {
	if e.PrunedAt != "" || e.SourcePath == e.Path {
		return false, nil
	}
	tmp, err := filepath.EvalSymlinks(os.TempDir())
	if err != nil {
		return false, err
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(e.SourcePath))
	if err != nil {
		return false, nil
	}
	rel, err := filepath.Rel(tmp, parent)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return false, nil
	}
	hash, err := Hash(e.Path)
	if err != nil || hash != e.SHA256 {
		return false, nil
	}
	hash, err = Hash(e.SourcePath)
	if err != nil || hash != e.SHA256 {
		return false, nil
	}
	if apply {
		if err = os.Remove(e.SourcePath); err != nil {
			return false, err
		}
	}
	return true, nil
}
