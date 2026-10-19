package datapipeline

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"testing"
	"time"
)

func TestPipelineHealthRequiresAppliedEvidence(t *testing.T) {
	now := time.Now().UTC()
	for _, tc := range []struct {
		name, state, desired, reason, applied string
		fresh                                 bool
		want, action                          string
		confirm                               bool
	}{
		{"receiver alone", "live", "running", "", "", true, "apply_unconfirmed", "", false},
		{"applied checkpoint", "live", "running", "", now.Add(-time.Second).Format(time.RFC3339Nano), true, "applied_progress_confirmed", "", false},
		{"expired proof", "live", "running", "", now.Add(-api.DataPipelineObservationMaxAge).Format(time.RFC3339Nano), true, "apply_unconfirmed", "", false},
		{"worker down", "live", "running", "", now.Format(time.RFC3339Nano), false, "monitoring_stale", "", false},
		{"future clock", "live", "running", "", now.Add(time.Second).Format(time.RFC3339Nano), true, "apply_unconfirmed", "", false},
		{"schema drift", "resync_required", "running", "source_schema_changed", "", true, "source_schema_changed", "resync", true},
		{"live mismatch", "blocked", "running", "live_table_mismatch", "", true, "live_table_mismatch", "", true},
		{"credential", "blocked", "running", "credential_unavailable", "", true, "credential_unavailable", "rotate_credentials", false},
		{"pause", "paused", "paused", "", "", true, "paused", "resume", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := pipelineHealth(api.DataPipeline{State: tc.state, DesiredState: tc.desired, Reason: tc.reason, AppliedThroughAt: tc.applied, ObservationFresh: tc.fresh}, now)
			if h.Code != tc.want || h.Action != tc.action || h.RequiresConfirmation != tc.confirm {
				t.Fatalf("health: %+v", h)
			}
		})
	}
}

func TestPipelineAppliedEvidenceFencedAndResetOnResync(t *testing.T) {
	s, store, _, account := testService(t)
	ctx := context.Background()
	c, err := s.CreateConnection(ctx, account, api.CreateDataConnectionRequest{Name: "fresh-source", ConnectionURL: "postgres://r:p@db.example/db?sslmode=verify-full"})
	if err != nil {
		t.Fatal(err)
	}
	fixturePassingCheck(t, store, account, c)
	p, err := s.Create(ctx, account, api.CreateDataPipelineRequest{Name: "fresh-orders", SourceConnectionID: c.ID, DatabaseID: uuid.NewString(), Tables: []string{"orders"}, ReadSubjects: []string{"alice"}})
	if err != nil {
		t.Fatal(err)
	}
	claim, err := store.Claim(ctx, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	applied := time.Now().UTC().Truncate(time.Microsecond)
	if err := store.Finish(ctx, claim, Observation{State: "live", AppliedThroughAt: applied}); err != nil {
		t.Fatal(err)
	}
	saved, err := store.Get(ctx, account, p.ID)
	if err != nil || saved.AppliedThroughAt != applied.Format(time.RFC3339Nano) {
		t.Fatalf("missing applied proof: %+v %v", saved, err)
	}
	resync, err := store.Control(ctx, account, p.ID, p.Revision, "resync")
	if err != nil || resync.AppliedThroughAt != "" {
		t.Fatalf("resync retained old generation evidence: %+v %v", resync, err)
	}
	if err := store.Finish(ctx, claim, Observation{State: "live", AppliedThroughAt: applied}); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale finish: %v", err)
	}
	saved, err = store.Get(ctx, account, p.ID)
	if err != nil || saved.AppliedThroughAt != "" {
		t.Fatal("stale worker restored old proof")
	}
}

func TestPipelineRetentionPressureGuidance(t *testing.T) {
	retained := int64(api.DataPipelineRetentionWarningBytes)
	h := pipelineHealth(api.DataPipeline{State: "live", DesiredState: "running", ObservationFresh: true, RetainedWALBytes: &retained, AppliedThroughAt: time.Now().UTC().Format(time.RFC3339Nano)}, time.Now())
	if h.Code != "retention_pressure" || h.Action != "" || h.RequiresConfirmation {
		t.Fatalf("unsafe recovery: %+v", h)
	}
}
