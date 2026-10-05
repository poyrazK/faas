//go:build !no_pg && (linux || darwin)

package main

// adr: 632

import (
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/runtimequota"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func TestPGRegionalRuntimeSigningRequiresDatabaseCell(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	store := state.NewPgStore(pool)
	router := sched.NewVMMRouter(nil, nil, nil).WithRegionalRuntimePeerResolver(signingPeerResolver{})
	cellID := uuid.New()
	dir := filepath.Join(t.TempDir(), "cell")
	if _, err := runtimequota.ProvisionAuthorityKeys(dir, runtimequota.Cell, cellID.String()); err != nil {
		t.Fatal(err)
	}
	cfg := Config{RegionalRuntimeSigning: runtimequota.KeyConfig{PrivateKeyPath: filepath.Join(dir, runtimequota.PrivateKeyFilename), TrustBundlePath: filepath.Join(dir, runtimequota.TrustFilename)}}
	if _, err := loadRegionalRuntimeRetirement(t.Context(), cfg, pool, store, router, true); err == nil {
		t.Fatal("unbound database accepted a cell key")
	}
	if _, err := (&sqlc.Queries{}).InitializeControlPlaneCell(t.Context(), pool, sqlc.InitializeControlPlaneCellParams{CellID: pgtype.UUID{Bytes: cellID, Valid: true}, Region: "eu-fsn"}); err != nil {
		t.Fatal(err)
	}
	owner, err := loadRegionalRuntimeRetirement(t.Context(), cfg, pool, store, router, true)
	if err != nil || owner.Signer.IssuerID() != cellID.String() {
		t.Fatal("database-bound signer failed", err)
	}
	wrong := filepath.Join(t.TempDir(), "other-cell")
	if _, err := runtimequota.ProvisionAuthorityKeys(wrong, runtimequota.Cell, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	cfg.RegionalRuntimeSigning = runtimequota.KeyConfig{PrivateKeyPath: filepath.Join(wrong, runtimequota.PrivateKeyFilename), TrustBundlePath: filepath.Join(wrong, runtimequota.TrustFilename)}
	if _, err := loadRegionalRuntimeRetirement(t.Context(), cfg, pool, store, router, true); err == nil {
		t.Fatal("another cell's trusted key replaced the database identity")
	}
}
