package state

import (
	"context"
	"errors"

	"github.com/google/uuid"
)

// ErrMigrationCommitUnresolved prevents an uncertain ownership result from
// authorizing destructive cleanup. The durable lease remains for recovery.
var ErrMigrationCommitUnresolved = errors.New("state: migration commit unresolved")

type MigrationCommitAttempt struct {
	InstanceID        string
	SourceNodeID      string
	DestinationNodeID string
	LeaseToken        string
	SourceWakeID      string
	DestinationWakeID string
}

type MigrationCommitResolution string

const (
	MigrationCommitRecovered MigrationCommitResolution = "committed"
	MigrationCommitAborted   MigrationCommitResolution = "aborted"
	MigrationCommitObsolete  MigrationCommitResolution = "obsolete"
	MigrationCommitRetained  MigrationCommitResolution = "retained"
)

// MigrationCommitRecoveryStore resolves ownership while serializing with the
// original commit. Aborted means the same source attempt was durably fenced
// against a late commit; obsolete means neither source nor destination owns
// serving capacity. Retained grants no permission to resume or destroy a VM.
type MigrationCommitRecoveryStore interface {
	ResolveInstanceMigrationCommit(context.Context, MigrationCommitAttempt) (MigrationCommitResolution, error)
}

func validateMigrationCommitAttempt(attempt MigrationCommitAttempt) error {
	if attempt.InstanceID == "" || attempt.SourceNodeID == "" || attempt.DestinationNodeID == "" ||
		attempt.SourceNodeID == attempt.DestinationNodeID || attempt.LeaseToken == "" {
		return ErrInvalidArgument
	}
	for _, wake := range []string{attempt.SourceWakeID, attempt.DestinationWakeID} {
		if wake != "" {
			if _, err := uuid.Parse(wake); err != nil {
				return ErrInvalidArgument
			}
		}
	}
	if attempt.SourceWakeID != "" && attempt.SourceWakeID == attempt.DestinationWakeID {
		return ErrInvalidArgument
	}
	return nil
}

// The caller holds the instance lock until it fences an abort or releases a
// read-only result. A destination row with another wake is deliberately kept;
// a stale orchestrator must not destroy a newer process on the same node.
func classifyMigrationCommit(instance Instance, attempt MigrationCommitAttempt) MigrationCommitResolution {
	if instance.NodeID == attempt.DestinationNodeID {
		if instance.State == string(StateRunning) && instance.LeaseToken == attempt.LeaseToken &&
			instance.MigratedFromNodeID != nil && *instance.MigratedFromNodeID == attempt.SourceNodeID &&
			(attempt.DestinationWakeID == "" || instance.WakeID == attempt.DestinationWakeID) {
			return MigrationCommitRecovered
		}
		return MigrationCommitRetained
	}
	if instance.NodeID == "" {
		return MigrationCommitRetained
	}
	if instance.NodeID != attempt.SourceNodeID {
		return MigrationCommitObsolete
	}
	if attempt.SourceWakeID != "" && instance.WakeID != attempt.SourceWakeID {
		return MigrationCommitRetained
	}
	if instance.State == string(StateStopped) || instance.State == string(StateFailed) || instance.State == string(StateEvictingAccountDeleting) {
		return MigrationCommitObsolete
	}
	if instance.State != string(StateMigrating) && instance.State != string(StateRunning) && instance.State != string(StateParked) {
		return MigrationCommitRetained
	}
	if instance.LeaseToken == attempt.LeaseToken {
		return MigrationCommitAborted // caller must durably clear this lease first
	}
	if instance.LeaseToken == "" && attempt.SourceWakeID != "" &&
		(instance.State == string(StateRunning) || instance.State == string(StateParked)) {
		return MigrationCommitAborted // another recovery already fenced the same wake
	}
	return MigrationCommitRetained
}
