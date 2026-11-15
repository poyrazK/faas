// adr: 20261112220000 — access controls and initial-copy observability.
package datapipeline

import (
	"context"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestNativeFinalReaderRevocationSurvivesDisconnect(t *testing.T) {
	f := newNativeRecoveryFixture(t, `CREATE TABLE public.orders(id integer PRIMARY KEY);INSERT INTO public.orders VALUES(1)`)
	role := nativeReaderRole(t, f)
	f.service.CanCreate = func(context.Context, string) bool { return false }
	approveNativeReaders(t, f, []string{})
	f.control(t, "delete")
	f.await(t, "deleted")
	if len(f.pipeline.ReadSubjects) != 0 || f.pipeline.ReaderChange.State != "applied" {
		t.Fatal("terminal disconnect skipped access revocation", f.pipeline.ReaderChange)
	}
	if rows := nativeReadAs(t, f, role, "alice"); rows != 0 {
		t.Fatal("revoked token retained access", rows)
	}
	var retained int
	if err := f.target.QueryRow(t.Context(), `SELECT count(*) FROM public.orders`).Scan(&retained); err != nil || retained != 1 {
		t.Fatal("disconnect removed imported rows", retained, err)
	}
}

func TestNativeInitialCopyStallPersistsAndRecovers(t *testing.T) {
	f := newNativeRecoveryFixture(t, `CREATE TABLE public.orders(id integer PRIMARY KEY);INSERT INTO public.orders VALUES(1)`)
	role := "copy_failure_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	if _, err := f.source.Exec(t.Context(), "CREATE ROLE "+ident(role)+" LOGIN REPLICATION"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = f.admin.Exec(context.Background(), "DROP ROLE IF EXISTS "+ident(role)) })
	original := f.engine.pinSource
	f.engine.pinSource = func(ctx context.Context, raw string) (string, error) {
		pinned, err := original(ctx, raw)
		if err != nil {
			return "", err
		}
		u, err := url.Parse(pinned)
		if err != nil {
			return "", err
		}
		u.User = url.User(role)
		return u.String(), nil
	}
	// The injected native login can discover publications but cannot COPY rows.
	// The worker retains valid metadata access, reproducing an otherwise silent stall.
	f.control(t, "resync")
	awaitNativeWorkflow(t, f, func(p Pipeline) bool {
		var copying bool
		return p.State == "copying" && f.target.QueryRow(t.Context(), `SELECT progress->0->>'PeakState'='d' FROM gregale_pipeline.table_copies WHERE pipeline_id=$1`, p.ID).Scan(&copying) == nil && copying
	})
	if _, err := f.target.Exec(t.Context(), `UPDATE gregale_pipeline.table_copies SET progress=jsonb_set(progress,'{0,ProgressAt}',to_jsonb($2::text)) WHERE pipeline_id=$1`, f.pipeline.ID, time.Now().Add(-6*time.Minute).UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	// A worker restart does not reset the durable progress clock/high-water mark.
	f.worker = &Worker{Store: f.store, Engine: f.engine, Identities: f.worker.Identities, Allowed: f.service.Allowed}
	f.tick(t)
	if f.pipeline.Reason != "initial_copy_stalled" {
		t.Fatal("stalled native COPY stayed silent", f.pipeline.State, f.pipeline.Reason)
	}
	// Other acceptance tests generate cluster WAL. Isolate this lower-priority
	// alert from retention pressure, which deliberately takes precedence.
	stalled := f.pipeline
	stalled.WALSample, stalled.WALRetention, stalled.RetainedWALBytes = nil, nil, nil
	if code, _ := alertCondition(stalled, time.Now()); code != "sync_blocked" {
		t.Fatal("stalled copy did not open an incident", code)
	}
	f.engine.pinSource = original
	f.await(t, "live")
	if f.pipeline.Reason != "" {
		t.Fatal("recovered copy retained a stall reason", f.pipeline.Reason)
	}
}

func TestNativeReadHooksRefreshAnAPIThatStartedBeforeInstallation(t *testing.T) {
	f := newNativeRecoveryFixture(t, `CREATE TABLE public.orders(id integer PRIMARY KEY);INSERT INTO public.orders VALUES(1)`)
	role := nativeReaderRole(t, f)
	if _, err := f.target.Exec(t.Context(), "ALTER ROLE "+ident(role)+" LOGIN NOINHERIT;DROP FUNCTION gregale_data_api.check_pipeline_request();DROP FUNCTION gregale_data_api.check_pipeline_read()"); err != nil {
		t.Fatal(err)
	}
	api := startNativeDataAPI(t, t.Context(), f.target.Config().ConnString(), role)
	defer func() { api.close() }()
	if status, body := api.request("GET", "alice", "", ""); status != 200 || !strings.Contains(body, `"id":1`) {
		t.Fatal("old API was not serving", status, body)
	}
	if _, err := f.target.Exec(t.Context(), `ALTER POLICY gregale_pipeline_read ON public.orders USING(false)`); err != nil {
		t.Fatal(err)
	}
	refreshes := 0
	f.engine.RefreshReadGate = func(ctx context.Context, _ Pipeline, _ string) error {
		refreshes++
		api.close()
		api = startNativeDataAPI(t, context.WithoutCancel(ctx), f.target.Config().ConnString(), role)
		return nil
	}
	f.control(t, "pause")
	f.tick(t)
	if refreshes != 1 {
		t.Fatal("hook installation did not refresh the preexisting API", refreshes)
	}
	if status, body := api.request("GET", "alice", "", ""); status != 503 || !strings.Contains(body, "mirror_unavailable") {
		t.Fatal("late-installed hook did not gate a real HTTP read", status, body)
	}
	if status, body := api.request("GET", "bob", "", ""); status != 200 || strings.TrimSpace(body) != "[]" {
		t.Fatal("unauthorized response changed", status, body)
	}
	f.tick(t)
	if refreshes != 1 {
		t.Fatal("read-gate refresh repeated after its receipt", refreshes)
	}
}
