package datapipeline

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
)

func approveNativeEvolution(t *testing.T, f *nativeRecoveryFixture, r api.DataPipelineEvolutionRequest) {
	t.Helper()
	r.ExpectedRevision, r.Action = f.pipeline.Revision, "preview"
	if err := f.service.Evolve(t.Context(), f.pipeline.AccountID, f.pipeline.ID, r); err != nil {
		t.Fatal(err)
	}
	p, err := f.store.Get(t.Context(), f.pipeline.AccountID, f.pipeline.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.service.Evolve(t.Context(), p.AccountID, p.ID, api.DataPipelineEvolutionRequest{ExpectedRevision: p.Revision, Action: "approve", ChangeID: p.Evolution.ID}); err != nil {
		t.Fatal(err)
	}
	f.pipeline, err = f.store.Get(t.Context(), p.AccountID, p.ID)
	if err != nil {
		t.Fatal(err)
	}
}

func awaitNativeEvolution(t *testing.T, f *nativeRecoveryFixture, done func(Pipeline) bool) {
	t.Helper()
	for deadline := time.Now().Add(time.Minute); ; {
		f.tick(t)
		if done(f.pipeline) {
			return
		}
		if f.pipeline.State == "resync_required" || time.Now().After(deadline) {
			t.Fatalf("evolution stalled: %+v schema=%+v", f.pipeline.DataPipeline, f.pipeline.SchemaChange)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func refreshNativeEvolutionCheck(t *testing.T, f *nativeRecoveryFixture, tables ...api.DataSourceTable) {
	t.Helper()
	if _, err := f.store.pool.Exec(t.Context(), `UPDATE data_connection_checks SET created_at=clock_timestamp()-interval '1 hour',observed_at=clock_timestamp()-interval '1 hour' WHERE connection_id=$1`, f.connection.ID); err != nil {
		t.Fatal(err)
	}
	fixturePassingCheck(t, f.store, f.pipeline.AccountID, f.connection, tables...)
}

func TestNativeEvolutionProjectionBackfillPublicationReplayAndTableCopy(t *testing.T) {
	f := newNativeRecoveryFixture(t, `CREATE TABLE public.orders(id integer PRIMARY KEY,tenant_id text NOT NULL,status text,secret text);INSERT INTO public.orders VALUES(1,'a','first','previously excluded'),(2,'b','second','private');CREATE TABLE public.customers(id integer PRIMARY KEY,tenant_id text NOT NULL,name text,excluded text);INSERT INTO public.customers VALUES(1,'a','customer','never copy')`, servingFixture())
	role := nativeReaderRole(t, f)
	approveNativeEvolution(t, f, api.DataPipelineEvolutionRequest{Kind: "projection", Columns: map[string][]string{"orders": {"id", "tenant_id", "status", "secret"}}})
	f.tick(t)
	if f.pipeline.Reason != "schema_change_pending" || f.pipeline.SchemaChange == nil || !f.pipeline.SchemaChange.Supported {
		t.Fatal("no exact source approval", f.pipeline.DataPipeline, f.pipeline.SchemaChange)
	}
	var present bool
	if err := f.target.QueryRow(t.Context(), `SELECT EXISTS(SELECT 1 FROM pg_attribute WHERE attrelid='public.orders'::regclass AND attname='secret' AND NOT attisdropped)`).Scan(&present); err != nil || present {
		t.Fatal("unapproved column exposed", present, err)
	}
	var published []string
	if err := f.source.QueryRow(t.Context(), `SELECT attnames FROM pg_publication_tables WHERE pubname=$1`, pipelineName(f.pipeline)).Scan(&published); err != nil || slices.Contains(published, "secret") {
		t.Fatal("publication expanded before exact approval", published, err)
	}
	if err := f.store.ApproveSchema(t.Context(), f.pipeline.AccountID, f.pipeline.ID, f.pipeline.Revision, f.pipeline.SchemaChange.ID, false); err != nil {
		t.Fatal(err)
	}
	refreshReady, lostAck := false, false
	f.engine.RefreshSchema = func(context.Context, Pipeline, SchemaChange) ([]SchemaRefresh, bool, error) {
		return nil, refreshReady, nil
	}
	original := f.engine.Target
	f.engine.Target = func(ctx context.Context, a, id string, run func(*pgx.Conn, string) error) error {
		return original(ctx, a, id, func(conn *pgx.Conn, owner string) error {
			if err := run(conn, owner); err != nil {
				return err
			}
			var phase string
			if err := conn.QueryRow(ctx, `SELECT schema_phase FROM gregale_pipeline.mirrors WHERE pipeline_id=$1`, f.pipeline.ID).Scan(&phase); err != nil {
				return err
			}
			if phase != "" && !lostAck {
				lostAck = true
				return ErrUnavailable
			}
			return nil
		})
	}
	awaitNativeEvolution(t, f, func(p Pipeline) bool { return p.Reason == "schema_api_refresh_pending" })
	if !lostAck || nativeReadAs(t, f, role, "alice") != 0 {
		t.Fatal("lost acknowledgement reopened unrefreshed data")
	}
	refreshReady = true
	awaitNativeEvolution(t, f, func(p Pipeline) bool { return p.State == "live" && p.Evolution.State == "applied" })
	var secret string
	if err := f.target.QueryRow(t.Context(), `SELECT secret FROM public.orders WHERE id=1`).Scan(&secret); err != nil || secret != "previously excluded" {
		t.Fatal("existing values not backfilled", secret, err)
	}
	if _, err := f.source.Exec(t.Context(), `UPDATE public.orders SET secret='CDC selected' WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	awaitNativeEvolution(t, f, func(Pipeline) bool {
		_ = f.target.QueryRow(t.Context(), `SELECT secret FROM public.orders WHERE id=1`).Scan(&secret)
		return secret == "CDC selected"
	})
	_, orders, err := inspectSourceTable(t.Context(), f.source, "orders")
	if err != nil {
		t.Fatal(err)
	}
	_, customers, err := inspectSourceTable(t.Context(), f.source, "customers")
	if err != nil {
		t.Fatal(err)
	}
	refreshNativeEvolutionCheck(t, f, orders, customers)
	approveNativeEvolution(t, f, api.DataPipelineEvolutionRequest{Kind: "projection", Columns: map[string][]string{"orders": {"id", "tenant_id", "status", "secret"}, "customers": {"id", "tenant_id", "name"}}})
	f.tick(t)
	if f.pipeline.Evolution.State != "approved" || f.pipeline.SchemaChange == nil || len(f.pipeline.SchemaChange.AddedTables) != 1 {
		t.Fatal("table copy marked applied before approval", f.pipeline)
	}
	if err = f.store.ApproveSchema(t.Context(), f.pipeline.AccountID, f.pipeline.ID, f.pipeline.Revision, f.pipeline.SchemaChange.ID, false); err != nil {
		t.Fatal(err)
	}
	awaitNativeEvolution(t, f, func(p Pipeline) bool { return p.State == "live" && p.Evolution.State == "applied" })
	if err = f.target.QueryRow(t.Context(), `SELECT EXISTS(SELECT 1 FROM pg_attribute WHERE attrelid='public.customers'::regclass AND attname='excluded' AND NOT attisdropped)`).Scan(&present); err != nil || present {
		t.Fatal("added table widened projection", present, err)
	}
}

func TestNativeEvolutionAuthProofExpiryRevocationAndUnavailableSource(t *testing.T) {
	f := newNativeRecoveryFixture(t, `CREATE TABLE public.orders(id integer PRIMARY KEY,tenant_id text NOT NULL,status text);INSERT INTO public.orders VALUES(1,'a','a row'),(2,'b','b row')`, servingFixture())
	role := nativeReaderRole(t, f)
	if _, err := f.target.Exec(t.Context(), "ALTER ROLE "+ident(role)+" LOGIN NOINHERIT PASSWORD 'pipeline-test'"); err != nil {
		t.Fatal(err)
	}
	auth := api.DataPipelineAuthOrganizations{AuthAppID: uuid.NewString(), BindingID: uuid.NewString(), DatabaseID: uuid.NewString(), Mappings: []api.DataPipelineOrganizationTenant{{OrganizationID: uuid.NewString(), Tenant: "a"}}}
	f.service.ResolveAuthOrganizations = func(context.Context, string, string, api.DataPipelineAuthOrganizations) (*api.DataPipelineAuthOrganizations, error) {
		return &auth, nil
	}
	members := []api.DataPipelineTenantMembership{{Subject: "alice", Tenant: "a"}}
	available := true
	f.worker.AuthMemberships = func(context.Context, Pipeline) (AuthMembershipSnapshot, error) {
		if !available {
			return AuthMembershipSnapshot{}, ErrUnavailable
		}
		return AuthMembershipSnapshot{Members: members, Issuer: "https://pipeline-test.example"}, nil
	}
	approveNativeEvolution(t, f, api.DataPipelineEvolutionRequest{Kind: "auth_organizations", AuthOrganizations: &auth})
	f.tick(t) // revoke the old manually granted memberships first
	if nativeReadAs(t, f, role, "alice") != 0 {
		t.Fatal("manual access survived Auth binding approval")
	}
	awaitNativeEvolution(t, f, func(p Pipeline) bool { return p.Evolution.State == "applied" })
	if nativeReadAs(t, f, role, "alice") != 1 || nativeReadAs(t, f, role, "bob") != 0 {
		t.Fatal("snapshot did not control tenant grants")
	}
	httpAPI := startNativeDataAPI(t, t.Context(), f.target.Config().ConnString(), role)
	defer httpAPI.close()
	otherIssuer := startNativeDataAPIForIssuer(t, t.Context(), f.target.Config().ConnString(), role, "https://other-auth.example", true)
	statusCode, body := otherIssuer.request("GET", "alice", "select=id", "")
	otherIssuer.close()
	if statusCode != 200 || strings.TrimSpace(body) != "[]" {
		t.Fatal("same subject from another verified issuer inherited access", statusCode, body)
	}
	statusCode, body = httpAPI.request("GET", "alice", "select=id", "")
	if statusCode != 200 || !strings.Contains(body, `"id":1`) {
		t.Fatal("approved issuer lost access", statusCode, body)
	}
	if _, err := f.source.Exec(t.Context(), "ALTER ROLE "+ident(f.role)+" NOLOGIN"); err != nil {
		t.Fatal(err)
	}
	members = nil
	f.tick(t)
	if nativeReadAs(t, f, role, "alice") != 0 {
		t.Fatal("revocation required replication source")
	}
	if _, err := f.source.Exec(t.Context(), "ALTER ROLE "+ident(f.role)+" LOGIN"); err != nil {
		t.Fatal(err)
	}
	members = []api.DataPipelineTenantMembership{{Subject: "alice", Tenant: "a"}}
	f.tick(t)
	// Install a short authoritative proof without sleeping through the full TTL.
	if _, err := f.store.pool.Exec(t.Context(), `UPDATE data_pipelines SET retry_at=clock_timestamp() WHERE id=$1`, f.pipeline.ID); err != nil {
		t.Fatal(err)
	}
	p, err := f.store.Claim(t.Context(), uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	c := authReaderChange(p, members, time.Now().UTC().Add(150*time.Millisecond).Format(time.RFC3339Nano), "https://pipeline-test.example")
	if err = f.store.StageAuthReaders(t.Context(), p, c); err != nil {
		t.Fatal(err)
	}
	p.ReaderChange = c
	wrongNamespace := *c
	wrongNamespace.BeforeIssuer = "https://other-auth.example"
	wrongNamespace.ID = readerChangeID(p, wrongNamespace)
	p.ReaderChange = &wrongNamespace
	if _, err = applyReaders(t.Context(), p, f.target, func(context.Context) error { return nil }); !errors.Is(err, ErrConflict) {
		t.Fatal("issuer receipt mismatch accepted", err)
	}
	p.ReaderChange = c
	applied, err := applyReaders(t.Context(), p, f.target, func(context.Context) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if _, err = applyReaders(t.Context(), p, f.target, func(context.Context) error { return nil }); err != nil {
		t.Fatal("issuer receipt replay after lost acknowledgement failed", err)
	}
	if err = f.store.Finish(t.Context(), p, Observation{ReaderOnly: true, ReaderChange: applied}); err != nil {
		t.Fatal(err)
	}
	available = false
	for until := time.Now().Add(3 * time.Second); ; {
		status, body := httpAPI.request("GET", "alice", "select=id", "")
		if status == 503 {
			if !strings.Contains(body, "pipeline_auth_membership_unconfirmed") {
				t.Fatal(body)
			}
			break
		}
		if time.Now().After(until) {
			t.Fatal("expired Auth proof served", status, body)
		}
		time.Sleep(25 * time.Millisecond)
	}
	if nativeReadAs(t, f, role, "alice") != 0 {
		t.Fatal("SQL access outlived Auth proof")
	}
	status, err := f.store.Get(t.Context(), p.AccountID, p.ID)
	if err != nil || status.Health.Code != "auth_membership_unconfirmed" {
		t.Fatal("expired proof stayed healthy without another worker tick", status.Health, err)
	}
	if err = f.service.TenantReaders(t.Context(), p.AccountID, p.ID, p.Revision, "preview", "", members); !errors.Is(err, ErrConflict) {
		t.Fatal("manual grant bypassed Auth", err)
	}
}
