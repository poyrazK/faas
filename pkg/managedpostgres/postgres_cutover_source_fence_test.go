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
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/state"
)

// The provider independently validates frozen placement, maintenance and fence
// ownership. Lost replies occur after its durable remote state has changed.
type sourceFenceSagaProvider struct {
	bindingProvider
	maintenance                           CheckpointMaintenance
	definition                            RestoreSourceDefinition
	closed                                *CheckpointConnectionClosure
	terminal                              *CheckpointConnectionTerminal
	closeLost, abandonLost, observeFailed bool
	closeCalls, abandonCalls              int
}

type sourceFenceFailReceiptStore struct {
	CutoverStore
	CutoverSourceFenceStore
}

func (s sourceFenceFailReceiptStore) SaveCutoverSourceFence(ctx context.Context, before, next CutoverSourceFence, keepLease bool, now, retry time.Time) (CutoverSourceFence, error) {
	if next.State == "closed" {
		return before, ErrUnavailable
	}
	return s.CutoverSourceFenceStore.SaveCutoverSourceFence(ctx, before, next, keepLease, now, retry)
}

func (p *sourceFenceSagaProvider) ReconcileCheckpointMaintenance(_ context.Context, d RestoreSourceDefinition, r CheckpointMaintenanceRequest) (CheckpointMaintenance, error) {
	if p.maintenance.OwnerToken == "" {
		p.definition = d
		p.maintenance = CheckpointMaintenance{OwnerToken: r.OwnerToken, SourceResourceID: r.SourceResourceID, OwnerOID: 501, State: "reserved"}
	}
	if d != p.definition || r.OwnerToken != p.maintenance.OwnerToken || r.SourceResourceID != d.DataResourceID {
		return CheckpointMaintenance{}, ErrConflict
	}
	out := p.maintenance
	switch r.Phase {
	case "role":
		out.DatabaseOID = 0
	case "database":
		out.DatabaseOID = 502
	case "activation", "ready":
		out.DatabaseOID, out.State = 502, "ready"
	}
	p.maintenance = out
	return out, nil
}
func (p *sourceFenceSagaProvider) DiscoverCheckpointConnections(_ context.Context, d RestoreSourceDefinition, m CheckpointMaintenance, i CheckpointConnectionIdentity) (CheckpointConnectionRequest, error) {
	if d != p.definition || m != p.maintenance || m.State != "ready" || i.SourceResourceID != d.DataResourceID {
		return CheckpointConnectionRequest{}, ErrConflict
	}
	return CheckpointConnectionRequest{CheckpointConnectionIdentity: i, DatabaseNames: []string{"private_alpha", "private_beta"}}, nil
}
func (p *sourceFenceSagaProvider) CloseCheckpointConnections(_ context.Context, d RestoreSourceDefinition, m CheckpointMaintenance, r CheckpointConnectionRequest) (CheckpointConnectionClosure, error) {
	p.closeCalls++
	if d != p.definition || m != p.maintenance || p.terminal != nil || r.SourceResourceID != d.DataResourceID {
		return CheckpointConnectionClosure{}, ErrConflict
	}
	if p.closed == nil {
		p.closed = &CheckpointConnectionClosure{CheckpointConnectionIdentity: r.CheckpointConnectionIdentity, State: "closed",
			ClosedAt: time.Now().UTC().Truncate(time.Microsecond), UnselectedDatabases: 1,
			Databases: []CheckpointConnectionDatabase{{Name: r.DatabaseNames[0], OID: 100, OwnerOID: 200, OriginalAllowConnections: true, Sessions: 3},
				{Name: r.DatabaseNames[1], OID: 101, OwnerOID: 200, OriginalAllowConnections: false, PreparedTransactions: 1}}}
	}
	if p.closed.CheckpointConnectionIdentity != r.CheckpointConnectionIdentity {
		return CheckpointConnectionClosure{}, ErrConflict
	}
	if p.closeLost {
		p.closeLost = false
		return CheckpointConnectionClosure{}, errors.New("private provider message must not escape")
	}
	return *p.closed, nil
}
func (p *sourceFenceSagaProvider) ObserveCheckpointConnectionClosure(_ context.Context, d RestoreSourceDefinition, m CheckpointMaintenance, r CheckpointConnectionRequest) (CheckpointConnectionClosure, error) {
	if p.observeFailed {
		return CheckpointConnectionClosure{}, ErrUnavailable
	}
	if p.closed == nil {
		return CheckpointConnectionClosure{}, ErrNotFound
	}
	if d != p.definition || m != p.maintenance || r.CheckpointConnectionIdentity != p.closed.CheckpointConnectionIdentity || p.terminal != nil {
		return CheckpointConnectionClosure{}, ErrConflict
	}
	return *p.closed, nil
}
func (p *sourceFenceSagaProvider) AbandonCheckpointConnections(_ context.Context, d RestoreSourceDefinition, m CheckpointMaintenance, i CheckpointConnectionIdentity) (CheckpointConnectionTerminal, error) {
	p.abandonCalls++
	if d != p.definition || m != p.maintenance {
		return CheckpointConnectionTerminal{}, ErrConflict
	}
	if p.terminal == nil {
		p.terminal = &CheckpointConnectionTerminal{CheckpointConnectionIdentity: i, State: "released", ReleasedAt: time.Now().UTC().Truncate(time.Microsecond)}
	}
	if p.abandonLost {
		p.abandonLost = false
		return CheckpointConnectionTerminal{}, ErrUnavailable
	}
	return *p.terminal, nil
}
func (p *sourceFenceSagaProvider) ObserveCheckpointConnections(_ context.Context, d RestoreSourceDefinition, m CheckpointMaintenance, i CheckpointConnectionIdentity) (CheckpointConnectionTerminal, error) {
	if p.terminal == nil || d != p.definition || m != p.maintenance || p.terminal.CheckpointConnectionIdentity != i {
		return CheckpointConnectionTerminal{}, ErrNotFound
	}
	return *p.terminal, nil
}

