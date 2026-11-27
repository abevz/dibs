package sqlite

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/abevz/dibs/internal/core"
	"github.com/abevz/dibs/migrations"
)

func controlFixture(t *testing.T) (*Store, core.Issue, core.ClaimResponse, string, core.Worktree, core.Worktree) {
	t.Helper()
	ctx := t.Context()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	db.SetMaxOpenConns(1)
	if _, err := db.ExecContext(ctx, "PRAGMA foreign_keys=ON"); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(ctx, db, migrations.FS); err != nil {
		t.Fatal(err)
	}
	s := NewStore(db)
	if _, err := s.CreateProject(ctx, "demo", "Demo", ""); err != nil {
		t.Fatal(err)
	}
	base := t.TempDir()
	repo, _, err := s.CreateRepo(ctx, "demo", core.CreateRepoRequest{
		Project: "demo", LogicalName: "test-repo", CanonicalGitDir: filepath.Join(base, ".git"),
	})
	if err != nil {
		t.Fatal(err)
	}
	worktrees := make([]core.Worktree, 0, 2)
	for _, name := range []string{"first", "sibling"} {
		root := filepath.Join(base, name)
		if err := os.Mkdir(root, 0700); err != nil {
			t.Fatal(err)
		}
		wt, _, err := s.UpsertWorktree(ctx, repo.ID, core.CreateWorktreeRequest{Repo: repo.ID, AbsolutePath: root, Branch: name})
		if err != nil {
			t.Fatal(err)
		}
		worktrees = append(worktrees, wt)
	}
	issue, err := s.CreateIssue(ctx, "demo", core.CreateIssueRequest{ScopeKind: "project", Title: "control", Actor: "worker"})
	if err != nil {
		t.Fatal(err)
	}
	claim, err := s.ClaimIssue(ctx, issue.ID, "worker", 3600)
	if err != nil {
		t.Fatal(err)
	}
	return s, issue, claim, repo.ID, worktrees[0], worktrees[1]
}

