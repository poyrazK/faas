package state

import "time"

// supersedesInFlightCanaries reports whether a newer canary going live may
// retire the canaries still rolling out in its scope (ADR-20261030102813,
// Hunt 14 H14-112). It may only when another serving deployment remains to
// take their traffic; otherwise the older canary is the sole serving
// predecessor and keeps its share.
func supersedesInFlightCanaries(serving []struct {
	ID    string
	Prior int
}) bool {
	for _, sibling := range serving {
		if sibling.Prior > 0 {
			return true
		}
	}
	return false
}

// endSupersededCanaryRolloutLocked ends a retired sibling's canary rollout as
// aborted when it was still in flight, mirroring the SQL in
// RetireLiveDeploymentSiblings and SupersedeInFlightCanarySiblings. A
// superseded row left at rolling_out reads as an in-flight rollout forever.
// It emits the rollout.aborted webhook the deployments trigger emits in
// Postgres.
func (m *MemStore) endSupersededCanaryRolloutLocked(before Deployment, other *Deployment, newerID string, now time.Time) {
	state := NormalizeRolloutState(before.RolloutState)
	if before.CanaryTotalSteps <= 0 || (state != "pending" && state != "rolling_out") {
		return
	}
	at := now
	other.RolloutState = "aborted"
	other.RolloutAbortedAt = &at
	other.RolloutAbortedReason = "superseded by deployment " + newerID
	m.enqueueRolloutOutcomeWebhooksLocked(before, *other)
}
