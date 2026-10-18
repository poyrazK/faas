package managedpostgres

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/migrations"
)

type writerIsolationSagaProvider struct {
	*sourceFenceSagaProvider
	inventory       CheckpointWriterInventory
	receipt         *CheckpointWriterIsolationEvidence
	proofErr        error
	proofCalls      int
	started, resume chan struct{}
}

func (p *writerIsolationSagaProvider) withInventory(o CheckpointConnectionClosure, err error) (CheckpointConnectionClosure, error) {
	if err == nil {
		i := p.inventory
		o.WriterInventory = &i
		if i.CatalogComplete {
			o.UnselectedDatabases = int64(i.DatabaseCount) - int64(len(o.Databases)) - 1
		}
	}
	return o, err
}

func (p *writerIsolationSagaProvider) CloseCheckpointConnections(ctx context.Context, d RestoreSourceDefinition, m CheckpointMaintenance, r CheckpointConnectionRequest) (CheckpointConnectionClosure, error) {
	return p.withInventory(p.sourceFenceSagaProvider.CloseCheckpointConnections(ctx, d, m, r))
}

func (p *writerIsolationSagaProvider) ObserveCheckpointConnectionClosure(ctx context.Context, d RestoreSourceDefinition, m CheckpointMaintenance, r CheckpointConnectionRequest) (CheckpointConnectionClosure, error) {
	return p.withInventory(p.sourceFenceSagaProvider.ObserveCheckpointConnectionClosure(ctx, d, m, r))
}

func (p *writerIsolationSagaProvider) ObserveCheckpointWriterIsolation(ctx context.Context, r CheckpointWriterIsolationRequest) (CheckpointWriterIsolationEvidence, error) {
	p.proofCalls++
	if p.proofErr != nil {
		return CheckpointWriterIsolationEvidence{}, p.proofErr
	}
	if p.terminal != nil {
		return CheckpointWriterIsolationEvidence{}, ErrNotFound
	}
	if p.receipt == nil {
		now := time.Now().UTC().Truncate(time.Microsecond)
		p.receipt = &CheckpointWriterIsolationEvidence{Request: r, Scope: "all_dataset_endpoints", CatalogStable: true,
			BackgroundWritersFenced: true, ObservedAt: now, ExpiresAt: now.Add(2 * time.Minute)}
	}
	e := *p.receipt
	if p.started != nil {
		close(p.started)
		select {
		case <-ctx.Done():
			return CheckpointWriterIsolationEvidence{}, ctx.Err()
		case <-p.resume:
		}
	}
	return e, nil
}

func installWriterIsolationSagaProvider(service *CutoverService, base *sourceFenceSagaProvider, c Cutover) *writerIsolationSagaProvider {
	p := &writerIsolationSagaProvider{sourceFenceSagaProvider: base, inventory: CheckpointWriterInventory{
		CatalogComplete: true, DatabaseCount: 3, CatalogFingerprint: strings.Repeat("c", 64)}}
	b := service.bindings.registry.backends[c.Source.BackendID]
	b.Provider = p
	service.bindings.registry.backends[c.Source.BackendID] = b
	return p
}

