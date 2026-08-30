package sqlite

import "context"

// coordinationProofPoint identifies a transaction boundary used by the
// deterministic concurrency proof matrix. The context key and hook type are
// private to this package, so production callers cannot activate the hook.
// Without a package-internal hook in the context, every call is a no-op.
type coordinationProofPoint string

const (
	coordinationProofClaimBeforeCommit     coordinationProofPoint = "claim_before_commit"
	coordinationProofHeartbeatBeforeCommit coordinationProofPoint = "heartbeat_before_commit"
	coordinationProofHandoffBeforeCommit   coordinationProofPoint = "handoff_before_commit"
	coordinationProofUpdateAfterAuthorize  coordinationProofPoint = "update_after_authorize"
)

type coordinationProofHook func(coordinationProofPoint)
type coordinationProofHookContextKey struct{}

func runCoordinationProofHook(ctx context.Context, point coordinationProofPoint) {
	hook, _ := ctx.Value(coordinationProofHookContextKey{}).(coordinationProofHook)
	if hook != nil {
		hook(point)
	}
}
