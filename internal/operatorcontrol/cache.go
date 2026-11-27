// Package operatorcontrol implements the trusted local tool gate. Its cache
// stores only coordinator decisions and lease deadlines, never credentials.
package operatorcontrol

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/abevz/dibs/internal/core"
	"golang.org/x/sys/unix"
)

type Cache struct {
	path string
}

func NewCache(stateDir, socket string) Cache {
	return Cache{path: filepath.Join(stateDir, fmt.Sprintf("%x.json", sha256.Sum256([]byte(filepath.Clean(socket)))))}
}

type observation struct {
	Revision int64              `json:"revision"`
	Signal   core.ControlSignal `json:"signal"`
}

type attempt struct {
	Deadline       time.Time            `json:"deadline"`
	Targets        []core.ControlTarget `json:"targets,omitempty"`
	Retired        bool                 `json:"retired,omitempty"`
	HeartbeatCount int64                `json:"heartbeat_count,omitempty"`
}

type state struct {
	Observations map[string]observation          `json:"observations"`
	Attempts     map[string]attempt              `json:"attempts"`
	Checkouts    map[string][]core.ControlTarget `json:"checkouts"`
}

func targetKey(t core.ControlTarget) string { return t.Kind + ":" + t.ID }

func attemptKey(req core.ControlStatusRequest) string {
	return req.IssueID + "@" + strconv.FormatInt(req.LeaseGeneration, 10)
}

func (c Cache) access(ctx context.Context, update bool, f func(*state) error) error {
	dir := filepath.Dir(c.path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return fmt.Errorf("control cache directory must be private")
	}

	lock, err := os.OpenFile(c.path+".lock", os.O_CREATE|os.O_RDWR|unix.O_NOFOLLOW, 0600)
	if err != nil {
		return err
	}
	defer lock.Close()
	for {
		err := unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, unix.EWOULDBLOCK) && !errors.Is(err, unix.EINTR) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}

	data := state{}
	content, err := os.ReadFile(c.path)
	if err == nil {
		if err := json.Unmarshal(content, &data); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if data.Observations == nil {
		data.Observations = make(map[string]observation)
	}
	if data.Attempts == nil {
		data.Attempts = make(map[string]attempt)
	}
	if data.Checkouts == nil {
		data.Checkouts = make(map[string][]core.ControlTarget)
	}
	if err := f(&data); err != nil || !update {
		return err
	}
	return c.write(data)
}

func (c Cache) write(data state) error {
	f, err := os.CreateTemp(filepath.Dir(c.path), ".observations-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	err = json.NewEncoder(f).Encode(data)
	if err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(f.Name(), c.path)
}

func (s *state) apply(signal core.ControlSignal, resumes []core.ControlRef) {
	for _, resume := range resumes {
		key := targetKey(resume.ControlTarget)
		if resume.Revision > s.Observations[key].Revision {
			s.Observations[key] = observation{Revision: resume.Revision}
		}
	}
	if signal.Ref != nil {
		key := targetKey(signal.Ref.ControlTarget)
		if signal.Ref.Revision > s.Observations[key].Revision {
			s.Observations[key] = observation{Revision: signal.Ref.Revision, Signal: signal}
		}
	}
}

func (s *state) targets(req core.ControlStatusRequest) []core.ControlTarget {
	targets := []core.ControlTarget{{Kind: "global"}}
	if req.IssueID != "" {
		targets = append(targets, core.ControlTarget{Kind: "issue", ID: req.IssueID})
		targets = append(targets, s.Attempts[attemptKey(req)].Targets...)
	}
	return append(targets, s.Checkouts[req.WorktreePath]...)
}

func (s *state) bind(req core.ControlStatusRequest, status core.ControlStatus) {
	if req.IssueID != "" {
		key := attemptKey(req)
		proof := s.Attempts[key]
		proof.Targets = status.BoundTargets
		s.Attempts[key] = proof
	}
	if req.WorktreePath != "" {
		delete(s.Checkouts, req.WorktreePath)
	}
	if req.WorktreePath != "" && status.WorktreeID != "" {
		s.Checkouts[req.WorktreePath] = []core.ControlTarget{
			{Kind: "worktree", ID: status.WorktreeID},
			{Kind: "repo", ID: status.RepositoryID},
		}
	}
}
