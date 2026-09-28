// Package workpolicy defines the customer-facing semantics of application-keyed
// background work. Admission and claim implementations consume validated Policy
// values; this package does not acquire scheduler capacity or execute work.
package workpolicy

import (
	"fmt"
	"regexp"
	"time"
)

// PendingUpdates controls what admission does to older pending work in a lane.
// Running work is never replaced by either mode.
type PendingUpdates string

const (
	PendingAll        PendingUpdates = "all"
	PendingKeepLatest PendingUpdates = "keep_latest"
)

var policyNamePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)

const (
	MaxDebounce     = 24 * time.Hour
	MaxExpiresAfter = 30 * 24 * time.Hour
)

// Policy is one named app-scoped configuration. The first delivery stage
// admits one running invocation per work key; wider per-key concurrency needs
// a durable slot ledger and is deliberately rejected until that path exists.
type Policy struct {
	Name                     string
	MaxRunningPerKey         int
	PendingUpdates           PendingUpdates
	Debounce                 time.Duration
	ExpiresAfter             time.Duration
	MaxRunningPerFairnessKey int
}

// Validate rejects settings that the first policy claim implementation cannot
// enforce. Zero PendingUpdates selects the non-destructive default.
func (p Policy) Validate() error {
	if !policyNamePattern.MatchString(p.Name) {
		return fmt.Errorf("work policy name must match [a-z][a-z0-9-]{0,62}")
	}
	if p.MaxRunningPerKey != 1 {
		return fmt.Errorf("max_running_per_key must be 1")
	}
	if p.PendingUpdates != "" && p.PendingUpdates != PendingAll && p.PendingUpdates != PendingKeepLatest {
		return fmt.Errorf("pending_updates must be all or keep_latest")
	}
	if p.Debounce < 0 || p.Debounce > MaxDebounce {
		return fmt.Errorf("debounce must be between zero and 24 hours")
	}
	if p.ExpiresAfter < 0 || p.ExpiresAfter > MaxExpiresAfter {
		return fmt.Errorf("expires_after must be between zero and 30 days")
	}
	if p.ExpiresAfter > 0 && p.ExpiresAfter <= p.Debounce {
		return fmt.Errorf("expires_after must be greater than debounce")
	}
	if p.MaxRunningPerFairnessKey < 0 {
		return fmt.Errorf("max_running_per_fairness_key cannot be negative")
	}
	if p.MaxRunningPerFairnessKey > 0 {
		return fmt.Errorf("fairness-key concurrency is not yet available")
	}
	return nil
}

// AvailableAt uses the server's admission time, not an untrusted event time.
// A source's later scheduled time remains authoritative for delayed work.
func (p Policy) AvailableAt(admittedAt, sourceDueAt time.Time) time.Time {
	availableAt := admittedAt.Add(p.Debounce)
	if sourceDueAt.After(availableAt) {
		return sourceDueAt
	}
	return availableAt
}

// ExpiresAt returns no deadline when the policy does not set a pending TTL.
// The deadline is anchored at admission, so a retry cannot extend it.
func (p Policy) ExpiresAt(admittedAt time.Time) *time.Time {
	if p.ExpiresAfter == 0 {
		return nil
	}
	expiresAt := admittedAt.Add(p.ExpiresAfter)
	return &expiresAt
}
