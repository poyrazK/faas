//go:build !no_pg

package state

// adr: 642
import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/runtimequota"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func TestPGRegionalRuntimeReconciliationSQLGuardAndDowngrade(t *testing.T) {
	f := pgRegionalCapacityFixture(t)
	s := f.ledger.(*PgStore)
	p := f.permit(runtimequota.CapacityWarm)
	f.reserve(t, p)
	f.issue(t, p)
	cellFence := uuid.NewString()
	if _, err := f.ledger.BeginRegionalRuntimeRetirement(t.Context(), p, cellFence); err != nil {
		t.Fatal(err)
	}
	stop := runtimequota.CapacityStop{Permit: p, FenceID: cellFence, AdmissionFrozen: true}
	e, err := f.cell.SignCapacityStop(stop)
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	for _, fence := range []any{nil, "", cellFence, uuid.Nil.String(), "invalid", "ABCDEFAB-1234-4234-8234-ABCDEFABCDEF", 1} {
		claims, err := json.Marshal(stop)
		if err != nil {
			t.Fatal(err)
		}
		var object map[string]any
		if err = json.Unmarshal(claims, &object); err != nil {
			t.Fatal(err)
		}
		object["HostFenceID"] = fence
		claims, err = json.Marshal(object)
		if err != nil {
			t.Fatal(err)
		}
		_, err = (&sqlc.Queries{}).StopRegionalRuntimeCapacity(t.Context(), s.pool, sqlc.StopRegionalRuntimeCapacityParams{
			AppID: uuidToPgtype(f.app()), PermitID: uuidToPgtype(p.ID), FenceID: uuidToPgtype(cellFence), StopClaims: claims, StopEnvelope: envelope,
		})
		var pgerr *pgconn.PgError
		if !errors.As(err, &pgerr) || pgerr.ConstraintName != "regional_capacity_host_fence_shape" {
			t.Fatalf("malformed host fence %v bypassed database guard: %v", fence, err)
		}
	}
	f.usage(t, 1, 1, 0)
	stop.FenceID = uuid.NewString()
	if _, err = f.authority.CompleteHostRetirementReconciliation(t.Context(), cellFence, stop, f.cell); err != nil {
		t.Fatal(err)
	}
	f.usage(t, 0, 0, 0)
	migration, err := migrations.FS.ReadFile("20261006085743443_regional_runtime_stop_fence_relation.sql")
	if err != nil {
		t.Fatal(err)
	}
	_, down, found := strings.Cut(string(migration), "-- +goose Down")
	if !found {
		t.Fatal("missing downgrade guard")
	}
	tx, err := s.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(t.Context()) }()
	if _, err = tx.Exec(t.Context(), down); err == nil {
		t.Fatal("downgrade discarded retained fence relation")
	}
}

func TestPGRegionalRuntimeReconciliationEmptyMigrationRoundTrip(t *testing.T) {
	s, _, _, _ := releasePGFixture(t)
	migration, err := migrations.FS.ReadFile("20261006085743443_regional_runtime_stop_fence_relation.sql")
	if err != nil {
		t.Fatal(err)
	}
	up, down, found := strings.Cut(string(migration), "-- +goose Down")
	if !found {
		t.Fatal("missing fence relation downgrade")
	}
	for _, statement := range []string{down, up} {
		if _, err := s.pool.Exec(t.Context(), statement); err != nil {
			t.Fatal(err)
		}
	}
}
