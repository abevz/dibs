package core

import (
	"cmp"
	"encoding/json"
	"fmt"
	"strings"
)

const (
	ControlPause    = "pause"
	ControlStop     = "stop"
	ControlResume   = "resume"
	ControlAskOwner = "ask_owner"

	ErrControlRestricted = "control_restricted"
	ErrControlChanged    = "control_changed"
	ErrControlStopped    = "control_stopped"
)

type ControlTarget struct {
	Kind string `json:"target_kind"`
	ID   string `json:"target_id"`
}

type ControlRef struct {
	ControlTarget
	Revision int64 `json:"revision"`
}

type ControlSignal struct {
	Control string      `json:"control,omitempty"`
	Reason  string      `json:"reason,omitempty"`
	SetBy   string      `json:"set_by,omitempty"`
	SetAt   string      `json:"set_at,omitempty"`
	Ref     *ControlRef `json:"control_ref,omitempty"`
}

type ControlChangeRequest struct {
	ControlTarget
	Action      string `json:"action"`
	Reason      string `json:"reason,omitempty"`
	Actor       string `json:"actor"`
	OperationID string `json:"operation_id,omitempty"`
}

type ControlChange struct {
	ControlRef
	Action string `json:"action"`
	Reason string `json:"reason,omitempty"`
	SetBy  string `json:"set_by"`
	SetAt  string `json:"set_at"`
}

func (r *ControlChangeRequest) Normalize() error {
	switch r.Kind {
	case "global":
		if r.ID != "" {
			return fmt.Errorf("global requires an empty target_id")
		}
	case "issue", "repo", "worktree":
		if strings.TrimSpace(r.ID) == "" {
			return fmt.Errorf("target_id is required")
		}
	default:
		return fmt.Errorf("target_kind must be issue, repo, worktree, or global")
	}
	if strings.TrimSpace(r.Actor) == "" {
		return fmt.Errorf("actor is required")
	}
	switch r.Action {
	case ControlResume:
		if r.Reason != "" {
			return fmt.Errorf("resume takes no reason")
		}
	case ControlPause, ControlStop:
		if r.Reason == "" {
			r.Reason = "operator_requested"
		}
		if ControlReason(r.Reason) == "" {
			return fmt.Errorf("unknown reason code")
		}
	default:
		return fmt.Errorf("action must be pause, stop, or resume")
	}
	return ValidateOperationID(r.OperationID)
}

func ControlReason(code string) string {
	switch code {
	case "operator_requested":
		return "operator request"
	case "operator_review":
		return "operator review"
	case "maintenance":
		return "maintenance"
	default:
		return ""
	}
}

func ControlSeverity(action string) int {
	switch action {
	case ControlStop:
		return 3
	case ControlPause:
		return 2
	case ControlAskOwner:
		return 1
	default:
		return 0
	}
}

// CompareControlTargets orders targets of equal control severity.
func CompareControlTargets(a, b ControlTarget) int {
	rank := func(kind string) int {
		switch kind {
		case "issue":
			return 0
		case "worktree":
			return 1
		case "repo":
			return 2
		default:
			return 3
		}
	}
	if a.Kind != b.Kind {
		return cmp.Compare(rank(a.Kind), rank(b.Kind))
	}
	return cmp.Compare(a.ID, b.ID)
}

type ControlStatusRequest struct {
	IssueID         string       `json:"issue_id,omitempty"`
	LeaseToken      string       `json:"lease_token,omitempty"`
	LeaseGeneration int64        `json:"lease_generation,omitempty"`
	WorktreePath    string       `json:"worktree_path,omitempty"`
	Observed        []ControlRef `json:"observed,omitempty"`
}

type ControlStatus struct {
	ControlSignal
	Targets        []ControlTarget `json:"targets"`
	BoundTargets   []ControlTarget `json:"bound_targets"`
	WorktreeID     string          `json:"worktree_id,omitempty"`
	RepositoryID   string          `json:"repository_id,omitempty"`
	Resumes        []ControlRef    `json:"resumes,omitempty"`
	ExpiresAt      string          `json:"expires_at,omitempty"`
	HeartbeatCount int64           `json:"heartbeat_count,omitempty"`
}

type ControlAckRequest struct {
	ControlStatusRequest
	Revision    int64  `json:"revision"`
	Quiescent   bool   `json:"quiescent,omitempty"`
	OperationID string `json:"operation_id,omitempty"`
}

type ControlAck struct {
	State string `json:"state"`
}

type HeartbeatResponse struct {
	ExpiresAt string `json:"expires_at"`
	ControlSignal
	HeartbeatCount *int64 `json:"heartbeat_count,omitempty"`
}

// UnmarshalJSON also reads pre-control string outcomes from the operation ledger.
func (r *HeartbeatResponse) UnmarshalJSON(data []byte) error {
	if len(data) != 0 && data[0] == '"' {
		*r = HeartbeatResponse{}
		return json.Unmarshal(data, &r.ExpiresAt)
	}
	type response HeartbeatResponse
	return json.Unmarshal(data, (*response)(r))
}
