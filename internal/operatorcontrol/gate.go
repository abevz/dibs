package operatorcontrol

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"time"

	"github.com/abevz/dibs/internal/client"
	"github.com/abevz/dibs/internal/core"
	"github.com/google/uuid"
)

const UnreachableWarning = "Dibs is unreachable. Operator control could not be checked."
const CheckFailed = "Dibs operator control check failed."
const LeaseEnded = "Dibs lease ended. Claim and launch a new attempt before using tools."

type Coordinator interface {
	ControlStatus(context.Context, core.ControlStatusRequest) (core.ControlStatus, error)
	AcknowledgeControl(context.Context, core.ControlAckRequest) (core.ControlAck, error)
}

type Decision struct {
	Denied  bool
	Message string
	Warning string
}

type Gate struct {
	Client  Coordinator
	Cache   Cache
	Timeout time.Duration
}

func validTarget(target core.ControlTarget) bool {
	switch target.Kind {
	case "issue", "repo", "worktree":
		_, err := uuid.Parse(target.ID)
		return err == nil
	case "global":
		return target.ID == ""
	default:
		return false
	}
}

func validSignal(signal core.ControlSignal) bool {
	if signal.Control == "" {
		return signal.Ref == nil && signal.Reason == "" && signal.SetAt == "" && signal.SetBy == ""
	}
	_, err := time.Parse(time.RFC3339, signal.SetAt)
	return err == nil && signal.SetBy == "operator" &&
		signal.Ref != nil && signal.Ref.Revision > 0 && validTarget(signal.Ref.ControlTarget) &&
		core.ControlSeverity(signal.Control) > 0 && core.ControlReason(signal.Reason) != ""
}

func validStatus(status core.ControlStatus) bool {
	if status.HeartbeatCount < 0 || !validSignal(status.ControlSignal) || !slices.Contains(status.Targets, core.ControlTarget{Kind: "global"}) ||
		!slices.Contains(status.BoundTargets, core.ControlTarget{Kind: "global"}) {
		return false
	}
	for _, target := range status.Targets {
		if !validTarget(target) {
			return false
		}
	}
	for _, target := range status.BoundTargets {
		if !slices.Contains(status.Targets, target) {
			return false
		}
	}
	for _, ref := range status.Resumes {
		if ref.Revision <= 0 || !slices.Contains(status.Targets, ref.ControlTarget) {
			return false
		}
	}
	if status.WorktreeID != "" {
		for _, target := range []core.ControlTarget{
			{Kind: "worktree", ID: status.WorktreeID}, {Kind: "repo", ID: status.RepositoryID},
		} {
			if !slices.Contains(status.Targets, target) {
				return false
			}
		}
	} else if status.RepositoryID != "" {
		return false
	}
	return status.Ref == nil || slices.Contains(status.Targets, status.Ref.ControlTarget)
}

func (g Gate) limit(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, g.Timeout)
}

func (g Gate) Enrolled(ctx context.Context, req core.ControlStatusRequest, status core.ControlStatus, expiry time.Time) error {
	if !validStatus(status) {
		return fmt.Errorf("invalid enrollment response")
	}
	ctx, cancel := g.limit(ctx)
	defer cancel()
	return g.Cache.access(ctx, true, func(s *state) error {
		s.bind(req, status)
		proof := s.Attempts[attemptKey(req)]
		if proof.Deadline.IsZero() || status.HeartbeatCount >= proof.HeartbeatCount {
			proof.Deadline = expiry
			proof.HeartbeatCount = status.HeartbeatCount
		}
		s.Attempts[attemptKey(req)] = proof
		s.apply(status.ControlSignal, status.Resumes)
		return nil
	})
}

func (g Gate) Heartbeat(ctx context.Context, req core.ControlStatusRequest, result core.HeartbeatResponse, expiry time.Time) error {
	if !validSignal(result.ControlSignal) {
		return fmt.Errorf("invalid heartbeat control")
	}
	cacheCtx, cancelCache := g.limit(ctx)
	defer cancelCache()
	if err := g.Cache.access(cacheCtx, true, func(s *state) error {
		key := attemptKey(req)
		proof := s.Attempts[key]
		if result.HeartbeatCount == nil || *result.HeartbeatCount >= proof.HeartbeatCount {
			proof.Deadline = expiry
			if result.HeartbeatCount != nil {
				proof.HeartbeatCount = *result.HeartbeatCount
			}
		}
		s.Attempts[key] = proof
		s.apply(result.ControlSignal, nil)
		return nil
	}); err != nil {
		return err
	}
	if result.Ref != nil {
		bounded, cancel := g.limit(ctx)
		defer cancel()
		// The cached denial remains active even when acknowledgement
		// fails. A later heartbeat or hook can retry the same observation.
		_, _ = g.acknowledge(bounded, req, result.ControlSignal, false)
	}
	return nil
}

