package state

import (
	"encoding/json"
	"time"

	"github.com/onebox-faas/faas/pkg/environmentsync"
)

// Finishing a report must not overwrite a wakeup for intent it never observed.
// Use the final observation rather than the claim version: enforcement and
// console writes may have advanced intent before the worker's final reread.
// An attempt that failed before planning falls back to its claimed version.
func gitOpsNextAttempt(lease EnvironmentGitOpsLease, raw json.RawMessage, version int64, overrides []environmentsync.Override, now, next time.Time) time.Time {
	observedVersion := lease.Source.IntentVersion
	var plan environmentsync.Plan
	if json.Unmarshal(raw, &plan) == nil && plan.Manager == lease.Source.ID && plan.Revision == lease.Revision.ID &&
		plan.Generation == lease.Source.Generation && plan.DesiredDigest == lease.Revision.Digest && len(plan.Hash) == 64 && plan.ObservedVersion >= 0 {
		observedVersion = plan.ObservedVersion
	} else {
		plan = environmentsync.Plan{}
	}
	if version != observedVersion {
		return now.UTC()
	}
	// Expiry changes planning without a database write. Wake at the earliest
	// override used by this plan, including one that expired during the run.
	// Old expired records must not cause stable reports to spin indefinitely.
	expirations := make(map[string]time.Time, len(overrides))
	for _, override := range overrides {
		expirations[(environmentsync.Field{Resource: override.Resource, Path: override.Path}).Key()] = override.ExpiresAt
	}
	for _, change := range plan.Changes {
		if change.Action != "overridden" {
			continue
		}
		expires, exists := expirations[(environmentsync.Field{Resource: change.Resource, Path: change.Path}).Key()]
		if exists && expires.Before(next) {
			if expires.Before(now) {
				expires = now
			}
			next = expires
		}
	}
	return next.UTC()
}
