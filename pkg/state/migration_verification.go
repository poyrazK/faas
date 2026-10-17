// adr:1572 — resumable migration evidence over existing runtime owners.
package state

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// MigrationVerificationRecord keeps orchestration leases private. No secret
// values, gateway challenges, response bodies or connection URLs are stored.
type MigrationVerificationRecord struct {
	AccountID       string
	AppID           string
	Version         int64
	LeaseUntil      time.Time
	LeaseID         string
	NextAttemptAt   time.Time
	RestoreRequired bool
	StopStatus      string
	StopReason      string
	Failures        int
	Report          api.MigrationVerification
}

type MigrationVerificationStore interface {
	CreateMigrationVerification(context.Context, MigrationVerificationRecord) (MigrationVerificationRecord, error)
	GetMigrationVerification(context.Context, string, string, string) (MigrationVerificationRecord, error)
	ListMigrationVerifications(context.Context, string, string) ([]MigrationVerificationRecord, error)
	// Save atomically journals an optional dependency task with its receipt.
	// An existing task identity is never replaced on a replay.
	SaveMigrationVerification(context.Context, MigrationVerificationRecord, int64, *CreateAppTaskParams) (MigrationVerificationRecord, error)
}

// MigrationVerificationWorkerStore discovers work across accounts. The normal
// versioned lease still claims each step; listing never authorizes side effects.
type MigrationVerificationWorkerStore interface {
	ListDueMigrationVerifications(context.Context, time.Time, int32) ([]MigrationVerificationRecord, error)
}

func migrationVerificationDue(r MigrationVerificationRecord) time.Time {
	if r.Report.Status != "pending" {
		return time.Time{}
	}
	due := r.NextAttemptAt
	if due.IsZero() {
		due = r.Report.CreatedAt
	}
	if r.LeaseUntil.After(due) {
		due = r.LeaseUntil
	}
	return due
}

func MigrationConfigurationRevision(dep Deployment, changed time.Time) string {
	raw, _ := json.Marshal(struct {
		Rootfs, Image string
		Override      json.RawMessage
		Changed       time.Time
	}{dep.RootfsKey, dep.ImageDigest, dep.OverrideEnv, changed.UTC()})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func cloneMigrationVerification(r MigrationVerificationRecord) MigrationVerificationRecord {
	raw, _ := json.Marshal(r)
	var out MigrationVerificationRecord
	_ = json.Unmarshal(raw, &out)
	return out
}

func migrationTaskSlot(r *MigrationVerificationRecord) *string {
	if r.Report.Phase == "cold_dependencies" {
		return &r.Report.ColdDependencyTaskID
	}
	return &r.Report.DependencyTaskID
}
