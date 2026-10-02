package db

// adr: 431. Reviewed ledger recovery does not recreate customer or runtime history.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/onebox-faas/faas/migrations"
)

var (
	ErrMigrationRecoveryHistory      = errors.New("ledger recovery refuses missing legacy, rolled-back or unknown migration history")
	ErrMigrationRecoverySchema       = errors.New("ledger recovery requires the complete canonical expanded schema and approved database writers")
	ErrMigrationRecoveryBackfill     = errors.New("ledger recovery backfill postconditions are incomplete")
	ErrMigrationRecoveryStale        = errors.New("ledger recovery approval no longer matches the target, schema, sources or ledger")
	ErrMigrationRecoverySource       = errors.New("canonical schema is not bound to this binary's embedded migration set; regenerate schema-dump")
	ErrMigrationRecoveryNoCandidates = errors.New("no eligible missing standards ledger entries")
	ErrMigrationRecoveryTransport    = errors.New("ledger recovery requires PostgreSQL 16 through a local Unix socket")
)

type MigrationLedgerRecoveryPlan struct {
	Format           string                                         `json:"format"`
	TargetHash       string                                         `json:"target_hash"`
	SchemaHash       string                                         `json:"schema_hash"`
	SourceHash       string                                         `json:"source_hash"`
	LedgerHash       string                                         `json:"ledger_hash"`
	ApplicationCount int64                                          `json:"application_count"`
	Repair           []migrations.ApplicationStandardRecoverySource `json:"repair"`
	Remaining        []migrations.Source                            `json:"remaining"`
	ApprovalHash     string                                         `json:"approval_hash,omitempty"`
}

type MigrationLedgerRecoveryReceipt struct {
	ApprovalHash     string    `json:"approval_hash"`
	TargetHash       string    `json:"target_hash"`
	Actor            string    `json:"actor"`
	RepairedVersions []int64   `json:"repaired_versions"`
	RecoveredAt      time.Time `json:"recovered_at"`
}

func migrationRecoveryDigest(value any) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:]), nil
}

func migrationRecoveryHash(raw []byte) string {
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}
