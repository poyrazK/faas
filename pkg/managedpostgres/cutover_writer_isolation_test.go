package managedpostgres

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func writerIsolationFixture() (CutoverSourceFence, CheckpointWriterIsolationEvidence, time.Time) {
	now := time.Now().UTC().Truncate(time.Microsecond)
	f := CutoverSourceFence{OwnerToken: uuid.NewString(), State: "closed", CatalogFingerprint: strings.Repeat("a", 64), ObservedAt: now,
		Source:        Database{Spec: testSpec(), DesiredGeneration: 1, BackendID: "primary-a", BackendFingerprint: strings.Repeat("b", 64), ProviderResourceID: "private-project", DataResourceID: "private-dataset"},
		Maintenance:   CheckpointMaintenance{OwnerToken: uuid.NewString(), SourceResourceID: "private-dataset", State: "ready", OwnerOID: 10, DatabaseOID: 20},
		DatabaseNames: []string{"private_a", "private_b"}, MaintenanceStage: "ready",
		Observation: &sourceFenceObservation{ClosedAt: now.Add(-time.Second), Drained: true,
			Databases:       []CheckpointConnectionDatabase{{Name: "private_a", OID: 1, OwnerOID: 10}, {Name: "private_b", OID: 2, OwnerOID: 10}},
			WriterInventory: &CheckpointWriterInventory{CatalogComplete: true, DatabaseCount: 3, CatalogFingerprint: strings.Repeat("a", 64)}}}
	e := CheckpointWriterIsolationEvidence{Request: f.writerIsolationRequest(), Scope: "all_dataset_endpoints", CatalogStable: true,
		BackgroundWritersFenced: true, ObservedAt: now, ExpiresAt: now.Add(time.Minute)}
	retained := sourceWriterIsolationEvidence(e)
	f.Observation.WriterIsolation = &retained
	return f, e, now
}

func TestWriterIsolationEvidenceRequiresExactAuthorityAndBoundedFreshness(t *testing.T) {
	f, e, now := writerIsolationFixture()
	if err := e.Validate(f.writerIsolationRequest(), now); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		change func(*CheckpointWriterIsolationEvidence)
	}{
		{"owner", func(e *CheckpointWriterIsolationEvidence) { e.Request.Identity.OwnerToken = uuid.NewString() }},
		{"generation", func(e *CheckpointWriterIsolationEvidence) { e.Request.Generation++ }},
		{"dataset", func(e *CheckpointWriterIsolationEvidence) { e.Request.Definition.DataResourceID = "other" }},
		{"backend", func(e *CheckpointWriterIsolationEvidence) {
			e.Request.Definition.BackendFingerprint = strings.Repeat("c", 64)
		}},
		{"catalog", func(e *CheckpointWriterIsolationEvidence) { e.Request.CatalogFingerprint = strings.Repeat("c", 64) }},
		{"maintenance", func(e *CheckpointWriterIsolationEvidence) { e.Request.Maintenance.DatabaseOID++ }},
		{"close_time", func(e *CheckpointWriterIsolationEvidence) {
			e.Request.ClosedAt = e.Request.ClosedAt.Add(time.Microsecond)
		}},
		{"scope", func(e *CheckpointWriterIsolationEvidence) { e.Scope = "selected_databases" }},
		{"future", func(e *CheckpointWriterIsolationEvidence) { e.ObservedAt = now.Add(time.Second) }},
		{"expired", func(e *CheckpointWriterIsolationEvidence) { e.ObservedAt, e.ExpiresAt = now.Add(-time.Second), now }},
		{"ttl", func(e *CheckpointWriterIsolationEvidence) {
			e.ExpiresAt = now.Add(CutoverVerificationMaxAge + time.Microsecond)
		}},
		{"unfenced", func(e *CheckpointWriterIsolationEvidence) { e.BackgroundWritersFenced = false }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			changed := e
			tc.change(&changed)
			if changed.Validate(f.writerIsolationRequest(), now) == nil {
				t.Fatal("mismatched or stale provider evidence accepted")
			}
		})
	}
}

