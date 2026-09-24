package core

import "time"

type Health struct {
	Name       string    `json:"name"`
	Status     string    `json:"status"`
	DBPath     string    `json:"db_path"`
	SocketPath string    `json:"socket_path"`
	Time       time.Time `json:"time"`
	// Revision is the git commit SHA the running daemon binary was built
	// from ("unknown" if not embedded at build time). See build.Revision.
	Revision string `json:"revision,omitempty"`
	// Nil means an older daemon did not report this field. Never expose the token.
	OperatorTokenConfigured *bool         `json:"operator_token_configured,omitempty"`
	Safety                  *SafetyHealth `json:"safety,omitempty"`
}

// SafetyHealth is local, bounded operational evidence. Startup checks are
// reported as such; the daemon does not run integrity_check on every request.
type SafetyHealth struct {
	SingletonLockHeld           bool             `json:"singleton_lock_held"`
	MigrationsVerifiedAtStartup bool             `json:"migrations_verified_at_startup"`
	IntegrityVerifiedAtStartup  bool             `json:"integrity_verified_at_startup"`
	IntegrityPolicy             string           `json:"integrity_policy"`
	ActiveLeases                int64            `json:"active_leases"`
	ExpiredLeases               int64            `json:"expired_leases"`
	StaleRejections             int64            `json:"stale_rejections"`
	DurableClaimConflicts       int64            `json:"durable_claim_conflicts"`
	LatestMigration             string           `json:"latest_migration"`
	MutationCounters            MutationCounters `json:"mutation_counters"`
}

type MutationCounters struct {
	ClaimConflicts      int64 `json:"claim_conflicts"`
	LeaseLossFailures   int64 `json:"lease_loss_failures"`
	TransactionFailures int64 `json:"transaction_failures"`
	DBBusyFailures      int64 `json:"db_busy_failures"`
	StaleRejections     int64 `json:"stale_rejections"`
}

type StaleRejectionCount struct {
	IssueID                          string `json:"issue_id"`
	Kind                             string `json:"kind"`
	Holder                           string `json:"holder"`
	ReasonCode                       string `json:"reason_code"`
	Count                            int64  `json:"count"`
	FirstSeenAt                      string `json:"first_seen_at"`
	LastSeenAt                       string `json:"last_seen_at"`
	LastPresentedGeneration          int64  `json:"last_presented_generation"`
	CurrentGenerationAtLastRejection int64  `json:"current_generation_at_last_rejection"`
	LastInvocationMode               string `json:"last_invocation_mode"`
}

type SafetySnapshot struct {
	ActiveLeases    int64                 `json:"active_leases"`
	ExpiredLeases   int64                 `json:"expired_leases"`
	StaleRejections int64                 `json:"stale_rejections"`
	ClaimConflicts  int64                 `json:"claim_conflicts"`
	LatestMigration string                `json:"latest_migration"`
	TopStaleHolders []StaleRejectionCount `json:"top_stale_holders"`
}
