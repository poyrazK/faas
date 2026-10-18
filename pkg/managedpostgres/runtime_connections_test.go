package managedpostgres

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestRuntimeConnectionsPGReconcilesLifecycleAndIsolatesEvidence(t *testing.T) {
	s, pool, ctx, account := postgresStoreFixture(t)
	ps := state.NewPgStore(pool)
	d := postgresReadyDatabase(t, s, account, "runtime-pools", time.Now().Add(-time.Minute))
	if _, err := pool.Exec(ctx, `UPDATE managed_postgres_databases SET data_resource_id='private-dataset' WHERE id=$1`, d.ID); err != nil {
		t.Fatal(err)
	}
	d.DataResourceID = "private-dataset"
	app, err := ps.CreateApp(ctx, state.App{AccountID: account, Slug: "pools-" + uuid.NewString()[:8], Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	node, err := ps.CreateComputeNode(ctx, state.ComputeNode{Name: "pools-" + uuid.NewString(), TargetURL: "unix:///tmp/pools.sock", VPCPUs: 2, MemMB: 8192, MaxConcurrency: 20, AdmissionCeilingMB: 4160, VCPUBudget: 16, Lifecycle: state.NodeLifecycleActive})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := ps.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "default", Kind: state.DeploymentKindImage, Status: state.DeployLive})
	if err != nil {
		t.Fatal(err)
	}
	instance, err := ps.CreateInstanceWithMode(ctx, app.ID, dep.ID, "running", 256, node.ID, "", "normal")
	if err != nil {
		t.Fatal(err)
	}
	for _, lifecycle := range []string{"waking", "parked"} {
		if _, err := ps.CreateInstanceWithMode(ctx, app.ID, dep.ID, lifecycle, 256, node.ID, "", "normal"); err != nil {
			t.Fatal(err)
		}
	}
	b := testBinding(account, d.ID, app.ID, uuid.NewString(), time.Now().Add(-time.Minute))
	b.Scope, b.Access = "default", CredentialDataAPI
	if _, _, err := s.ReserveBinding(ctx, b); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE managed_postgres_bindings SET state='ready',provider_identity_id='private-role',credential_ref='private-reference' WHERE id=$1`, b.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE instances SET started_at=now()-interval '1 minute' WHERE id=$1`, instance.ID); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte(d.BackendFingerprint + ":" + d.DataResourceID))
	authority := RuntimeConnectionAuthority{AccountID: account, AppID: app.ID, InstanceID: instance.ID, TokenExpiresAt: time.Now().Add(time.Minute)}
	r := RuntimeConnectionReport{Version: 1, BindingID: b.ID, DatabaseIdentity: hex.EncodeToString(digest[:]), DatabaseGeneration: d.DesiredGeneration, CredentialGeneration: 1, Role: "private-role", State: "observed", RuntimeModel: "postgrest-14.3-pools-v1", EngineConnections: 1, RefreshConnections: 1}
	record := func() {
		t.Helper()
		r.ObservedAt = time.Now().UTC().Truncate(time.Microsecond)
		if err := s.RecordRuntimeConnections(ctx, authority, r, time.Now()); err != nil {
			t.Fatal("record", err)
		}
	}
	read := func(observed int64) api.ManagedPostgresConnectionBudget {
		t.Helper()
		snapshot, err := s.ReadConnectionBudget(ctx, account, d.ID)
		if err != nil {
			t.Fatal(err)
		}
		got := describeConnectionBudget(snapshot, time.Now())
		coverage := got.Demand.RuntimeObservations
		if coverage == nil || coverage.ObservedInstances != observed || coverage.ConfiguredConnections != observed*2 || got.State != "unknown" || got.Enforced || !slices.Contains(got.Reasons, "external_consumers_unobserved") {
			t.Fatalf("coverage=%+v budget=%+v", coverage, got)
		}
		data, _ := json.Marshal(got)
		for _, private := range []string{app.ID, instance.ID, node.ID, "private-role", "private-reference", "backend_fingerprint", "database_identity"} {
			if strings.Contains(string(data), private) {
				t.Fatal("private evidence escaped", private)
			}
		}
		return got
	}
	record()
	got := read(1)
	if c := got.Demand.RuntimeObservations; c.State != "partial" || c.UnobservedInstances != 1 || c.ParkedInstances != 1 || got.Demand.DataAPIResidentEstimate != 4 {
		t.Fatal(c, got.Demand)
	}
	if err := s.RecordRuntimeConnections(ctx, authority, r, time.Now()); !errors.Is(err, ErrConflict) {
		t.Fatal("replay extended observation", err)
	}
	restarted, _ := NewPostgresStore(pool)
	if snapshot, err := restarted.ReadConnectionBudget(ctx, account, d.ID); err != nil || snapshot.Bindings[0].ObservedResidents != 1 {
		t.Fatal(snapshot, err)
	}
	r.State = "unknown"
	record()
	read(0)
	r.State = "observed"
	record()
	read(1)
	for _, tc := range []struct {
		name, change, restore string
		id                    string
	}{
		{"credential generation", `UPDATE managed_postgres_bindings SET credential_generation=2 WHERE id=$1`, `UPDATE managed_postgres_bindings SET credential_generation=1 WHERE id=$1`, b.ID},
		{"role", `UPDATE managed_postgres_bindings SET provider_identity_id='replacement' WHERE id=$1`, `UPDATE managed_postgres_bindings SET provider_identity_id='private-role' WHERE id=$1`, b.ID},
		{"binding transition", `UPDATE managed_postgres_bindings SET state='deleting' WHERE id=$1`, `UPDATE managed_postgres_bindings SET state='ready' WHERE id=$1`, b.ID},
		{"database generation", `UPDATE managed_postgres_databases SET desired_generation=2,observed_generation=2 WHERE id=$1`, `UPDATE managed_postgres_databases SET desired_generation=1,observed_generation=1 WHERE id=$1`, d.ID},
		{"dataset replacement", `UPDATE managed_postgres_databases SET data_resource_id='replacement' WHERE id=$1`, `UPDATE managed_postgres_databases SET data_resource_id=$2 WHERE id=$1`, d.ID},
		{"scope", `UPDATE deployments SET scope='other' WHERE id=$1`, `UPDATE deployments SET scope='default' WHERE id=$1`, dep.ID},
		{"heartbeat", `UPDATE compute_nodes SET last_heartbeat_at=now()-interval '1 hour' WHERE id=$1`, `UPDATE compute_nodes SET last_heartbeat_at=now() WHERE id=$1`, node.ID},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := pool.Exec(ctx, tc.change, tc.id); err != nil {
				t.Fatal(err)
			}
			read(0)
			r.ObservedAt = time.Now().UTC()
			if err := s.RecordRuntimeConnections(ctx, authority, r, time.Now()); !errors.Is(err, ErrConflict) {
				t.Fatal("changed authority accepted", err)
			}
			restoreArgs := []any{tc.id}
			if strings.Contains(tc.restore, "$2") {
				restoreArgs = append(restoreArgs, d.DataResourceID)
			}
			if _, err := pool.Exec(ctx, tc.restore, restoreArgs...); err != nil {
				t.Fatal(err)
			}
			record()
		})
	}
	other := authority
	other.AccountID = uuid.NewString()
	r.ObservedAt = time.Now().UTC()
	if err := s.RecordRuntimeConnections(ctx, other, r, time.Now()); !errors.Is(err, ErrConflict) {
		t.Fatal("cross-account report", err)
	}
	other = authority
	other.InstanceID = uuid.NewString()
	if err := s.RecordRuntimeConnections(ctx, other, r, time.Now()); !errors.Is(err, ErrConflict) {
		t.Fatal("cross-instance report", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE data_api_runtime_connection_observations SET observed_at=observed_at-interval '1 minute',expires_at=expires_at-interval '1 minute' WHERE binding_id=$1`, b.ID); err != nil {
		t.Fatal(err)
	}
	read(0)
	record()
	if _, err := pool.Exec(ctx, `UPDATE instances SET wake_id=gen_random_uuid() WHERE id=$1`, instance.ID); err != nil {
		t.Fatal(err)
	}
	read(0)
}

func TestRuntimeConnectionReportRejectsUnboundedAndStaleInputs(t *testing.T) {
	now := time.Now().UTC()
	a := RuntimeConnectionAuthority{AccountID: uuid.NewString(), AppID: uuid.NewString(), InstanceID: uuid.NewString(), TokenExpiresAt: now.Add(time.Minute)}
	base := RuntimeConnectionReport{Version: 1, BindingID: uuid.NewString(), DatabaseIdentity: strings.Repeat("a", 64), DatabaseGeneration: 1, CredentialGeneration: 1, Role: "restricted", State: "observed", RuntimeModel: "postgrest-14.3-pools-v1", EngineConnections: 2, ObservedAt: now}
	if !base.valid(a, now) {
		t.Fatal("valid report rejected")
	}
	for _, change := range []func(*RuntimeConnectionReport){
		func(r *RuntimeConnectionReport) { r.EngineConnections = 3 },
		func(r *RuntimeConnectionReport) { r.RefreshConnections = 1 },
		func(r *RuntimeConnectionReport) { r.RuntimeModel = "unqualified" },
		func(r *RuntimeConnectionReport) { r.ObservedAt = now.Add(time.Second) },
		func(r *RuntimeConnectionReport) { r.ObservedAt = now.Add(-api.DataAPIConnectionBudgetMaxAge) },
		func(r *RuntimeConnectionReport) { r.DatabaseIdentity = "private-target" },
		func(r *RuntimeConnectionReport) { r.DatabaseGeneration = 0 },
		func(r *RuntimeConnectionReport) { r.State = "verified" },
	} {
		r := base
		change(&r)
		if r.valid(a, now) {
			t.Fatal("invalid report accepted", r)
		}
	}
	a.TokenExpiresAt = now
	if base.valid(a, now) {
		t.Fatal("expired authority accepted")
	}
}