func TestWriterIsolationFindingsNeverPromoteUnprovenWritersOrCatalogABA(t *testing.T) {
	f, _, now := writerIsolationFixture()
	view := sourceSessionsView(f, now)
	if !view.WriterIsolation.Confirmed || slices.Contains(view.Blockers, "background_writers_unproven") || slices.Contains(view.Blockers, "catalog_stability_unproven") {
		t.Fatal(view.WriterIsolation, view.Blockers)
	}
	for _, tc := range []struct {
		name   string
		change func(*CutoverSourceFence)
	}{
		{"catalog_drift", func(f *CutoverSourceFence) {
			f.Observation.WriterInventory.CatalogFingerprint = strings.Repeat("b", 64)
		}},
		{"catalog_aba", func(f *CutoverSourceFence) { f.CatalogInvalidatedAt = now }},
		{"clients", func(f *CutoverSourceFence) { f.Observation.WriterInventory.ClientSessions = 1 }},
		{"background", func(f *CutoverSourceFence) { f.Observation.WriterInventory.BackgroundWorkers = 1 }},
		{"replication", func(f *CutoverSourceFence) { f.Observation.WriterInventory.ReplicationWorkers = 1 }},
		{"visibility", func(f *CutoverSourceFence) { f.Observation.WriterInventory.UnknownBackends = 1 }},
		{"partial_inventory", func(f *CutoverSourceFence) { f.Observation.WriterInventory = nil }},
		{"failed_sql", func(f *CutoverSourceFence) { f.LastErrorCode = "source_fence_observation_failed" }},
		{"failed_provider", func(f *CutoverSourceFence) { f.Observation.WriterIsolationCode = "source_writer_isolation_unavailable" }},
		{"unsupported", func(f *CutoverSourceFence) { f.Observation.WriterIsolationCode = "source_writer_isolation_unsupported" }},
		{"released", func(f *CutoverSourceFence) { f.State = "released" }},
		{"expired_sql", func(f *CutoverSourceFence) { f.ObservedAt = now.Add(-CutoverVerificationMaxAge) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			copyFence := f
			observation, inventory := *f.Observation, *f.Observation.WriterInventory
			observation.WriterInventory = &inventory
			copyFence.Observation = &observation
			tc.change(&copyFence)
			v := sourceSessionsView(copyFence, now)
			if v.WriterIsolation.Confirmed || v.WriterIsolation.CatalogStable || v.WriterIsolation.BackgroundWritersFenced ||
				!slices.Contains(v.Blockers, "background_writers_unproven") || !slices.Contains(v.Blockers, "catalog_stability_unproven") {
				t.Fatal(v.WriterIsolation, v.Blockers)
			}
		})
	}
	view = sourceSessionsView(f, now.Add(time.Minute))
	if view.WriterIsolation.Confirmed || view.WriterIsolation.State != "expired" {
		t.Fatal("expired provider evidence remained positive", view.WriterIsolation)
	}
	raw, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{f.OwnerToken, f.CatalogFingerprint, f.Maintenance.OwnerToken, f.Source.ProviderResourceID, "private_a", "private-dataset"} {
		if strings.Contains(string(raw), private) {
			t.Fatal("writer authority leaked through preflight", private)
		}
	}
}

type writerIsolationTestProvider struct {
	bindingProvider
	evidence CheckpointWriterIsolationEvidence
	err      error
	calls    int
}

func (p *writerIsolationTestProvider) ObserveCheckpointWriterIsolation(context.Context, CheckpointWriterIsolationRequest) (CheckpointWriterIsolationEvidence, error) {
	p.calls++
	return p.evidence, p.err
}

func TestWriterIsolationServiceRejectsUnsupportedAndInvalidReceipts(t *testing.T) {
	f, evidence, now := writerIsolationFixture()
	p := &writerIsolationTestProvider{evidence: evidence}
	p.capabilities = testCapabilities()
	registry := &Registry{backends: map[string]Backend{f.Source.BackendID: {ID: f.Source.BackendID, Fingerprint: f.Source.BackendFingerprint,
		Region: f.Source.Spec.Region, Capabilities: p.Capabilities(), Provider: p}}}
	s, err := NewService(registry, NewMemoryStore(), ServiceOptions{Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ObserveCheckpointWriterIsolation(t.Context(), f.writerIsolationRequest()); err != nil {
		t.Fatal(err)
	}
	p.evidence.Request.Generation++
	if _, err = s.ObserveCheckpointWriterIsolation(t.Context(), f.writerIsolationRequest()); !errors.Is(err, ErrConflict) {
		t.Fatal("foreign receipt accepted", err)
	}
	p.err = errors.New("private provider response")
	if _, err = s.ObserveCheckpointWriterIsolation(t.Context(), f.writerIsolationRequest()); !errors.Is(err, ErrUnavailable) || strings.Contains(err.Error(), "private") {
		t.Fatal("untrusted provider error escaped", err)
	}
	b := registry.backends[f.Source.BackendID]
	b.Provider = &bindingProvider{}
	registry.backends[f.Source.BackendID] = b
	if _, err = s.ObserveCheckpointWriterIsolation(t.Context(), f.writerIsolationRequest()); !errors.Is(err, ErrUnsupported) {
		t.Fatal("ordinary provider manufactured isolation", err)
	}
}
