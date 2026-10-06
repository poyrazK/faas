package state

import (
	"context"
	"slices"
	"time"
)

type RuntimeUpgradeVerificationPhase string

const (
	RuntimeUpgradeVerificationPending  RuntimeUpgradeVerificationPhase = "pending"
	RuntimeUpgradeVerificationVerified RuntimeUpgradeVerificationPhase = "verified"
	RuntimeUpgradeVerificationBlocked  RuntimeUpgradeVerificationPhase = "blocked"
	RuntimeUpgradeVerificationExpired  RuntimeUpgradeVerificationPhase = "expired"
)

// Journal completion is historical evidence, never permission to drain, delete
// rollback artifacts or skip a fresh check. Participants and deadline are fixed.
type RuntimeUpgradeVerificationJournal struct {
	OperationID     string                          `json:"operation_id"`
	GatewaySessions []string                        `json:"gateway_sessions"`
	Phase           RuntimeUpgradeVerificationPhase `json:"phase"`
	Reason          string                          `json:"reason,omitempty"`
	CutoverAt       time.Time                       `json:"cutover_at"`
	CreatedAt       time.Time                       `json:"created_at"`
	DeadlineAt      time.Time                       `json:"deadline_at"`
	FinishedAt      time.Time                       `json:"finished_at,omitzero"`
	LastObservation *RuntimeUpgradeVerification     `json:"last_observation,omitempty"`
	LeaseToken      string                          `json:"-"`
	LeaseUntil      time.Time                       `json:"-"`
	NextAttemptAt   time.Time                       `json:"-"`
}

// Private apid controls enroll an already activated operation. Gateway restarts
// cannot silently substitute a new process identity or shrink the reviewed set.
type RuntimeUpgradeVerificationJournalStore interface {
	StartRuntimeUpgradeVerification(context.Context, string, string, []string) (RuntimeUpgradeVerificationJournal, error)
	RuntimeUpgradeVerificationJournal(context.Context, string, string) (RuntimeUpgradeVerificationJournal, error)
	ClaimRuntimeUpgradeVerification(context.Context) (RuntimeUpgradeOperationClaim, error)
	AdvanceRuntimeUpgradeVerification(context.Context, RuntimeUpgradeOperationClaim) (RuntimeUpgradeVerificationJournal, error)
}

func cloneRuntimeUpgradeVerificationJournal(j RuntimeUpgradeVerificationJournal) RuntimeUpgradeVerificationJournal {
	j.GatewaySessions = slices.Clone(j.GatewaySessions)
	if j.LastObservation != nil {
		observation := *j.LastObservation
		observation.GatewaySessions = slices.Clone(observation.GatewaySessions)
		j.LastObservation = &observation
	}
	return j
}

func runtimeUpgradeVerificationCheckpoint(j RuntimeUpgradeVerificationJournal, observation RuntimeUpgradeVerification, now time.Time) RuntimeUpgradeVerificationJournal {
	j.LastObservation = &observation
	j.Phase, j.Reason = RuntimeUpgradeVerificationPending, observation.Reason
	if !j.DeadlineAt.After(now) {
		j.Phase, j.Reason = RuntimeUpgradeVerificationExpired, "deadline_exceeded"
	} else if observation.Status == "verified" {
		j.Phase, j.Reason = RuntimeUpgradeVerificationVerified, ""
	} else if observation.Reason == "activation_changed" || observation.Reason == "activation_inputs_changed" || observation.Reason == "health_scope_unsupported" {
		j.Phase = RuntimeUpgradeVerificationBlocked
	}
	if j.Phase != RuntimeUpgradeVerificationPending {
		j.FinishedAt = now
	}
	j.LeaseToken, j.LeaseUntil = "", time.Time{}
	return j
}
