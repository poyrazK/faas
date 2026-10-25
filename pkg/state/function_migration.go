package state

// adr: 20261018030613. The source's physical recovery follows a durable ownership fence.

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/runtimeadmission"
)

// Versioned opaque source tokens cannot be reused through the legacy Phase-2
// path after their durable source receipt has been removed.
const FunctionMigrationTokenPrefix = runtimeadmission.FunctionMigrationLeasePrefix

type FunctionMigrationSource struct {
	InstanceID, LeaseToken, SourceNodeID, SourceWakeID string
	Phase                                              string
	CaptureToken                                       string // nonempty only for a measured source permission
}

const (
	FunctionMigrationPreparing  = "preparing"
	FunctionMigrationPrepared   = "prepared"
	FunctionMigrationRecovering = "recovering"
	FunctionMigrationRecovered  = "recovered"
	FunctionMigrationRetired    = "retired"
)

// FunctionMigrationSourceStore is optional; ordinary production migration
// keeps its existing path. Only schedd calls Fence/Complete. vmmd registers
// capture identity and reads the resulting authority without writing instances.
type FunctionMigrationSourceStore interface {
	RegisterFunctionMigrationSource(context.Context, FunctionMigrationSource, string, string) error
	PrepareFunctionMigrationSource(context.Context, string, string) error
	GetFunctionMigrationSource(context.Context, string, string) (FunctionMigrationSource, error)
	GetFunctionMigrationSourceByInstance(context.Context, string) (FunctionMigrationSource, error)
	ListFunctionMigrationSourceInstances(context.Context, string) ([]string, error)
	FenceFunctionMigrationSource(context.Context, string, string) (MigrationCommitResolution, error)
	CompleteFunctionMigrationSource(context.Context, string, string, bool) error
	ListExpiredFunctionMigrationSources(context.Context, time.Time, int) ([]FunctionMigrationSource, error)
}

// The original commit requires the original token and therefore cannot succeed
// once schedd has replaced it. Keep MIGRATING until physical recovery settles.
func FunctionMigrationRollbackToken(token string) string { return "function-rollback:" + token }

func classifyFunctionMigrationSource(ins Instance, source FunctionMigrationSource) MigrationCommitResolution {
	if ins.NodeID != source.SourceNodeID {
		if ins.NodeID == "" {
			return MigrationCommitRetained
		}
		return MigrationCommitObsolete
	}
	if ins.WakeID != source.SourceWakeID {
		return MigrationCommitRetained
	}
	if ins.State == string(StateStopped) || ins.State == string(StateFailed) || ins.State == string(StateEvictingAccountDeleting) {
		return MigrationCommitObsolete
	}
	if ins.State == string(StateMigrating) && (ins.LeaseToken == source.LeaseToken || ins.LeaseToken == FunctionMigrationRollbackToken(source.LeaseToken)) {
		return MigrationCommitAborted
	}
	if (ins.State == string(StateRunning) || ins.State == string(StateParked)) && ins.LeaseToken == "" {
		return MigrationCommitAborted
	}
	return MigrationCommitRetained
}
