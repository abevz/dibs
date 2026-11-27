package operatorcontrol

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/abevz/dibs/internal/client"
	"github.com/abevz/dibs/internal/core"
)

var globalTarget = core.ControlTarget{Kind: "global"}
var issueTarget = core.ControlTarget{Kind: "issue", ID: "11111111-1111-4111-8111-111111111111"}
var repoTarget = core.ControlTarget{Kind: "repo", ID: "22222222-2222-4222-8222-222222222222"}
var checkoutTarget = core.ControlTarget{Kind: "worktree", ID: "33333333-3333-4333-8333-333333333333"}

type fakeCoordinator struct {
	next      core.ControlStatus
	statusErr error
	lastQuery core.ControlStatusRequest
	lastAck   core.ControlAckRequest
	ack       core.ControlAck
}

func (f *fakeCoordinator) ControlStatus(_ context.Context, req core.ControlStatusRequest) (core.ControlStatus, error) {
	f.lastQuery = req
	return f.next, f.statusErr
}
func (f *fakeCoordinator) AcknowledgeControl(_ context.Context, req core.ControlAckRequest) (core.ControlAck, error) {
	f.lastAck = req
	return f.ack, nil
}

func fixture(t *testing.T) (Gate, core.ControlStatusRequest, *fakeCoordinator) {
	t.Helper()
	st := core.ControlStatus{
		Targets:      []core.ControlTarget{issueTarget, checkoutTarget, repoTarget, globalTarget},
		BoundTargets: []core.ControlTarget{issueTarget, checkoutTarget, repoTarget, globalTarget},
		WorktreeID:   checkoutTarget.ID, RepositoryID: repoTarget.ID,
		ExpiresAt: time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
	}
	f := &fakeCoordinator{next: st}
	cache := NewCache(filepath.Join(t.TempDir(), "control"), "/scratch/demo.sock")
	gate := Gate{Client: f, Cache: cache, Timeout: 250 * time.Millisecond}
	req := core.ControlStatusRequest{IssueID: issueTarget.ID, LeaseGeneration: 1, LeaseToken: "fixture-token", WorktreePath: "/scratch/main"}
	expiry, err := time.Parse(time.RFC3339, st.ExpiresAt)
	if err != nil {
		t.Fatal(err)
	}
	if err := gate.Enrolled(t.Context(), req, st, expiry); err != nil {
		t.Fatal(err)
	}
	return gate, req, f
}

func signal(target core.ControlTarget, action string, revision int64) core.ControlSignal {
	return core.ControlSignal{
		Control: action, SetBy: "operator", Reason: "operator_review", SetAt: "2026-11-27T00:00:00Z",
		Ref: &core.ControlRef{ControlTarget: target, Revision: revision},
	}
}

func TestGateOutagePolicyAndMatchingScopes(t *testing.T) {
	for _, tc := range []struct {
		name, action string
		target       core.ControlTarget
		transport    error
	}{
		{"issue pause disconnect", core.ControlPause, issueTarget, &net.OpError{Op: "dial", Net: "unix", Err: syscall.ECONNREFUSED}},
		{"worktree stop timeout", core.ControlStop, checkoutTarget, context.DeadlineExceeded},
		{"repo pause timeout", core.ControlPause, repoTarget, context.DeadlineExceeded},
		{"global stop disconnect", core.ControlStop, globalTarget, &net.OpError{Op: "dial", Net: "unix", Err: syscall.ECONNREFUSED}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g, req, f := fixture(t)
			f.statusErr = tc.transport
			empty := g.Check(t.Context(), req, false)
			if empty.Denied || empty.Warning != UnreachableWarning {
				t.Fatalf("empty outage = %+v", empty)
			}
			hb := core.HeartbeatResponse{ControlSignal: signal(tc.target, tc.action, 2)}
			if err := g.Heartbeat(t.Context(), req, hb, time.Now().Add(time.Hour)); err != nil {
				t.Fatal(err)
			}
			restarted := Gate{Client: f, Cache: Cache{path: g.Cache.path}, Timeout: g.Timeout}
			cached := restarted.Check(t.Context(), req, false)
			if !cached.Denied || cached.Message != restrictionMessage(hb.ControlSignal) {
				t.Fatalf("cached outage = %+v", cached)
			}

			outside := core.ControlStatusRequest{WorktreePath: "/scratch/unbound"}
			out := restarted.Check(t.Context(), outside, false)
			if out.Denied != (tc.target.Kind == "global") {
				t.Fatalf("unrelated checkout = %+v", out)
			}
			f.statusErr = nil
			f.next.Resumes = []core.ControlRef{{ControlTarget: tc.target, Revision: 3}}
			if cleared := restarted.Check(t.Context(), req, false); cleared.Denied || cleared.Warning != "" {
				t.Fatalf("explicit resume = %+v", cleared)
			}
			f.statusErr = tc.transport
			if clearOutage := restarted.Check(t.Context(), req, false); clearOutage.Denied || clearOutage.Warning != UnreachableWarning {
				t.Fatalf("cleared outage = %+v", clearOutage)
			}
		})
	}
}