func (g Gate) Check(ctx context.Context, req core.ControlStatusRequest, quiescent bool) Decision {
	denied := Decision{Denied: true, Message: CheckFailed}
	snapshotCtx, cancel := g.limit(ctx)
	defer cancel()
	if err := g.Cache.access(snapshotCtx, false, func(s *state) error {
		for _, target := range s.targets(req) {
			if entry := s.Observations[targetKey(target)]; entry.Signal.Ref != nil &&
				!slices.Contains(req.Observed, *entry.Signal.Ref) {
				req.Observed = append(req.Observed, *entry.Signal.Ref)
			}
		}
		return nil
	}); err != nil {
		return denied
	}

	lookupCtx, cancelLookup := g.limit(ctx)
	result, lookupErr := g.Client.ControlStatus(lookupCtx, req)
	cancelLookup()
	var apiErr *client.ClientError
	leaseLost := errors.As(lookupErr, &apiErr) &&
		(apiErr.Code == core.ErrLeaseExpired || apiErr.Code == core.ErrControlStopped)
	unavailable := client.ControlUnavailable(lookupErr)
	if !leaseLost && !unavailable && (lookupErr != nil || !validStatus(result)) {
		return denied
	}

	var liveExpiry time.Time
	if !unavailable && !leaseLost && req.IssueID != "" {
		var err error
		liveExpiry, err = time.Parse(time.RFC3339, result.ExpiresAt)
		if err != nil {
			return denied
		}
		if !time.Now().Before(liveExpiry) {
			leaseLost = true
		}
	}
	var effective core.ControlSignal
	var leaseEnded bool
	cacheCtx, cancelCache := g.limit(ctx)
	defer cancelCache()
	if err := g.Cache.access(cacheCtx, !unavailable, func(s *state) error {
		proof := s.Attempts[attemptKey(req)]
		if leaseLost {
			proof.Retired = true
			s.Attempts[attemptKey(req)] = proof
		}
		leaseEnded = proof.Retired || (unavailable && req.IssueID != "" &&
			(proof.Deadline.IsZero() || !time.Now().Before(proof.Deadline)))
		if leaseEnded {
			return nil
		}
		targets := s.targets(req)
		if !unavailable {
			s.bind(req, result)
			if req.IssueID != "" {
				proof = s.Attempts[attemptKey(req)]
				if proof.Deadline.IsZero() || result.HeartbeatCount >= proof.HeartbeatCount {
					proof.Deadline = liveExpiry
					proof.HeartbeatCount = result.HeartbeatCount
					s.Attempts[attemptKey(req)] = proof
				}
			}
			s.apply(result.ControlSignal, result.Resumes)
			targets = result.Targets
		}
		for _, target := range targets {
			signal := s.Observations[targetKey(target)].Signal
			severity, prior := core.ControlSeverity(signal.Control), core.ControlSeverity(effective.Control)
			if severity > prior || (severity > 0 && severity == prior &&
				core.CompareControlTargets(signal.Ref.ControlTarget, effective.Ref.ControlTarget) < 0) {
				effective = signal
			}
		}
		return nil
	}); err != nil {
		return denied
	}

	if leaseEnded {
		return Decision{Denied: true, Message: LeaseEnded}
	}
	if effective.Ref == nil {
		if unavailable {
			return Decision{Warning: UnreachableWarning}
		}
		return Decision{}
	}

	message := restrictionMessage(effective)
	if !unavailable && req.IssueID != "" && result.Ref != nil && *result.Ref == *effective.Ref {
		bounded, cancel := g.limit(ctx)
		defer cancel()
		out, err := g.acknowledge(bounded, req, effective, quiescent)
		if err == nil && out.State == "stopped" {
			if g.Cache.access(bounded, true, func(s *state) error {
				key := attemptKey(req)
				proof := s.Attempts[key]
				proof.Retired = true
				s.Attempts[key] = proof
				return nil
			}) == nil {
				message = "Dibs handed off and released the stopped attempt (" + core.ControlReason(effective.Reason) + ")."
			}
		}
	}
	return Decision{Denied: true, Message: message}
}

func (g Gate) acknowledge(ctx context.Context, req core.ControlStatusRequest, signal core.ControlSignal, quiescent bool) (core.ControlAck, error) {
	req.Observed = nil
	quiescent = quiescent && signal.Control == core.ControlStop
	key := attemptKey(req) + "\x00" + req.WorktreePath + "\x00" +
		strconv.FormatInt(signal.Ref.Revision, 10) + "\x00" + strconv.FormatBool(quiescent)
	return g.Client.AcknowledgeControl(ctx, core.ControlAckRequest{
		ControlStatusRequest: req, Revision: signal.Ref.Revision, Quiescent: quiescent,
		OperationID: uuid.NewSHA1(uuid.NameSpaceOID, []byte(key)).String(),
	})
}

func restrictionMessage(signal core.ControlSignal) string {
	reason := core.ControlReason(signal.Reason)
	switch signal.Control {
	case core.ControlPause:
		return "Dibs paused tools (" + reason + ")."
	case core.ControlStop:
		return "Dibs stop pending (" + reason + ")."
	default:
		return "Dibs is waiting for the owner (" + reason + ")."
	}
}
