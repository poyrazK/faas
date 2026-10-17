package imaged

import (
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
	"testing"
	"time"
)

func TestPostCaptureUnknownPostgres(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	s := state.NewPgStore(pool)
	ctx := t.Context()
	account, err := s.CreateAccount(ctx, "post-unknown@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	_, d := postCaptureCandidate(t, s, account.ID, "unknown")
	published := postCapturePublish(t, s, d, state.SnapshotTierInit)
	for _, raw := range []string{`{"current":"snapshot_prepare"}`, `{"current":"snapshot_prepare","current_started_at":"yesterday"}`, `{"current":"snapshot_prepare","current_started_at":"2026-02-30T00:00:00Z"}`, `[]`, `{"current":null,"current_started_at":"2026-01-01T00:00:00Z"}`, `{"current":"snapshot_prepare","current_started_at":"` + time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano) + `"}`} {
		if _, err := pool.Exec(ctx, "UPDATE deployments SET stage_state=$2::jsonb WHERE id=$1", d.ID, raw); err != nil {
			t.Fatal(err)
		}
		got, err := s.PostCaptureSnapshot(ctx)
		if err != nil || got[state.PostCaptureUnknown].Pending != 1 || got[state.PostCaptureUnknown].UnknownAge != 1 {
			t.Fatalf("raw=%s result=%+v %v", raw, got, err)
		}
	}
	for _, raw := range []string{`{}`, `[]`, `{"started_at":"yesterday"}`} {
		if _, err := pool.Exec(ctx, "UPDATE deployments SET stage_state=jsonb_set($2::jsonb,'{hosting_verification}',$3::jsonb) WHERE id=$1", d.ID, d.StageState, raw); err != nil {
			t.Fatal(err)
		}
		got, err := s.PostCaptureSnapshot(ctx)

		if err != nil || got[state.PostCaptureUnknown].Pending != 1 {
			t.Fatalf("raw=%s result=%+v %v", raw, got, err)
		}
	}
	if _, err := pool.Exec(ctx, "UPDATE deployments SET stage_state=$2::jsonb WHERE id=$1", d.ID, d.StageState); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkSnapshotStale(ctx, published.ID); err != nil {
		t.Fatal(err)
	}
	node, err := s.ComputeNodeByName(ctx, state.DefaultLocalNodeName)
	if err != nil {
		t.Fatal(err)
	}
	wake, err := uuid.NewV7()
	if err != nil {
		t.Fatal(err)
	}
	instance, err := s.CreateInstance(ctx, d.AppID, d.ID, string(state.StateParked), 256, node.ID, wake.String())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "UPDATE instances SET started_at=NULL WHERE id=$1", instance.ID); err != nil {
		t.Fatal(err)
	}
	missing, err := s.PostCaptureSnapshot(ctx)
	if err != nil || missing[state.PostCaptureUnknown].Pending != 1 {
		t.Fatalf("missing start fabricated empty: %+v %v", missing, err)
	}
	if _, err := pool.Exec(ctx, "UPDATE instances SET started_at=clock_timestamp(),parked_at=NULL WHERE id=$1", instance.ID); err != nil {
		t.Fatal(err)
	}
	missing, err = s.PostCaptureSnapshot(ctx)
	if err != nil || missing[state.PostCapturePublicationPending].Pending != 1 || missing[state.PostCapturePublicationPending].UnknownAge != 1 {
		t.Fatalf("missing parked time fabricated age: %+v %v", missing, err)
	}

}