func applyControl(t *testing.T, s *Store, kind, id, action string) core.ControlChange {
	t.Helper()
	out, err := s.ChangeControl(t.Context(), core.ControlChangeRequest{
		ControlTarget: core.ControlTarget{Kind: kind, ID: id}, Action: action,
		Actor: "local-operator", Reason: map[string]string{core.ControlStop: "maintenance"}[action],
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestControlScopeEnrollmentAndHeartbeatReplay(t *testing.T) {
	s, issue, claim, repoID, first, sibling := controlFixture(t)
	ctx := t.Context()
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(first.AbsolutePath, alias); err != nil {
		t.Fatal(err)
	}
	req := core.ControlStatusRequest{IssueID: issue.ShortID, LeaseToken: claim.LeaseToken, LeaseGeneration: claim.LeaseGeneration, WorktreePath: alias}
	firstStatus, err := s.ControlStatus(ctx, req, true)
	if err != nil || firstStatus.WorktreeID != first.ID || firstStatus.ExpiresAt != claim.ExpiresAt {
		t.Fatalf("enrollment = %+v, err = %v", firstStatus, err)
	}
	if _, err := s.CreateNote(ctx, issue.ID, core.CreateNoteRequest{Author: "agent", Body: "control=stop"}); err != nil {
		t.Fatal(err)
	}

	global := applyControl(t, s, "global", "", core.ControlPause)
	repo := applyControl(t, s, "repo", "test-repo", core.ControlStop)
	if repo.ID != repoID || repo.Revision <= global.Revision {
		t.Fatalf("resolved repository/revision = %+v", repo)
	}
	worktree := applyControl(t, s, "worktree", alias, core.ControlStop)
	narrow := applyControl(t, s, "issue", issue.ShortID, core.ControlPause)
	cases := []struct {
		name, path string
		claimed    bool
		want       int64
	}{
		{"bound checkout", "", true, worktree.Revision},
		{"sibling still inherits bound checkout", sibling.AbsolutePath, true, worktree.Revision},
		{"unclaimed sibling only sees repo/global", sibling.AbsolutePath, false, repo.Revision},
		{"unclaimed bound checkout sees worktree", alias, false, worktree.Revision},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			read := core.ControlStatusRequest{WorktreePath: tc.path}
			if tc.claimed {
				read = req
				read.WorktreePath = tc.path
			}
			got, err := s.ControlStatus(ctx, read, false)
			if err != nil || got.Ref == nil || got.Ref.Revision != tc.want {
				t.Fatalf("status = %+v, err = %v, want revision %d", got, err, tc.want)
			}
		})
	}
	changed := req
	changed.WorktreePath = sibling.AbsolutePath
	_, err = s.ControlStatus(ctx, changed, true)
	requireCoordinationErrorCode(t, err, core.ErrConflict)
	changed.LeaseGeneration++
	_, err = s.ControlStatus(ctx, changed, false)
	requireCoordinationErrorCode(t, err, core.ErrLeaseExpired)

	hb := core.HeartbeatRequest{LeaseToken: claim.LeaseToken, LeaseGeneration: claim.LeaseGeneration, TTLSeconds: 7200, OperationID: "heartbeat-control-0001"}
	snapshot, err := s.HeartbeatLeaseStatus(ctx, issue.ID, hb, time.Now().UTC())
	if err != nil || snapshot.Ref == nil || snapshot.Ref.Revision != worktree.Revision || snapshot.HeartbeatCount == nil || *snapshot.HeartbeatCount != 1 {
		t.Fatalf("heartbeat = %+v, err = %v", snapshot, err)
	}
	resume := applyControl(t, s, "worktree", first.ID, core.ControlResume)
	applyControl(t, s, "repo", repo.ID, core.ControlResume)
	req.Observed = []core.ControlRef{worktree.ControlRef, narrow.ControlRef}
	now, err := s.ControlStatus(ctx, req, false)
	if err != nil || now.Ref == nil || now.Ref.Revision != narrow.Revision || now.ExpiresAt != snapshot.ExpiresAt || len(now.Resumes) != 1 || now.Resumes[0].Revision != resume.Revision {
		t.Fatalf("status after resume = %+v, err = %v", now, err)
	}
	replay, err := s.HeartbeatLeaseStatus(ctx, issue.ID, hb, time.Now().UTC().Add(time.Minute))
	if err != nil || replay.Ref == nil || *replay.Ref != *snapshot.Ref || replay.ExpiresAt != snapshot.ExpiresAt {
		t.Fatalf("old heartbeat = %+v, err = %v", replay, err)
	}
	hb.OperationID = "heartbeat-control-0002"
	fresh, err := s.HeartbeatLeaseStatus(ctx, issue.ID, hb, time.Now().UTC().Add(time.Minute))
	if err != nil || fresh.Ref == nil || fresh.Ref.Revision != narrow.Revision || fresh.ExpiresAt <= snapshot.ExpiresAt {
		t.Fatalf("fresh heartbeat = %+v, err = %v", fresh, err)
	}

	legacy, _ := json.Marshal(fresh.ExpiresAt)
	if _, err := s.db.ExecContext(ctx, `UPDATE operations SET outcome_json=? WHERE operation_id=?`, string(legacy), hb.OperationID); err != nil {
		t.Fatal(err)
	}
	old, err := s.HeartbeatLeaseStatus(ctx, issue.ID, hb, time.Now().UTC())
	if err != nil || old.ExpiresAt != fresh.ExpiresAt || old.Ref != nil {
		t.Fatalf("pre-upgrade outcome = %+v, err = %v", old, err)
	}

	applyControl(t, s, "issue", issue.ID, core.ControlResume)
	applyControl(t, s, "global", "", core.ControlResume)
	cleared, err := s.ControlStatus(ctx, req, false)
	if err != nil || cleared.Ref != nil {
		t.Fatalf("note influenced clear status: %+v, %v", cleared, err)
	}
}

func TestControlPauseAndStopLifecycle(t *testing.T) {
	s, issue, claim, _, worktree, sibling := controlFixture(t)
	ctx := t.Context()
	req := core.ControlStatusRequest{IssueID: issue.ID, LeaseToken: claim.LeaseToken, LeaseGeneration: claim.LeaseGeneration, WorktreePath: worktree.AbsolutePath}
	if _, err := s.ControlStatus(ctx, req, true); err != nil {
		t.Fatal(err)
	}
	pause := applyControl(t, s, "issue", issue.ID, core.ControlPause)
	var workers sync.WaitGroup
	for range 6 {
		workers.Go(func() {
			_, err := s.AcknowledgeControl(ctx, core.ControlAckRequest{ControlStatusRequest: req, Revision: pause.Revision})
			if err != nil {
				t.Error(err)
			}
		})
	}
	workers.Wait()
	notes, err := s.ListNotes(ctx, issue.ID)
	if err != nil || len(notes) != 1 || !strings.HasPrefix(notes[0].Body, "paused at ") {
		t.Fatalf("pause notes = %v, err = %v", notes, err)
	}
	closeReq := core.CloseIssueRequest{Actor: "worker", Resolution: "done", ExpectedVersion: core.LatestIssueVersion, LeaseToken: claim.LeaseToken, LeaseGeneration: claim.LeaseGeneration}
	_, err = s.CloseIssue(ctx, issue.ID, closeReq)
	requireCoordinationErrorCode(t, err, core.ErrControlRestricted)
	applyControl(t, s, "issue", issue.ID, core.ControlResume)

	// A stop seen in the sibling checkout must block ordinary completion
	// even though the runner is bound to the first checkout.
	stop := applyControl(t, s, "worktree", sibling.ID, core.ControlStop)
	req.WorktreePath = sibling.AbsolutePath
	ack := core.ControlAckRequest{ControlStatusRequest: req, Revision: stop.Revision, OperationID: "stop-pending-test"}
	out, err := s.AcknowledgeControl(ctx, ack)
	if err != nil || out.State != "stop_pending" {
		t.Fatalf("pending stop = %+v, err = %v", out, err)
	}
	_, lease, err := s.GetIssue(ctx, issue.ID)
	if err != nil || lease == nil || lease.LeaseGeneration != claim.LeaseGeneration {
		t.Fatalf("pending stop lost lease: %+v, %v", lease, err)
	}
	_, err = s.CloseIssue(ctx, issue.ID, closeReq)
	requireCoordinationErrorCode(t, err, core.ErrControlRestricted)

	applyControl(t, s, "worktree", sibling.ID, core.ControlResume)
	ack.Quiescent, ack.OperationID = true, "stop-cancelled-by-resume"
	_, err = s.AcknowledgeControl(ctx, ack)
	requireCoordinationErrorCode(t, err, core.ErrControlChanged)
	stop = applyControl(t, s, "worktree", sibling.ID, core.ControlStop)
	ack.Revision, ack.OperationID = stop.Revision, "stop-handoff-replay"
	withWrongToken := ack
	withWrongToken.LeaseToken = "bad-token"
	_, err = s.AcknowledgeControl(ctx, withWrongToken)
	requireCoordinationErrorCode(t, err, core.ErrLeaseExpired)
	for range 2 {
		out, err = s.AcknowledgeControl(ctx, ack)
		if err != nil || out.State != "stopped" {
			t.Fatalf("stopped outcome = %+v, err = %v", out, err)
		}
	}
	open, lease, err := s.GetIssue(ctx, issue.ID)
	if err != nil || lease != nil || open.Status != "open" {
		t.Fatalf("controlled handoff = %s, lease = %+v, err = %v", open.Status, lease, err)
	}
	notes, err = s.ListNotes(ctx, issue.ID)
	if err != nil || len(notes) != 2 {
		t.Fatalf("handoff notes = %v, err = %v", notes, err)
	}
	foundHandoff := false
	for _, n := range notes {
		foundHandoff = foundHandoff || strings.HasPrefix(n.Body, "HANDOFF: stopped by operator (maintenance)")
	}
	if !foundHandoff {
		t.Fatalf("missing generated stop handoff: %v", notes)
	}

	// Dispatch eligibility is unchanged. A replacement generation can claim
	// even before the remaining worktree control is cleared.
	replacement, err := s.ClaimIssue(ctx, issue.ID, "replacement", 3600)
	if err != nil || replacement.LeaseGeneration <= claim.LeaseGeneration {
		t.Fatalf("replacement claim = generation %d, err = %v", replacement.LeaseGeneration, err)
	}
	applyControl(t, s, "worktree", sibling.ID, core.ControlResume)
	_, err = s.ControlStatus(ctx, req, false)
	requireCoordinationErrorCode(t, err, core.ErrControlStopped)
}

func TestControlChangeOrderAndRetry(t *testing.T) {
	s, issue, _, _, _, _ := controlFixture(t)
	ctx := t.Context()
	req := core.ControlChangeRequest{
		ControlTarget: core.ControlTarget{Kind: "issue", ID: issue.ShortID},
		Action:        core.ControlPause, Actor: "owner", OperationID: "control-change-retry",
	}
	first, err := s.ChangeControl(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	later := applyControl(t, s, "issue", issue.ID, core.ControlResume)
	reopened := NewStore(s.db)
	replay, err := reopened.ChangeControl(ctx, req)
	if err != nil || replay != first || later.Revision <= replay.Revision {
		t.Fatalf("replay/revision = %+v (latest %+v), err = %v", replay, later, err)
	}
	req.Action = core.ControlStop
	_, err = reopened.ChangeControl(ctx, req)
	requireIdempotencyConflict(t, err)
}

func TestControlRejectsExpiredOrForceReleasedAcknowledgement(t *testing.T) {
	for _, mode := range []string{"expired", "operator released"} {
		t.Run(mode, func(t *testing.T) {
			s, issue, claim, _, wt, _ := controlFixture(t)
			ctx := t.Context()
			req := core.ControlStatusRequest{IssueID: issue.ID, LeaseToken: claim.LeaseToken,
				LeaseGeneration: claim.LeaseGeneration, WorktreePath: wt.AbsolutePath}
			if _, err := s.ControlStatus(ctx, req, true); err != nil {
				t.Fatal(err)
			}
			stop := applyControl(t, s, "issue", issue.ID, core.ControlStop)
			if mode == "expired" {
				if _, err := s.db.ExecContext(ctx, `UPDATE leases SET expires_at=? WHERE issue_id=?`,
					time.Now().UTC().Add(-time.Second).Format(time.RFC3339), issue.ID); err != nil {
					t.Fatal(err)
				}
			} else {
				_, err := s.OperatorReleaseIssue(ctx, issue.ID, core.OperatorReleaseIssueRequest{
					ExpectedVersion: claim.Version, Actor: "operator", Reason: "recovery",
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			_, err := s.AcknowledgeControl(ctx, core.ControlAckRequest{ControlStatusRequest: req, Revision: stop.Revision, Quiescent: true})
			requireCoordinationErrorCode(t, err, core.ErrLeaseExpired)
			if _, err := s.ClaimIssue(ctx, issue.ID, "replacement", 3600); err != nil {
				t.Fatal(err)
			}
			_, err = s.AcknowledgeControl(ctx, core.ControlAckRequest{ControlStatusRequest: req, Revision: stop.Revision, Quiescent: true})
			requireCoordinationErrorCode(t, err, core.ErrLeaseExpired)
			notes, err := s.ListNotes(ctx, issue.ID)
			if err != nil || len(notes) != 0 {
				t.Fatalf("stale acknowledgement wrote notes: %v, %v", notes, err)
			}
		})
	}
}

func TestControlRejectsAmbiguousRepoAndReplaysOrphanWorktreeChange(t *testing.T) {
	s, _, _, _, _, sibling := controlFixture(t)
	ctx := t.Context()
	if _, err := s.CreateProject(ctx, "another", "Another", ""); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.CreateRepo(ctx, "another", core.CreateRepoRequest{
		Project: "another", LogicalName: "test-repo", CanonicalGitDir: filepath.Join(t.TempDir(), ".git"),
	}); err != nil {
		t.Fatal(err)
	}
	_, err := s.ChangeControl(ctx, core.ControlChangeRequest{ControlTarget: core.ControlTarget{Kind: "repo", ID: "test-repo"},
		Action: core.ControlPause, Actor: "operator"})
	requireCoordinationErrorCode(t, err, core.ErrValidationFailed)

	req := core.ControlChangeRequest{
		ControlTarget: core.ControlTarget{Kind: "worktree", ID: sibling.ID},
		Action:        core.ControlPause, Actor: "operator", OperationID: "worktree-control-replay",
	}
	first, err := s.ChangeControl(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DeleteWorktree(ctx, sibling.ID); err != nil {
		t.Fatal(err)
	}
	replay, err := s.ChangeControl(ctx, req)
	if err != nil || replay != first {
		t.Fatalf("orphan replay = %+v, err = %v, want %+v", replay, err, first)
	}
}
