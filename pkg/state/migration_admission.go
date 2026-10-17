package state

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"math"
	"time"

	"github.com/google/uuid"
)

var ErrMigrationAdoptionUnresolved = errors.New("migration adoption requires reconciliation")

const (
	MigrationReserved  = "reserved"
	MigrationAdopting  = "adopting"
	MigrationAdopted   = "adopted"
	MigrationCleaning  = "cleaning"
	MigrationCleaned   = "cleaned"
	MigrationCommitted = "committed"
)

// MigrationAdmissionPlan is immutable authority for one destination dispatch.
// RAMMB includes host overhead and sidecars; CPU includes startup and sidecars.
type MigrationAdmissionPlan struct {
	ID, InstanceID, SourceNodeID, DestinationNodeID string
	SourceWakeID, DestinationWakeID, LeaseToken     string
	MemStorageKey, VMStateStorageKey, RequestDigest string
	DestinationGeneration                           int64
	RAMMB, CPUMillicores, VCPU                      int
	ExpiresAt                                       time.Time
}

type MigrationAdmission struct {
	MigrationAdmissionPlan
	Phase           string
	AdoptionReceipt []byte
	CleanupProof    string
}

func (a MigrationAdmission) Pending() bool {
	return a.Phase != MigrationCleaned && a.Phase != MigrationCommitted
}

// MigrationAdmissionStore preserves inbound capacity until ownership or physical
// cleanup is proven. Expiry never releases a started adoption.
type MigrationAdmissionStore interface {
	ReserveMigrationAdmission(context.Context, MigrationAdmissionPlan) (MigrationAdmission, error)
	MigrationAdmissionByID(context.Context, string) (MigrationAdmission, error)
	StartMigrationAdoption(context.Context, string, string, string) (MigrationAdmission, bool, error)
	AcknowledgeMigrationAdoption(context.Context, string, string, string, []byte) error
	RequestMigrationAdmissionCleanup(context.Context, string, string) (MigrationAdmission, error)
	AcknowledgeMigrationAdmissionCleanup(context.Context, string, string, string) error
	ListMigrationAdmissionsForRecovery(context.Context, string, time.Time, string, int) ([]MigrationAdmission, error)
	FenceMigrationSourceResume(context.Context, string, string) (bool, error)
}

type migrationAdmissionKey struct{}

func WithMigrationAdmission(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, migrationAdmissionKey{}, id)
}

func migrationAdmissionID(ctx context.Context) string {
	id, _ := ctx.Value(migrationAdmissionKey{}).(string)
	return id
}

func validateMigrationAdmissionPlan(p MigrationAdmissionPlan) error {
	for _, id := range []string{p.ID, p.InstanceID, p.SourceNodeID, p.DestinationNodeID, p.SourceWakeID, p.DestinationWakeID} {
		parsed, err := uuid.Parse(id)
		if err != nil || parsed == uuid.Nil {
			return ErrInvalidArgument
		}
	}
	digest, err := hex.DecodeString(p.RequestDigest)
	if err != nil || len(digest) != 32 || hex.EncodeToString(digest) != p.RequestDigest || p.SourceNodeID == p.DestinationNodeID || p.SourceWakeID == p.DestinationWakeID || p.DestinationGeneration <= 0 || p.RAMMB <= 0 || p.RAMMB > math.MaxInt32 || p.CPUMillicores <= 0 || p.CPUMillicores > math.MaxInt32 || p.VCPU <= 0 || p.VCPU > math.MaxInt32 || p.LeaseToken == "" || p.MemStorageKey == "" || p.VMStateStorageKey == "" || p.ExpiresAt.IsZero() {
		return ErrInvalidArgument
	}
	return nil
}

func normalizeMigrationAdmissionPlan(p MigrationAdmissionPlan) MigrationAdmissionPlan {
	p.ExpiresAt = p.ExpiresAt.UTC().Truncate(time.Microsecond)
	return p
}

func cloneMigrationAdmission(a MigrationAdmission) MigrationAdmission {
	a.AdoptionReceipt = bytes.Clone(a.AdoptionReceipt)
	return a
}
