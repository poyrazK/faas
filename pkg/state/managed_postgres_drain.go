package state

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// ManagedPostgresDrainFrame authorizes retirement of one fenced incarnation.
// It is never an app-wide drain receipt or evidence of SQL-session quiescence.
type ManagedPostgresDrainFrame struct {
	ID, CutoverID, AppID, DeploymentID, InstanceID, NodeID, WakeID, OwnerNodeID string
	OwnerRevision                                                               int64
	FencedAt                                                                    time.Time
}

func (f ManagedPostgresDrainFrame) Valid() bool {
	for _, value := range []string{f.ID, f.CutoverID, f.AppID, f.DeploymentID, f.InstanceID, f.NodeID, f.WakeID, f.OwnerNodeID} {
		u, err := uuid.Parse(value)
		if err != nil || u == uuid.Nil || u.String() != value {
			return false
		}
	}
	return f.OwnerRevision > 0 && !f.FencedAt.IsZero()
}

func (f ManagedPostgresDrainFrame) Equal(other ManagedPostgresDrainFrame) bool {
	stamp, otherStamp := f.FencedAt, other.FencedAt
	f.FencedAt, other.FencedAt = time.Time{}, time.Time{}
	return f == other && stamp.Equal(otherStamp)
}

// Only the journal-backed native lifecycle can produce this observation.
// UUIDs alone, a successful generic Destroy, or an empty inventory cannot.
type ManagedPostgresDrainProof struct {
	DrainID, NativeGeneration, KernelBootID string
	ProcessesExited, ResourcesRemoved       bool
}

func (p ManagedPostgresDrainProof) ValidFor(f ManagedPostgresDrainFrame) bool {
	if !f.Valid() || p.DrainID != f.ID || !p.ProcessesExited || !p.ResourcesRemoved {
		return false
	}
	for _, value := range []string{p.NativeGeneration, p.KernelBootID} {
		u, err := uuid.Parse(value)
		if err != nil || u == uuid.Nil || u.String() != value {
			return false
		}
	}
	return true
}

type ManagedPostgresDrain struct {
	Frame     ManagedPostgresDrainFrame
	Proof     *ManagedPostgresDrainProof
	RetiredAt *time.Time
}

// schedd alone reserves work and commits instance retirement. Callers must
// explicitly request a member; discovery never installs an admission fence.
type ManagedPostgresDrainStore interface {
	ReserveManagedPostgresDrain(context.Context, string, string, string) (ManagedPostgresDrain, error)
	ManagedPostgresDrain(context.Context, string) (ManagedPostgresDrain, error)
	CompleteManagedPostgresDrain(context.Context, ManagedPostgresDrainFrame, ManagedPostgresDrainProof) error
	ListPendingManagedPostgresDrains(context.Context, string, string, int) ([]ManagedPostgresDrain, error)
}

// vmmd holds authoritative app, cutover, member and instance locks throughout
// retirement. Cancellation, migration or owner replacement cannot race a stop.
// A DB-less vmmd cannot implement this authority.
type ManagedPostgresDrainAuthority interface {
	WithManagedPostgresDrainAuthority(context.Context, ManagedPostgresDrainFrame,
		func(context.Context) (ManagedPostgresDrainProof, error)) (ManagedPostgresDrainProof, error)
}
