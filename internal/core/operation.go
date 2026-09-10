package core

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
)

// Operation kinds recorded in the idempotency ledger. The kind is part of the
// ledger identity, so the same operation_id presented for a different kind is
// a conflict rather than a replay (AFC-SDD-0159).
const (
	OperationKindClaim = "claim"
)

// MaxOperationIDLength bounds the opaque client-generated identifier. The
// coordinator never interprets the value's structure; it only requires enough
// length to be unguessable and little enough to index cheaply.
const MaxOperationIDLength = 128

// MinOperationIDLength rejects trivially guessable identifiers. Knowledge of
// an operation_id authorizes replay of that operation's recorded outcome —
// which for a claim includes its lease token — so a short or sequential value
// would be a capability an unrelated worker could enumerate.
const MinOperationIDLength = 8

// ValidateOperationID checks the opaque idempotency key at the protocol
// boundary. The value is client-generated (afctl uses a UUIDv4); the daemon
// treats it as an opaque capability and does not parse it. An empty value is
// valid at this layer and means "no idempotency requested" — callers that
// predate AFC-SDD-0159 keep working unchanged.
func ValidateOperationID(operationID string) error {
	if operationID == "" {
		return nil
	}
	if strings.TrimSpace(operationID) != operationID {
		return fmt.Errorf("operation_id must not have leading or trailing whitespace")
	}
	if len(operationID) < MinOperationIDLength {
		return fmt.Errorf("operation_id must be at least %d characters", MinOperationIDLength)
	}
	if len(operationID) > MaxOperationIDLength {
		return fmt.Errorf("operation_id must be at most %d characters", MaxOperationIDLength)
	}
	for _, r := range operationID {
		if r < 0x21 || r > 0x7e {
			return fmt.Errorf("operation_id must be printable ASCII without spaces")
		}
	}
	return nil
}

// OperationFingerprint hashes the canonical, request-defining arguments of a
// mutation. Two requests share a fingerprint exactly when replaying one in
// place of the other is indistinguishable to the caller.
//
// Fields are sorted and length-prefixed so that no combination of values can
// be re-partitioned into a different field set with the same digest — without
// the length prefix, {"a":"xy"} and {"ax":"y"} would collide.
//
// Only immutable request arguments belong here. Values the daemon derives at
// commit time (timestamps, generated tokens, expiry) must not participate:
// they differ on every attempt and would make every retry a false conflict.
func OperationFingerprint(fields map[string]string) string {
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	h := sha256.New()
	for _, k := range keys {
		fmt.Fprintf(h, "%d:%s=%d:%s\n", len(k), k, len(fields[k]), fields[k])
	}
	return hex.EncodeToString(h.Sum(nil))
}
