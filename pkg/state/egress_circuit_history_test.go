// adr: 375
package state_test

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func TestPgStoreEgressCircuitReadsDistinctHistoryAndUnmeasuredUpstream(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	s := state.NewPgStore(pool)
	appID := seedAppForAllowlist(t, t.Context(), s, "circuit-history")
	app, err := s.AppByID(t.Context(), appID)
	if err != nil {
		t.Fatal(err)
	}
	uuidParam := func(id string) pgtype.UUID { return pgtype.UUID{Bytes: uuid.MustParse(id), Valid: true} }
	measuredHash := strings.Repeat("a", 64)
	for _, item := range []struct{ host, hash string }{{"measured.example.com", measuredHash}, {"unmeasured.example.com", strings.Repeat("b", 64)}} {
		id, err := s.InsertDataUpstream(t.Context(), sqlc.InsertDataUpstreamParams{
			ID: uuidParam(uuid.NewString()), AppID: uuidParam(appID), AccountID: uuidParam(app.AccountID),
			Source: "explicit", Scope: "default", DeploymentScope: "default", Kind: "postgres", Host: item.host,
			Port: 5432, HostRedactedHash: item.hash,
		})
		if err != nil {
			t.Fatal(err)
		}
		enabled := true
		if err := s.UpdateDataUpstreamCircuitBreaker(t.Context(), state.UpdateDataUpstreamCircuitBreakerParams{ID: id, AppID: uuid.MustParse(appID), Enabled: &enabled}); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now().UTC().Truncate(time.Second)
	for _, age := range []time.Duration{180, 90, 60, 30} {
		if err := s.InsertDataUpstreamProbe(t.Context(), sqlc.InsertDataUpstreamProbeParams{
			ID: uuidParam(uuid.NewString()), HostRedactedHash: measuredHash, Region: "region_a", Kind: "postgres",
			SampledAt:  pgtype.Timestamptz{Time: now.Add(-age * time.Second), Valid: true},
			ErrorClass: pgtype.Text{String: "timeout", Valid: true},
		}); err != nil {
			t.Fatal(err)
		}
	}
	// A second region at the same generation must not fabricate a fourth
	// observation. A mixed verdict is conservative for the global circuit.
	if err := s.InsertDataUpstreamProbe(t.Context(), sqlc.InsertDataUpstreamProbeParams{
		ID: uuidParam(uuid.NewString()), HostRedactedHash: measuredHash, Region: "region_b", Kind: "postgres",
		SampledAt: pgtype.Timestamptz{Time: now.Add(-30 * time.Second), Valid: true},
		Ok:        true, RttMs: pgtype.Int4{Int32: 5, Valid: true},
	}); err != nil {
		t.Fatal(err)
	}
	rows, err := s.ListEgressCircuitCandidates(t.Context(), now.Add(-120*time.Second))
	if err != nil || len(rows) != 4 {
		t.Fatalf("history rows=%d err=%v", len(rows), err)
	}
	for i, age := range []time.Duration{90, 60, 30} {
		if rows[i].HostRedactedHash != measuredHash || rows[i].OK || !rows[i].Sampled.Equal(now.Add(-age*time.Second)) {
			t.Fatalf("probe history changed: %+v", rows[i])
		}
	}
	if !rows[3].Sampled.IsZero() {
		t.Fatal("unmeasured dependency was manufactured into a failed sample")
	}
}