func TestGateExplicitResumeAndOrdering(t *testing.T) {
	g, req, f := fixture(t)
	f.next.ControlSignal = signal(issueTarget, core.ControlPause, 8)
	if d := g.Check(t.Context(), req, false); !d.Denied {
		t.Fatal("first pause did not deny")
	}
	f.next.ControlSignal = core.ControlSignal{}
	f.next.Resumes = []core.ControlRef{{ControlTarget: globalTarget, Revision: 9}, {ControlTarget: issueTarget, Revision: 6}}
	if d := g.Check(t.Context(), req, false); !d.Denied {
		t.Fatal("unrelated/older resume cleared pause")
	}
	if len(f.lastQuery.Observed) != 1 || f.lastQuery.Observed[0].Revision != 8 {
		t.Fatalf("lookup omitted latched revision: %v", f.lastQuery.Observed)
	}
	f.next.Resumes = nil
	if d := g.Check(t.Context(), req, false); !d.Denied {
		t.Fatal("omitted signal cleared pause")
	}
	f.next.Resumes = []core.ControlRef{{ControlTarget: issueTarget, Revision: 10}}
	if d := g.Check(t.Context(), req, false); d.Denied {
		t.Fatal("matching newer resume did not clear")
	}
	f.next.Resumes = nil
	f.next.ControlSignal = signal(issueTarget, core.ControlPause, 8)
	if d := g.Check(t.Context(), req, false); d.Denied {
		t.Fatal("delayed pause re-applied after newer resume")
	}
}

func TestGateLeaseAndMalformedStateRemainFailClosed(t *testing.T) {
	g, req, f := fixture(t)
	f.statusErr = &client.ClientError{Code: core.ErrControlStopped}
	if d := g.Check(t.Context(), req, false); !d.Denied || d.Message != LeaseEnded {
		t.Fatalf("retired = %+v", d)
	}
	f.statusErr = context.DeadlineExceeded
	if d := g.Check(t.Context(), req, false); !d.Denied || d.Message != LeaseEnded {
		t.Fatalf("retired outage = %+v", d)
	}
	req.LeaseGeneration++
	if d := g.Check(t.Context(), req, false); !d.Denied || d.Message != LeaseEnded {
		t.Fatalf("unproved claimed deadline = %+v", d)
	}
	if err := g.Heartbeat(t.Context(), req, core.HeartbeatResponse{}, time.Now().Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	if d := g.Check(t.Context(), req, false); !d.Denied || d.Message != LeaseEnded {
		t.Fatalf("expired deadline = %+v", d)
	}

	f.statusErr = nil
	f.next.ControlSignal = signal(issueTarget, core.ControlPause, 2)
	f.next.Reason = "untrusted raw text"
	if d := g.Check(t.Context(), req, false); !d.Denied || d.Message != CheckFailed {
		t.Fatalf("malformed reply = %+v", d)
	}
	f.statusErr = errors.New("raw socket payload")
	if d := g.Check(t.Context(), req, false); !d.Denied || d.Message != CheckFailed {
		t.Fatalf("malformed read = %+v", d)
	}
	if err := os.WriteFile(g.Cache.path, []byte("unreadable"), 0600); err != nil {
		t.Fatal(err)
	}
	f.statusErr = context.DeadlineExceeded
	if d := g.Check(t.Context(), req, false); !d.Denied || d.Message != CheckFailed {
		t.Fatalf("corrupt cache = %+v", d)
	}
}