func sourceFenceSagaFixture(t *testing.T) (*CutoverService, *PostgresStore, *state.PgStore, Cutover, *sourceFenceSagaProvider, *bool) {
	t.Helper()
	s, ps, c, node, _ := cutoverDrainPlanFixture(t, drainPlanInstanceSpec{"default", "normal", "running"})
	provider := &sourceFenceSagaProvider{}
	provider.capabilities = testCapabilities()
	provider.capabilities.CredentialAccess = append(provider.capabilities.CredentialAccess, CredentialMigration)
	registry := &Registry{backends: map[string]Backend{c.Source.BackendID: {ID: c.Source.BackendID, Fingerprint: c.Source.BackendFingerprint,
		Region: testSpec().Region, Capabilities: provider.Capabilities(), Provider: provider}}}
	enabled := true
	bs, err := NewBindingService(registry, s, s, newBindingCredentialSink(), BindingServiceOptions{
		ProvisioningEnabled: func() bool { return enabled }, ProviderTimeout: 30 * time.Second, LeaseDuration: 2 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	bs.retryInterval = time.Nanosecond
	service, err := NewCutoverService(bs, s, &cutoverTestSealer{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.RequestSourceFence(t.Context(), c.AccountID, c.ID); !errors.Is(err, ErrConflict) {
		t.Fatal("SQL fence allowed without runtime retirement", err)
	}
	plan := fenceDrainPlan(t, s, ps, c, node.ID)
	if _, err = service.RequestSourceFence(t.Context(), c.AccountID, c.ID); !errors.Is(err, ErrConflict) {
		t.Fatal("SQL fence allowed before native receipt", err)
	}
	frame := plan.Members[0].Drain.Frame
	if err := ps.CompleteManagedPostgresDrain(t.Context(), frame, cutoverDrainProof(frame)); err != nil {
		t.Fatal(err)
	}
	return service, s, ps, c, provider, &enabled
}

func sourceFenceDue(t *testing.T, s *PostgresStore, c Cutover) {
	t.Helper()
	if _, err := s.pool.Exec(t.Context(), `UPDATE managed_postgres_cutover_source_fences SET retry_at=clock_timestamp()-interval '1 second' WHERE cutover_id=$1 AND state<>'released'`, c.ID); err != nil {
		t.Fatal(err)
	}
}

func sourceFenceMigrationStatements(t *testing.T) (string, string) {
	t.Helper()
	raw, err := migrations.FS.ReadFile("20261016165128592_managed_postgres_cutover_source_fences.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(string(raw), "-- +goose Down")
	writerUp, writerDown := writerCatalogMigrationStatements(t)
	return parts[0] + "\n" + writerUp, writerDown + "\n" + parts[1]
}

func prepareSourceFenceSaga(t *testing.T, service *CutoverService, s *PostgresStore, c Cutover) CutoverSourceFence {
	t.Helper()
	f, err := service.RequestSourceFence(t.Context(), c.AccountID, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	for range 4 {
		sourceFenceDue(t, s, c)
		f, err = service.ReconcileSourceFence(t.Context(), c.AccountID, c.ID)
		if err != nil {
			t.Fatal("maintenance/selection", f.State, f.MaintenanceStage, err)
		}
	}
	if f.MaintenanceStage != "ready" || len(f.DatabaseNames) != 2 || !f.CloseRequestedAt.IsZero() {
		t.Fatal("selection was not durable before dispatch", f.State, f.MaintenanceStage)
	}
	return f
}

func TestPostgresCutoverSourceFenceLostReplyPreflightAndCancellation(t *testing.T) {
	service, s, _, c, provider, enabled := sourceFenceSagaFixture(t)
	f := prepareSourceFenceSaga(t, service, s, c)
	if f.OwnerToken == c.ID || f.OwnerToken == f.Maintenance.OwnerToken {
		t.Fatal("public identity reused as private operation owner")
	}
	provider.closeLost = true
	sourceFenceDue(t, s, c)
	if _, err := service.ReconcileSourceFence(t.Context(), c.AccountID, c.ID); err == nil || strings.Contains(err.Error(), "private provider") {
		t.Fatal("unknown close outcome or private error lost", err)
	}
	f, err := s.GetCutoverSourceFence(t.Context(), c.AccountID, c.ID)
	if err != nil || f.State != "closing" || f.CloseRequestedAt.IsZero() || f.Observation != nil {
		t.Fatal("unknown outcome did not retain dispatch", f.State, err)
	}
	// Restart recovers the exact token and original selection through observation.
	restarted, err := NewPostgresStore(s.pool)
	if err != nil {
		t.Fatal(err)
	}
	service.store = restarted
	sourceFenceDue(t, s, c)
	f, err = service.ReconcileSourceFence(t.Context(), c.AccountID, c.ID)
	if err != nil || f.State != "closed" || provider.closeCalls != 1 || f.Observation == nil {
		t.Fatal("lost close reply caused a new owner or duplicate dispatch", f.State, err)
	}
	p := completeRuntimePreflight(t, s, c)
	if p.SourceSessions == nil || !p.SourceSessions.AdmissionClosed || p.SourceSessions.SelectedDatabasesDrained ||
		p.SourceSessions.Sessions != 3 || p.SourceSessions.PreparedTransactions != 1 || !slices.Contains(p.SourceSessions.Blockers, "database_coverage_incomplete") {
		t.Fatal("SQL blockers omitted", p.SourceSessions)
	}
	sourceFenceDue(t, s, c)
	if _, err = service.ReconcileSourceFence(t.Context(), c.AccountID, c.ID); err != nil {
		t.Fatal(err)
	}
	unchanged, err := s.GetCutoverPreflight(t.Context(), c.AccountID, c.ID)
	if err != nil || unchanged.SourceSessionsChanged {
		t.Fatal("identical refresh invalidated historical evidence", err)
	}
	provider.closed.Databases[0].Sessions, provider.closed.Databases[1].PreparedTransactions = 0, 0
	provider.closed.UnselectedDatabases, provider.closed.Drained = 0, true
	sourceFenceDue(t, s, c)
	if _, err = service.ReconcileSourceFence(t.Context(), c.AccountID, c.ID); err != nil {
		t.Fatal(err)
	}
	old, err := s.GetCutoverPreflight(t.Context(), c.AccountID, c.ID)
	if err != nil || !old.SourceSessionsChanged || old.SourceSessions.Sessions != 3 {
		t.Fatal("historical SQL findings silently rewritten", err)
	}
	p = completeRuntimePreflight(t, s, c)
	if !p.SourceSessions.SelectedDatabasesDrained || !slices.Contains(p.Blockers, "source_sessions_unfenced") || !slices.Contains(p.Blockers, "activation_disabled") {
		t.Fatal("selected drainage promoted complete isolation", p.SourceSessions)
	}
	provider.observeFailed = true
	sourceFenceDue(t, s, c)
	if _, err = service.ReconcileSourceFence(t.Context(), c.AccountID, c.ID); err == nil {
		t.Fatal("failed refresh ignored")
	}
	p = completeRuntimePreflight(t, s, c)
	if p.SourceSessions.AdmissionClosed || !slices.Contains(p.SourceSessions.Blockers, "source_fence_observation_unavailable") {
		t.Fatal(p.SourceSessions)
	}
	provider.observeFailed = false
	if _, err = s.GetCutoverSourceFence(t.Context(), uuid.NewString(), c.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("SQL inventory tenant leak", err)
	}
	raw, err := json.Marshal(p.SourceSessions)
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{f.OwnerToken, f.Maintenance.OwnerToken, "private_alpha", "private_beta", c.Source.ProviderResourceID, "OwnerOID"} {
		if strings.Contains(string(raw), private) {
			t.Fatal("private SQL authority exposed", private)
		}
	}
	if _, err = s.CancelCutover(t.Context(), c.AccountID, c.ID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if _, err = s.pool.Exec(t.Context(), `UPDATE managed_postgres_cutovers SET state='cancelled' WHERE id=$1`, c.ID); err == nil {
		t.Fatal("unfinished SQL hold allowed cancellation completion")
	}
	provider.abandonLost, *enabled = true, false
	if _, err = service.ReconcileSourceFence(t.Context(), c.AccountID, c.ID); err == nil {
		t.Fatal("lost terminal reply discarded hold")
	}
	sourceFenceDue(t, s, c)
	f, err = service.ReconcileSourceFence(t.Context(), c.AccountID, c.ID)
	if err != nil || f.State != "released" || f.RemoteTerminalState != "released" || provider.abandonCalls != 2 {
		t.Fatal("terminal recovery failed with provisioning disabled", f.State, err)
	}
	if _, err := provider.CloseCheckpointConnections(t.Context(), f.definition(), f.Maintenance, f.request()); !errors.Is(err, ErrConflict) {
		t.Fatal("late close survived terminal tombstone", err)
	}
	for range len(c.Credentials) {
		if _, err = service.Reconcile(t.Context(), c.AccountID, c.ID); err != nil {
			t.Fatal("cancel after SQL cleanup", err)
		}
	}
	final, err := s.GetCutover(t.Context(), c.AccountID, c.ID)
	if err != nil || final.State != CutoverCancelled {
		t.Fatal(final.State, err)
	}
}

func TestPostgresCutoverSourceFenceExclusionAndNeverDispatchedCancellation(t *testing.T) {
	service, s, ps, c, provider, _ := sourceFenceSagaFixture(t)
	project, operation := uuid.NewString(), uuid.NewString()
	if _, err := s.pool.Exec(t.Context(), `INSERT INTO projects(id,account_id,slug) VALUES($1,$2,'source-fence')`, project, c.AccountID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(t.Context(), `INSERT INTO project_environment_clone_operations(id,account_id,project_id,source_environment,target_environment,idempotency_key,source_revision_hash)
 VALUES($1,$2,$3,'source','target','source-fence',repeat('a',64))`, operation, c.AccountID, project); err != nil {
		t.Fatal(err)
	}
	d, err := s.Get(t.Context(), c.AccountID, c.Source.ID)
	if err != nil {
		t.Fatal(err)
	}
	insertClone := `INSERT INTO project_environment_clone_postgres_write_fences(operation_id,source_database_id,source_version,backend_id,backend_fingerprint,source_provider_resource_id,source_data_resource_id)
 VALUES($1,$2,repeat('a',64),$3,$4,$5,$6)`
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if _, err = tx.Exec(ctx, insertClone, operation, d.ID, d.BackendID, d.BackendFingerprint, d.ProviderResourceID, d.DataResourceID); err != nil {
		t.Fatal(err)
	}
	waiting := make(chan error, 1)
	go func() {
		_, err := service.RequestSourceFence(ctx, c.AccountID, c.ID)
		waiting <- err
	}()
	waitDrainPlanDatabaseLocks(t, ctx, s, 1)
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err = <-waiting; !errors.Is(err, ErrConflict) {
		t.Fatal("waiting cutover bypassed a newly committed clone hold", err)
	}
	if _, err = s.GetCutoverSourceFence(t.Context(), c.AccountID, c.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("failed reservation committed partial recovery ownership", err)
	}
	if _, err = s.pool.Exec(t.Context(), `UPDATE project_environment_clone_postgres_write_fences SET state='released',remote_terminal_state='abandoned',remote_released_at=clock_timestamp(),released_at=clock_timestamp() WHERE operation_id=$1`, operation); err != nil {
		t.Fatal(err)
	}
	f, err := service.RequestSourceFence(t.Context(), c.AccountID, c.ID)
	if err != nil || f.State != "pending" {
		t.Fatal(f.State, err)
	}
	if _, err = s.pool.Exec(t.Context(), `UPDATE project_environment_clone_postgres_write_fences SET state='held',remote_terminal_state=NULL,remote_released_at=NULL,released_at=NULL WHERE operation_id=$1`, operation); err == nil {
		t.Fatal("released clone hold reopened over a cutover")
	}
	if _, err = s.pool.Exec(t.Context(), `DELETE FROM managed_postgres_cutover_source_fences WHERE cutover_id=$1`, c.ID); err == nil {
		t.Fatal("recovery hold deleted")
	}
	up, down := sourceFenceMigrationStatements(t)
	if _, err = s.pool.Exec(t.Context(), down); err == nil {
		t.Fatal("downgrade discarded live SQL authority")
	}
	// The clone insert is also rejected before FK checking by the shared guard.
	_, err = s.pool.Exec(t.Context(), insertClone, uuid.NewString(), d.ID, d.BackendID, d.BackendFingerprint, d.ProviderResourceID, d.DataResourceID)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "55000" {
		t.Fatal("cutover did not exclude clone admission", err)
	}
	if _, err = s.CancelCutover(t.Context(), c.AccountID, c.ID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	f, err = service.ReconcileSourceFence(t.Context(), c.AccountID, c.ID)
	if err != nil || f.State != "released" || f.RemoteTerminalState != "not_dispatched" || provider.abandonCalls != 0 || provider.closeCalls != 0 {
		t.Fatal("never-dispatched close required or performed remote mutation", f.State, err)
	}
	// No maintenance dispatch occurred, so this migration can be rolled back.
	for _, statement := range []string{down, up} {
		if _, err = s.pool.Exec(t.Context(), statement); err != nil {
			t.Fatal("source fence migration round trip", err)
		}
	}
	coverage, err := ps.ProjectEnvironmentCloneSchemaCoverage(t.Context(), c.AccountID, project)
	if err != nil || !coverage.Known {
		t.Fatal("SQL journal schema escaped clone classification", coverage.Blockers, err)
	}
	// ADR-1646/1647: probe/activation/removal receipts are source-bound
	// authority and audit evidence, never configuration to copy into a clone.
	for _, name := range []string{"managed_postgres_cutover_source_fences", "managed_postgres_cutover_preflights",
		"environment_binding_release_receipts", "environment_binding_retirement_receipts", "environment_qualification_service_receipts"} {
		i := slices.IndexFunc(coverage.Tables, func(p state.ProjectEnvironmentCloneSchemaPolicy) bool { return p.TableName == name })
		if i < 0 || coverage.Tables[i].Kind != state.CloneSchemaOperational {
			t.Fatal("private recovery table is cloneable", name)
		}
	}
	// ADR-20261017191509/20261017191518: readiness policies remain copy-gated
	// until an isolated strategy exists; deployment proofs, one-shot overrides,
	// monitors, cursors and incident history remain operational state at the
	// source, matching the clone schema registry.
	mcpCloneKinds := map[string]string{
		"app_mcp_readiness_policies":               state.CloneSchemaConfiguration,
		"app_mcp_readiness_policy_history":         state.CloneSchemaConfiguration,
		"deployment_mcp_readiness_evidence":        state.CloneSchemaOperational,
		"deployment_mcp_readiness_monitors":        state.CloneSchemaOperational,
		"deployment_mcp_readiness_overrides":       state.CloneSchemaOperational,
		"mcp_readiness_attention_actions":          state.CloneSchemaOperational,
		"mcp_readiness_fallback_policies":          state.CloneSchemaConfiguration,
		"mcp_readiness_incident_attention":         state.CloneSchemaOperational,
		"mcp_readiness_incident_cursors":           state.CloneSchemaOperational,
		"mcp_readiness_incident_events":            state.CloneSchemaOperational,
		"mcp_readiness_incidents":                  state.CloneSchemaOperational,
		"mcp_readiness_maintenance_windows":        state.CloneSchemaConfiguration,
		"mcp_readiness_notification_actions":       state.CloneSchemaOperational,
		"mcp_readiness_pagerduty_ack_events":       state.CloneSchemaOperational,
		"mcp_readiness_pagerduty_acknowledgements": state.CloneSchemaConfiguration,
		"mcp_readiness_pagerduty_policies":         state.CloneSchemaConfiguration,
		"mcp_readiness_reminder_defaults":          state.CloneSchemaConfiguration,
	}
	for name, kind := range mcpCloneKinds {
		i := slices.IndexFunc(coverage.Tables, func(p state.ProjectEnvironmentCloneSchemaPolicy) bool { return p.TableName == name })
		if i < 0 || coverage.Tables[i].Kind != kind {
			t.Fatal("MCP readiness clone boundary changed", name, kind)
		}
		if kind == state.CloneSchemaConfiguration && !slices.ContainsFunc(coverage.Blockers, func(b state.ProjectEnvironmentCloneCoverageBlocker) bool {
			return b.Table == name && b.Code == "isolated_strategy_unavailable"
		}) {
			t.Fatal("MCP readiness policy became copyable without a scoped strategy", name)
		}
	}

}

func TestPostgresCutoverSourceFenceFailedReceiptAndOwnerChange(t *testing.T) {
	service, s, ps, c, provider, enabled := sourceFenceSagaFixture(t)
	f := prepareSourceFenceSaga(t, service, s, c)
	service.store = sourceFenceFailReceiptStore{CutoverStore: s, CutoverSourceFenceStore: s}
	sourceFenceDue(t, s, c)
	if _, err := service.ReconcileSourceFence(t.Context(), c.AccountID, c.ID); !errors.Is(err, ErrUnavailable) {
		t.Fatal("failed local receipt write ignored", err)
	}
	if _, err := s.pool.Exec(t.Context(), `UPDATE managed_postgres_cutover_source_fences SET lease_until=clock_timestamp()-interval '1 second' WHERE cutover_id=$1`, c.ID); err != nil {
		t.Fatal(err)
	}
	restarted, err := NewPostgresStore(s.pool)
	if err != nil {
		t.Fatal(err)
	}
	service.store = restarted
	sourceFenceDue(t, s, c)
	recovered, err := service.ReconcileSourceFence(t.Context(), c.AccountID, c.ID)
	if err != nil || recovered.State != "closed" || recovered.OwnerToken != f.OwnerToken || provider.closeCalls != 1 {
		t.Fatal("receipt loss replayed remote close", recovered.State, err)
	}
	p := completeRuntimePreflight(t, s, c)
	other, err := ps.ComputeNodeByName(t.Context(), state.DefaultLocalNodeName)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.pool.Exec(t.Context(), `UPDATE apps SET node_id=$2 WHERE id=$1`, c.AppID, other.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.pool.Exec(t.Context(), `UPDATE apps SET node_id=$2 WHERE id=$1`, c.AppID, f.OwnerNodeID); err != nil {
		t.Fatal(err)
	}
	old, err := s.GetCutoverPreflight(t.Context(), c.AccountID, c.ID)
	if err != nil || !old.SourceSessionsChanged || old.SourceSessions.ObservedAt != p.SourceSessions.ObservedAt {
		t.Fatal("owner change silently adopted historical SQL evidence", err)
	}
	*enabled = false
	sourceFenceDue(t, s, c)
	released, err := service.ReconcileSourceFence(t.Context(), c.AccountID, c.ID)
	if err != nil || released.State != "released" || released.OwnerRevision != f.OwnerRevision || provider.abandonCalls != 1 {
		t.Fatal("old owner was adopted instead of abandoned", released.State, err)
	}
}

func TestPostgresCutoverSourceFenceCancellationFencesStaleWorker(t *testing.T) {
	service, s, _, c, provider, _ := sourceFenceSagaFixture(t)
	prepareSourceFenceSaga(t, service, s, c)
	sourceFenceDue(t, s, c)
	now := time.Now().UTC()
	claimed, err := s.ClaimCutoverSourceFence(t.Context(), c.AccountID, c.ID, uuid.NewString(), now, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	next := claimed
	next.State, next.CloseRequestedAt = "closing", time.Now().UTC()
	marked, err := s.SaveCutoverSourceFence(t.Context(), claimed, next, true, now, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.CancelCutover(t.Context(), c.AccountID, c.ID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SaveCutoverSourceFence(t.Context(), marked, marked, false, now, now); !errors.Is(err, ErrConflict) {
		t.Fatal("cancelled worker retained its mutation lease", err)
	}
	released, err := service.ReconcileSourceFence(t.Context(), c.AccountID, c.ID)
	if err != nil || released.State != "released" || provider.abandonCalls != 1 || provider.closeCalls != 0 {
		t.Fatal("possible dispatch released without a remote tombstone", released.State, err)
	}
	if _, err = provider.CloseCheckpointConnections(t.Context(), marked.definition(), marked.Maintenance, marked.request()); !errors.Is(err, ErrConflict) {
		t.Fatal("delayed cancelled worker reclosed admission", err)
	}
}

func TestPostgresCutoverSourceFenceExpiredVerificationWaitsAndCancels(t *testing.T) {
	service, s, _, c, provider, enabled := sourceFenceSagaFixture(t)
	if _, err := service.RequestSourceFence(t.Context(), c.AccountID, c.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(t.Context(), `UPDATE managed_postgres_cutover_credentials SET verified_at=clock_timestamp()-interval '6 minutes' WHERE cutover_id=$1`, c.ID); err != nil {
		t.Fatal(err)
	}
	f, err := service.ReconcileSourceFence(t.Context(), c.AccountID, c.ID)
	if !errors.Is(err, ErrUnavailable) || f.State != "pending" || f.LeaseToken != "" || !f.RetryAt.After(time.Now()) ||
		f.LastErrorCode != "source_fence_verification_required" || provider.maintenance.OwnerToken != "" {
		t.Fatal("expired verification dispatched work or monopolized the due queue", f.State, err)
	}
	rows, err := s.DueCutoverSourceFences(t.Context(), true, 10, time.Now().UTC())
	if err != nil || len(rows) != 0 {
		t.Fatal("verification wait ignored durable backoff", len(rows), err)
	}
	*enabled = false
	if _, err = s.CancelCutover(t.Context(), c.AccountID, c.ID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	f, err = service.ReconcileSourceFence(t.Context(), c.AccountID, c.ID)
	if err != nil || f.State != "released" || f.RemoteTerminalState != "not_dispatched" || provider.abandonCalls != 0 {
		t.Fatal("verification backoff delayed cancellation cleanup", f.State, err)
	}
}