func writerCatalogMigrationStatements(t *testing.T) (string, string) {
	t.Helper()
	raw, err := migrations.FS.ReadFile("20261016165131580_managed_postgres_writer_catalog_baselines.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(string(raw), "-- +goose Down")
	return parts[0], parts[1]
}

func closeWriterIsolationSaga(t *testing.T, service *CutoverService, s *PostgresStore, c Cutover, p *writerIsolationSagaProvider) CutoverSourceFence {
	t.Helper()
	prepareSourceFenceSaga(t, service, s, c)
	sourceFenceDue(t, s, c)
	if _, err := service.ReconcileSourceFence(t.Context(), c.AccountID, c.ID); err != nil {
		t.Fatal(err)
	}
	p.closed.Databases[0].Sessions, p.closed.Databases[1].PreparedTransactions = 0, 0
	p.closed.UnselectedDatabases, p.closed.Drained = 0, true
	sourceFenceDue(t, s, c)
	f, err := service.ReconcileSourceFence(t.Context(), c.AccountID, c.ID)
	if err != nil || f.CatalogFingerprint == "" || f.Observation.WriterIsolation == nil {
		t.Fatal("complete idle source did not retain provider evidence", err)
	}
	return f
}

func TestPostgresCutoverWriterIsolationRecoveryExpiryAndCatalogABA(t *testing.T) {
	service, s, _, c, base, enabled := sourceFenceSagaFixture(t)
	p := installWriterIsolationSagaProvider(service, base, c)
	f := closeWriterIsolationSaga(t, service, s, c, p)
	restarted, err := NewPostgresStore(s.pool)
	if err != nil {
		t.Fatal(err)
	}
	service.store = restarted
	loaded, err := restarted.GetCutoverSourceFence(t.Context(), c.AccountID, c.ID)
	if err != nil || loaded.CatalogFingerprint != f.CatalogFingerprint || loaded.Observation.WriterIsolation == nil {
		t.Fatal("restart lost the original guard authority", err)
	}
	preflight := completeRuntimePreflight(t, s, c)
	if preflight.SourceSessions.WriterIsolation == nil || !preflight.SourceSessions.WriterIsolation.Confirmed ||
		!preflight.ExpiresAt.Equal(p.receipt.ExpiresAt) || !slices.Contains(preflight.Blockers, "activation_disabled") ||
		!slices.Contains(preflight.Blockers, "source_sessions_unfenced") {
		t.Fatal("provider evidence escaped expiry or activation boundaries", preflight.SourceSessions)
	}
	sourceFenceDue(t, s, c)
	if _, err = service.ReconcileSourceFence(t.Context(), c.AccountID, c.ID); err != nil {
		t.Fatal(err)
	}
	unchanged, err := s.GetCutoverPreflight(t.Context(), c.AccountID, c.ID)
	if err != nil || unchanged.SourceSessionsChanged {
		t.Fatal("unchanged provider guard invalidated historical findings", err)
	}
	p.proofErr = ErrUnavailable
	sourceFenceDue(t, s, c)
	if _, err = service.ReconcileSourceFence(t.Context(), c.AccountID, c.ID); err != nil {
		t.Fatal("provider failure discarded confirmed SQL admission", err)
	}
	failed := completeRuntimePreflight(t, s, c)
	if !failed.SourceSessions.AdmissionClosed || failed.SourceSessions.WriterIsolation.Confirmed ||
		!slices.Contains(failed.SourceSessions.Blockers, "source_writer_isolation_unavailable") {
		t.Fatal("failed evidence refresh retained positive isolation", failed.SourceSessions)
	}
	p.proofErr = nil
	p.inventory.CatalogFingerprint = strings.Repeat("d", 64)
	sourceFenceDue(t, s, c)
	f, err = service.ReconcileSourceFence(t.Context(), c.AccountID, c.ID)
	if err != nil || f.CatalogFingerprint != loaded.CatalogFingerprint || f.CatalogInvalidatedAt.IsZero() {
		t.Fatal("catalog change adopted a new baseline", err)
	}
	up, down := writerCatalogMigrationStatements(t)
	if _, err = s.pool.Exec(t.Context(), down); err == nil {
		t.Fatal("downgrade discarded active catalog authority")
	}
	for _, statement := range []string{
		`UPDATE managed_postgres_cutover_source_fences SET catalog_fingerprint=repeat('e',64) WHERE cutover_id=$1`,
		`UPDATE managed_postgres_cutover_source_fences SET catalog_invalidated_at=NULL WHERE cutover_id=$1`,
	} {
		if _, err = s.pool.Exec(t.Context(), statement, c.ID); err == nil {
			t.Fatal("original baseline or drift history was mutable")
		}
	}
	// Model an older worker overwriting only the closure JSON. Dedicated catalog
	// columns retain the original fingerprint and permanent invalidation latch.
	if _, err = s.pool.Exec(t.Context(), `UPDATE managed_postgres_cutover_source_fences SET closure=closure-'writer_inventory'-'writer_isolation'-'writer_isolation_code' WHERE cutover_id=$1`, c.ID); err != nil {
		t.Fatal(err)
	}
	p.inventory.CatalogFingerprint = loaded.CatalogFingerprint
	sourceFenceDue(t, s, c)
	if _, err = service.ReconcileSourceFence(t.Context(), c.AccountID, c.ID); err != nil {
		t.Fatal(err)
	}
	aba := completeRuntimePreflight(t, s, c)
	if aba.SourceSessions.WriterIsolation.Confirmed || aba.SourceSessions.WriterIsolation.CatalogMatches ||
		!slices.Contains(aba.SourceSessions.Blockers, "source_writer_catalog_changed") {
		t.Fatal("catalog ABA or mixed-version write reused earlier isolation", aba.SourceSessions)
	}
	*enabled = false
	if _, err = s.CancelCutover(t.Context(), c.AccountID, c.ID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	f, err = service.ReconcileSourceFence(t.Context(), c.AccountID, c.ID)
	if err != nil || f.State != "released" {
		t.Fatal("invalidated catalog blocked authenticated cleanup", f.State, err)
	}
	for _, statement := range []string{down, up} {
		if _, err = s.pool.Exec(t.Context(), statement); err != nil {
			t.Fatal("completed catalog authority prevented migration round trip", err)
		}
	}
}

func TestPostgresCutoverWriterIsolationCancellationRejectsLateEvidence(t *testing.T) {
	service, s, _, c, base, _ := sourceFenceSagaFixture(t)
	p := installWriterIsolationSagaProvider(service, base, c)
	closeWriterIsolationSaga(t, service, s, c, p)
	p.started, p.resume = make(chan struct{}), make(chan struct{})
	sourceFenceDue(t, s, c)
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := service.ReconcileSourceFence(ctx, c.AccountID, c.ID)
		done <- err
	}()
	select {
	case <-ctx.Done():
		t.Fatal("provider evidence read did not start", ctx.Err())
	case <-p.started:
	}
	if _, err := s.CancelCutover(ctx, c.AccountID, c.ID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if f, err := service.ReconcileSourceFence(ctx, c.AccountID, c.ID); err != nil || f.State != "released" {
		t.Fatal("cancellation waited for a stale evidence worker", f.State, err)
	}
	close(p.resume)
	if err := <-done; !errors.Is(err, ErrConflict) {
		t.Fatal("late evidence retained its worker lease", err)
	}
	f, err := s.GetCutoverSourceFence(ctx, c.AccountID, c.ID)
	if err != nil || sourceSessionsView(f, time.Now().UTC()).WriterIsolation.Confirmed {
		t.Fatal("late evidence revived released isolation", err)
	}
	if _, err = p.CloseCheckpointConnections(ctx, f.definition(), f.Maintenance, f.request()); !errors.Is(err, ErrConflict) {
		t.Fatal("late close survived the provider tombstone", err)
	}
}
