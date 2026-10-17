// adr:1661 — durable, manual production cutover plans.
package state

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type MigrationCutoverRecord struct {
	AccountID       string
	AppID           string
	Version         int64
	BindingRevision string
	LeaseUntil      time.Time
	NextAttemptAt   time.Time
	Report          api.MigrationCutover
}

type MigrationCutoverStore interface {
	CreateMigrationCutover(context.Context, MigrationCutoverRecord) (MigrationCutoverRecord, error)
	GetMigrationCutover(context.Context, string, string, string) (MigrationCutoverRecord, error)
	ListMigrationCutovers(context.Context, string, string) ([]MigrationCutoverRecord, error)
	SaveMigrationCutover(context.Context, MigrationCutoverRecord, int64) (MigrationCutoverRecord, error)
	ListDueMigrationCutovers(context.Context, time.Time, int32) ([]MigrationCutoverRecord, error)
}

func MigrationDomainRevision(d CustomDomain) string {
	sum := sha256.Sum256([]byte(d.AppID + "\x00" + d.EnvironmentID + "\x00" + d.Domain + "\x00" + d.ChallengeToken))
	return hex.EncodeToString(sum[:])
}

func migrationCutoverDue(r MigrationCutoverRecord) time.Time {
	if r.Report.Status != "checking" && r.Report.Status != "ready" {
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

func cloneMigrationCutover(r MigrationCutoverRecord) MigrationCutoverRecord {
	raw, _ := json.Marshal(r)
	var out MigrationCutoverRecord
	_ = json.Unmarshal(raw, &out)
	return out
}
