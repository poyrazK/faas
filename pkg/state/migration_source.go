package state

import (
	"context"
	"encoding/hex"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const (
	MigrationSourceBound    = "bound"
	MigrationSourceRetiring = "retiring"
	MigrationSourceRetired  = "retired"
	MigrationSourceResumed  = "resumed"
)

// MigrationSourceBinding identifies the original physical source, independently
// of the instance row's later location or a replacement VM with the same name.
type MigrationSourceBinding struct {
	InstanceID, SourceNodeID, SourceWakeID, LeaseToken string
	NativeGeneration, PhysicalBinding                  string
}

func (b MigrationSourceBinding) Validate() error {
	if err := validateSnapshotRecoveryIDs(b.InstanceID, b.SourceNodeID, b.SourceWakeID, b.NativeGeneration); err != nil {
		return err
	}
	digest, err := hex.DecodeString(b.PhysicalBinding)
	if err != nil || len(digest) != 32 || hex.EncodeToString(digest) != b.PhysicalBinding || b.LeaseToken == "" || len(b.LeaseToken) > api.MigrationSourceLeaseTokenMaxBytes {
		return ErrInvalidArgument
	}
	return nil
}

type MigrationSource struct {
	MigrationSourceBinding
	RAMMB, CPUMillicores, VCPU int
	Phase, Proof               string
}

func (s MigrationSource) Pending() bool {
	return s.Phase == MigrationSourceBound || s.Phase == MigrationSourceRetiring
}

type MigrationSourceStore interface {
	BindMigrationSource(context.Context, MigrationSourceBinding) (MigrationSource, error)
	RecordMigrationSourceSnapshot(context.Context, string, string, string, string) error
	MigrationSourceByLease(context.Context, string) (MigrationSource, error)
	RequestMigrationSourceRetirement(context.Context, string, string) (MigrationSource, error)
	AcknowledgeMigrationSource(context.Context, MigrationSourceBinding, string, string) error
	ListMigrationSourcesForRecovery(context.Context, string, time.Time, string, int) ([]MigrationSource, error)
	ListMigrationSourceLeasesForRecovery(context.Context, string, time.Time, string, int) ([]MigrationLease, error)
}
