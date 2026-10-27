package apidserver_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/apid/datapipelines"
	"github.com/onebox-faas/faas/pkg/datapipeline"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/session"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestDashboardPipelinesRequiresNamedCSRFAndScopesMetadata(t *testing.T) {
	ctx := t.Context()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "pipelines@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.MarkAccountEmailVerified(ctx, account.ID); err != nil {
		t.Fatal(err)
	}
	manager, err := session.NewEphemeralManager(apidserver.SessionCookieLifetime)
	if err != nil {
		t.Fatal(err)
	}
	sid := "pipelines-session"
	if _, err = store.CreateSession(ctx, sid, account.ID, "192.0.2.10", "pipeline-tests"); err != nil {
		t.Fatal(err)
	}
	token, err := manager.IssueWithSessionAndBindingHashAndStepUp(sid, account.ID, "", time.Now(), false)
	if err != nil {
		t.Fatal(err)
	}
	cookie := &http.Cookie{Name: apidserver.SessionCookie, Value: token}
	pool := pgtest.Open(t)
	schema, err := os.ReadFile("../../datapipeline/schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, string(schema)); err != nil {
		t.Fatal(err)
	}
	key, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	pipelines := &datapipeline.Service{Store: datapipeline.NewPostgresStore(pool), Recipient: key.Recipient(), Allowed: func(_ context.Context, id string) bool { return id == account.ID }, ValidateTarget: func(context.Context, string, string) error { return nil }}
	srv := apidserver.NewServerWithDeps(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", apidserver.NoopNotifier{}, "", apidserver.NoopMailer{}, apidserver.StubGithubdClient{}, manager, nil, 15*time.Minute, "")
	*srv.DataPipelines() = &datapipelines.Handler{Service: pipelines}
	handler := srv.Handler()
	r := httptest.NewRequest("GET", "/dashboard/data", nil)
	r.AddCookie(cookie)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, r)
	if response.Code != 200 || !strings.Contains(response.Body.String(), "Connect your data") {
		t.Fatalf("dashboard: %d %s", response.Code, response.Body.String())
	}
	var csrf *http.Cookie
	for _, c := range response.Result().Cookies() {
		if c.Name == "gregale_data_pipelines_csrf" {
			csrf = c
		}
	}
	if csrf == nil {
		t.Fatal("dashboard did not issue its named CSRF cookie")
	}
	form := url.Values{"action": {"connection"}, "name": {"orders-source"}, "connection_url": {"postgres://r:secret-sentinel@source.example/db?sslmode=verify-full"}, "csrf_token": {csrf.Value}}
	post := func(includeCSRF bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/dashboard/data", strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.AddCookie(cookie)
		if includeCSRF {
			r.AddCookie(csrf)
		}
		out := httptest.NewRecorder()
		handler.ServeHTTP(out, r)
		return out
	}
	if out := post(false); out.Code != 400 {
		t.Fatalf("missing CSRF: %d %s", out.Code, out.Body.String())
	}
	out := post(true)
	if out.Code != 303 || strings.Contains(out.Body.String(), "secret-sentinel") {
		t.Fatalf("source registration: %d %s", out.Code, out.Body.String())
	}
	items, err := pipelines.Store.Connections(ctx, account.ID)
	if err != nil || len(items) != 1 {
		t.Fatal("source registration was not persisted")
	}

	// Saved setup is accepted without provider I/O and is idempotent across
	// repeated, CSRF-verified browser submissions.
	pipelines.PlanMirror = func(context.Context, string, string) (datapipeline.MirrorPlan, error) {
		return datapipeline.MirrorPlan{Region: "test-region", StorageLimitBytes: 1 << 30, RestoreWindowSeconds: 3600}, nil
	}
	pipelines.EnsureMirror = func(context.Context, datapipeline.Setup) error {
		t.Fatal("dashboard requested provider I/O")
		return nil
	}
	pipelines.MirrorState = func(context.Context, string, string) (string, error) { return "provisioning", nil }
	authID := uuid.NewString()
	pipelines.PlanDataAPI = func(_ context.Context, owner string, request datapipeline.SetupRequest) (datapipeline.DataAPIPlan, error) {
		if owner != account.ID || request.AuthAppID != authID || request.DataAPIName != "browser-data" {
			return datapipeline.DataAPIPlan{}, datapipeline.ErrNotFound
		}
		return datapipeline.DataAPIPlan{AppID: uuid.NewString(), AuthAppID: authID, Issuer: "https://auth.example/auth/v1"}, nil
	}
	pipelines.PrepareDataAPI = func(context.Context, datapipeline.Setup) (string, bool, error) {
		t.Fatal("dashboard configured an app synchronously")
		return "", false, nil
	}
	pipelines.DeployDataAPI = func(context.Context, datapipeline.Setup) (string, string, error) {
		t.Fatal("dashboard enqueued a build synchronously")
		return "", "", nil
	}
	pipelines.DataAPIState = func(context.Context, datapipeline.Setup) (string, string, error) { return "building", "", nil }
	source := items[0]
	if _, err := pipelines.Store.RequestCheck(ctx, account.ID, source.ID, source.Revision, ""); err != nil {
		t.Fatal(err)
	}
	checkJob, err := pipelines.Store.ClaimCheck(ctx, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	sourceCapacity := &api.DataEndpointCapacity{State: "ready", RequiredConnections: 5, UsableConnections: 100, ObservedAt: now.Format(time.RFC3339Nano), ExpiresAt: now.Add(api.DataPipelineCapacityMaxAge).Format(time.RFC3339Nano)}
	if err := pipelines.Store.FinishCheck(ctx, checkJob, api.DataConnectionCheck{State: "passed", Tables: []api.DataSourceTable{{Name: "orders", Supported: true}}, Checks: []api.DataConnectionIssue{}, Capacity: &api.DataPipelineCapacity{State: "needs_qualification", Source: sourceCapacity}}); err != nil {
		t.Fatal(err)
	}
	form = url.Values{"action": {"setup"}, "name": {"browser-orders"}, "data_api_name": {"browser-data"}, "auth_app_id": {authID}, "connection_id": {source.ID}, "tables": {"orders"}, "read_subjects": {"alice"}, "csrf_token": {csrf.Value}}
	if out := post(false); out.Code != 400 {
		t.Fatalf("setup without CSRF: %d", out.Code)
	}
	for range 2 {
		if out := post(true); out.Code != 303 {
			t.Fatalf("saved setup: %d %s", out.Code, out.Body.String())
		}
	}
	setupStore := pipelines.Store.(datapipeline.SetupStore)
	setups, err := setupStore.Setups(ctx, account.ID)
	if err != nil || len(setups) != 1 {
		t.Fatalf("duplicate setup: %+v %v", setups, err)
	}
	if setups[0].Request.DataAPIName != "browser-data" || setups[0].Request.AuthAppID != authID || setups[0].DataAPIPlan.AppID == "" {
		t.Fatal("optional API intent was dropped")
	}
	r = httptest.NewRequest("GET", "/dashboard/data?connection="+source.ID, nil)
	r.AddCookie(cookie)
	savedPage := httptest.NewRecorder()
	handler.ServeHTTP(savedPage, r)
	if savedPage.Code != 200 || !strings.Contains(savedPage.Body.String(), "browser-orders") || !strings.Contains(savedPage.Body.String(), "Start saved setup") {
		t.Fatalf("saved progress: %d %s", savedPage.Code, savedPage.Body.String())
	}
	// A different account cannot cancel the saved operation through the domain.
	if _, err := setupStore.ControlSetup(ctx, uuid.NewString(), setups[0].ID, setups[0].Revision, "cancel"); err == nil {
		t.Fatal("cross-account setup control accepted")
	}
	form = url.Values{"action": {"setup_control"}, "setup_id": {setups[0].ID}, "revision": {"1"}, "control": {"cancel"}, "csrf_token": {csrf.Value}}
	if out := post(true); out.Code != 400 {
		t.Fatalf("unconfirmed setup cancellation: %d", out.Code)
	}
	form.Set("confirm_cancel", "yes")
	if out := post(true); out.Code != 303 {
		t.Fatalf("confirmed setup cancellation: %d %s", out.Code, out.Body.String())
	}
	// A schema approval uses the same named CSRF boundary and a fresh,
	// revision-bound exact preview. The form only saves intent.
	schemaID, pipelineID := strings.Repeat("b", 64), uuid.NewString()
	preview, _ := json.Marshal(datapipeline.SchemaChange{ID: schemaID, State: "pending", Supported: true, Changes: []datapipeline.SchemaColumnChange{{Table: "orders", Column: "note", Kind: "added", AfterType: "text", Safe: true}}})
	if _, err = pool.Exec(ctx, `INSERT INTO data_pipelines(id,account_id,name,source_connection_id,database_id,tables,read_subjects,state,observed_revision,observed_at,schema_change) VALUES($1,$2,'schema-orders',$3,$4,'["orders"]','["alice"]','blocked',1,clock_timestamp(),$5)`, pipelineID, account.ID, source.ID, uuid.NewString(), preview); err != nil {
		t.Fatal(err)
	}
	r = httptest.NewRequest("GET", "/dashboard/data", nil)
	r.AddCookie(cookie)
	schemaPage := httptest.NewRecorder()
	handler.ServeHTTP(schemaPage, r)
	if schemaPage.Code != 200 || !strings.Contains(schemaPage.Body.String(), "Approve additions") || !strings.Contains(schemaPage.Body.String(), "orders.note") {
		t.Fatal("schema approval preview missing")
	}
	form = url.Values{"action": {"schema_approve"}, "pipeline_id": {pipelineID}, "revision": {"1"}, "change_id": {schemaID}, "csrf_token": {csrf.Value}}
	if out := post(true); out.Code != 400 {
		t.Fatal("unconfirmed schema approval accepted", out.Code)
	}
	form.Set("confirm_schema", "yes")
	if out := post(false); out.Code != 400 {
		t.Fatal("schema approval without CSRF accepted", out.Code)
	}
	form.Set("change_id", strings.Repeat("c", 64))
	if out := post(true); out.Code != 409 {
		t.Fatal("different schema preview accepted", out.Code)
	}
	form.Set("change_id", schemaID)
	if out := post(true); out.Code != 303 {
		t.Fatal("confirmed schema approval failed", out.Code, out.Body.String())
	}
	if out := post(true); out.Code != 409 {
		t.Fatal("stale schema approval accepted", out.Code)
	}
	approved, err := pipelines.Store.Get(ctx, account.ID, pipelineID)
	if err != nil || approved.SchemaApproval != schemaID || approved.Revision != 2 {
		t.Fatal("schema approval intent not saved", err)
	}
	// The same named CSRF boundary protects stopping a specific verification run.
	runID := strings.Repeat("d", 64)
	progress, _ := json.Marshal(datapipeline.VerificationProgress{ID: runID, State: "running", Phase: "scan", Rows: 12000, Ranges: 6, TablesTotal: 1})
	if _, err = pool.Exec(ctx, `UPDATE data_pipelines SET verification_progress=$2,observed_revision=revision,observed_at=clock_timestamp() WHERE id=$1`, pipelineID, progress); err != nil {
		t.Fatal(err)
	}
	r = httptest.NewRequest("GET", "/dashboard/data", nil)
	r.AddCookie(cookie)
	verificationPage := httptest.NewRecorder()
	handler.ServeHTTP(verificationPage, r)
	if verificationPage.Code != 200 || !strings.Contains(verificationPage.Body.String(), "12000 rows checked") || !strings.Contains(verificationPage.Body.String(), "Stop verification") {
		t.Fatal("verification progress missing")
	}
	form = url.Values{"action": {"verification_cancel"}, "pipeline_id": {pipelineID}, "revision": {"2"}, "run_id": {runID}, "csrf_token": {csrf.Value}}
	if out := post(true); out.Code != 400 {
		t.Fatal("unconfirmed verification cancellation accepted", out.Code)
	}
	form.Set("confirm_verification", "yes")
	if out := post(false); out.Code != 400 {
		t.Fatal("verification cancellation without CSRF accepted", out.Code)
	}
	form.Set("run_id", strings.Repeat("e", 64))
	if out := post(true); out.Code != 409 {
		t.Fatal("different verification run cancelled", out.Code)
	}
	form.Set("run_id", runID)
	if out := post(true); out.Code != 303 {
		t.Fatal("verification cancellation failed", out.Code)
	}
	if out := post(true); out.Code != 409 {
		t.Fatal("stale cancellation accepted", out.Code)
	}
	// Health webhooks are opt-in, separately revisioned, account-scoped, and
	// protected by the same named CSRF boundary. Saved secrets never render.
	form = url.Values{"action": {"alerts_enable"}, "pipeline_id": {pipelineID}, "alert_revision": {"0"}, "alert_url": {"https://alerts.example/hook?token=webhook-url-sentinel"}, "alert_secret": {strings.Repeat("webhook-signing-sentinel", 2)}, "csrf_token": {csrf.Value}}
	if out := post(true); out.Code != 400 {
		t.Fatal("unconfirmed webhook accepted", out.Code)
	}
	form.Set("confirm_alerts", "yes")
	if out := post(false); out.Code != 400 {
		t.Fatal("webhook without named CSRF accepted", out.Code)
	}
	if out := post(true); out.Code != 303 {
		t.Fatal("webhook configuration failed", out.Code, out.Body.String())
	}
	if out := post(true); out.Code != 409 {
		t.Fatal("stale webhook configuration accepted", out.Code)
	}
	alertEventID, alertIncidentID := uuid.NewString(), uuid.NewString()
	if _, err = pool.Exec(ctx, `INSERT INTO data_pipeline_alert_deliveries(id,pipeline_id,account_id,config_revision,incident_id,kind,code,occurred_at,status,attempts,response_code,result) VALUES($1,$2,$3,1,$4,'incident','sync_blocked',clock_timestamp(),'dead',1,400,'receiver_rejected')`, alertEventID, pipelineID, account.ID, alertIncidentID); err != nil {
		t.Fatal(err)
	}
	r = httptest.NewRequest("GET", "/dashboard/data", nil)
	r.AddCookie(cookie)
	alertPage := httptest.NewRecorder()
	handler.ServeHTTP(alertPage, r)
	if alertPage.Code != 200 || !strings.Contains(alertPage.Body.String(), "Disable notifications") || !strings.Contains(alertPage.Body.String(), alertEventID) || !strings.Contains(alertPage.Body.String(), "receiver_rejected") || strings.Contains(alertPage.Body.String(), "webhook-url-sentinel") || strings.Contains(alertPage.Body.String(), "webhook-signing-sentinel") {
		t.Fatal("webhook settings missing or leaked", alertPage.Code)
	}
	allowed := pipelines.Allowed
	pipelines.Allowed = func(context.Context, string) bool { return false }
	form = url.Values{"action": {"alerts_disable"}, "pipeline_id": {pipelineID}, "alert_revision": {"1"}, "csrf_token": {csrf.Value}}
	if out := post(false); out.Code != 400 {
		t.Fatal("webhook opt-out without CSRF accepted", out.Code)
	}
	if out := post(true); out.Code != 303 {
		t.Fatal("webhook opt-out failed", out.Code)
	}
	pipelines.Allowed = allowed
	// Table selection is metadata-only; exact approval still needs confirmation,
	// current revision, fresh discovery and the named CSRF cookie.
	pipelines.CapacityStatus = func(context.Context, string, string) (*api.DataPipelineCapacity, error) {
		return &api.DataPipelineCapacity{State: "ready"}, nil
	}
	tableID := uuid.NewString()
	if _, err = pool.Exec(ctx, `INSERT INTO data_pipelines(id,account_id,name,source_connection_id,database_id,tables,read_subjects,state,observed_revision,observed_at) VALUES($1,$2,'table-orders',$3,$4,'["orders"]','["alice"]','live',1,clock_timestamp())`, tableID, account.ID, source.ID, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	discovered, _ := json.Marshal(api.DataConnectionCheck{Tables: []api.DataSourceTable{{Name: "orders", Supported: true}, {Name: "customers", Supported: true}}})
	if _, err = pool.Exec(ctx, `UPDATE data_connection_checks SET state='passed',result=$2,observed_at=clock_timestamp() WHERE connection_id=$1`, source.ID, discovered); err != nil {
		t.Fatal(err)
	}
	form = url.Values{"action": {"tables_request"}, "pipeline_id": {tableID}, "connection_id": {source.ID}, "revision": {"1"}, "tables": {"customers"}, "csrf_token": {csrf.Value}}
	if out := post(false); out.Code != 400 {
		t.Fatal("table selection without CSRF accepted", out.Code)
	}
	if out := post(true); out.Code != 303 {
		t.Fatal("table selection failed", out.Code, out.Body.String())
	}
	if out := post(true); out.Code != 409 {
		t.Fatal("stale table selection accepted", out.Code)
	}
	selection, err := pipelines.Store.Get(ctx, account.ID, tableID)
	if err != nil || len(selection.Tables) != 1 || selection.SchemaChange.State != "requested" {
		t.Fatal("selection applied tables before approval", err)
	}
	form = url.Values{"action": {"tables_cancel"}, "pipeline_id": {tableID}, "revision": {"2"}, "csrf_token": {csrf.Value}}
	if out := post(true); out.Code != 303 {
		t.Fatal("pending table selection could not be discarded", out.Code)
	}
	tablePlanID := strings.Repeat("f", 64)
	tablePlan, _ := json.Marshal(datapipeline.SchemaChange{ID: tablePlanID, State: "pending", Supported: true, AddedTables: []string{"customers"}, RequestedTables: []string{"customers", "orders"}})
	if _, err = pool.Exec(ctx, `UPDATE data_pipelines SET schema_change=$2,reason='table_addition_pending',observed_revision=revision,observed_at=clock_timestamp() WHERE id=$1`, tableID, tablePlan); err != nil {
		t.Fatal(err)
	}
	form = url.Values{"action": {"schema_approve"}, "pipeline_id": {tableID}, "revision": {"3"}, "change_id": {tablePlanID}, "csrf_token": {csrf.Value}}
	if out := post(true); out.Code != 400 {
		t.Fatal("table exposure accepted without confirmation", out.Code)
	}
	form.Set("confirm_schema", "yes")
	if out := post(true); out.Code != 303 {
		t.Fatal("approved table selection failed", out.Code, out.Body.String())
	}
	selection, err = pipelines.Store.Get(ctx, account.ID, tableID)
	if err != nil || len(selection.Tables) != 2 || selection.Generation != 1 || selection.SchemaApproval != tablePlanID {
		t.Fatal("approval did not preserve table-generation intent", err)
	}
	// Recovery uses the same named CSRF boundary, exact saved preview and a
	// separate confirmation before replacing one failed new table's rows.
	recoveryPlan := *selection.SchemaChange
	recoveryPlan.State = "verifying"
	recoveryJSON, _ := json.Marshal(recoveryPlan)
	failedJSON, _ := json.Marshal(datapipeline.VerificationProgress{ID: runID, State: "mismatch", Table: "customers"})
	if _, err = pool.Exec(ctx, `UPDATE data_pipelines SET schema_change=$2,verification_progress=$3,state='blocked',reason='replica_mismatch',observed_revision=revision,observed_at=clock_timestamp() WHERE id=$1`, tableID, recoveryJSON, failedJSON); err != nil {
		t.Fatal(err)
	}
	form = url.Values{"action": {"table_recovery_preview"}, "pipeline_id": {tableID}, "revision": {"4"}, "csrf_token": {csrf.Value}}
	if out := post(false); out.Code != 400 {
		t.Fatal("recovery preview without CSRF accepted", out.Code)
	}
	if out := post(true); out.Code != 303 {
		t.Fatal("recovery preview rejected", out.Code, out.Body.String())
	}
	if out := post(true); out.Code != 409 {
		t.Fatal("stale recovery preview accepted", out.Code)
	}
	recoveryID := strings.Repeat("b", 64)
	recoveryPlan.Recovery = &datapipeline.TableRecovery{ID: recoveryID, Table: "customers", State: "pending", Stage: "preview", Attempt: 1}
	recoveryJSON, _ = json.Marshal(recoveryPlan)
	if _, err = pool.Exec(ctx, `UPDATE data_pipelines SET schema_change=$2,reason='table_recovery_pending',observed_revision=revision,observed_at=clock_timestamp() WHERE id=$1`, tableID, recoveryJSON); err != nil {
		t.Fatal(err)
	}
	form = url.Values{"action": {"table_recovery_approve"}, "pipeline_id": {tableID}, "revision": {"5"}, "recovery_id": {recoveryID}, "csrf_token": {csrf.Value}}
	if out := post(true); out.Code != 400 {
		t.Fatal("recovery without confirmation accepted", out.Code)
	}
	form.Set("confirm_recopy", "yes")
	if out := post(false); out.Code != 400 {
		t.Fatal("recovery approval without CSRF accepted", out.Code)
	}
	form.Set("recovery_id", strings.Repeat("c", 64))
	if out := post(true); out.Code != 409 {
		t.Fatal("different recovery preview accepted", out.Code)
	}
	form.Set("recovery_id", recoveryID)
	if out := post(true); out.Code != 303 {
		t.Fatal("confirmed recovery rejected", out.Code, out.Body.String())
	}
	if out := post(true); out.Code != 409 {
		t.Fatal("stale recovery approval accepted", out.Code)
	}
	recovering, err := pipelines.Store.Get(ctx, account.ID, tableID)
	if err != nil || recovering.SchemaChange.Recovery.State != "approved" || recovering.SchemaChange.Retry != 1 || recovering.Generation != 1 {
		t.Fatal("recovery approval did not preserve intent", err)
	}
	// Initial COPY recovery selects a stalled new table without requiring a
	// verification mismatch; the caller cannot choose a healthy baseline table.
	copyID := uuid.NewString()
	copyPlan := datapipeline.SchemaChange{ID: tablePlanID, State: "verifying", Supported: true, AddedTables: []string{"customers"}, RequestedTables: []string{"customers", "orders"}, Copies: []datapipeline.TableCopyProgress{{Table: "customers", Phase: "copying", NativeState: "d", Stalled: true, Rows: 1234, Bytes: 5678, CountersKnown: true}}}
	copyJSON, _ := json.Marshal(copyPlan)
	if _, err = pool.Exec(ctx, `INSERT INTO data_pipelines(id,account_id,name,source_connection_id,database_id,tables,read_subjects,state,reason,schema_change,schema_approval,observed_revision,observed_at) VALUES($1,$2,'stalled-copy',$3,$4,'["customers","orders"]','["alice"]','blocked','table_copy_stalled',$5,$6,1,clock_timestamp())`, copyID, account.ID, source.ID, uuid.NewString(), copyJSON, tablePlanID); err != nil {
		t.Fatal(err)
	}
	form = url.Values{"action": {"table_recovery_preview"}, "pipeline_id": {copyID}, "revision": {"1"}, "copy_table": {"customers"}, "csrf_token": {csrf.Value}}
	if out := post(false); out.Code != 400 {
		t.Fatal("COPY preview without CSRF accepted", out.Code)
	}
	form.Set("copy_table", "orders")
	if out := post(true); out.Code != 409 {
		t.Fatal("healthy table selected for COPY recovery", out.Code)
	}
	form.Set("copy_table", "customers")
	if out := post(true); out.Code != 303 {
		t.Fatal("stalled COPY preview rejected", out.Code, out.Body.String())
	}
	copyRequested, err := pipelines.Store.Get(ctx, account.ID, copyID)
	if err != nil || copyRequested.SchemaChange.Recovery.Kind != "copy" || copyRequested.VerificationProgress != nil {
		t.Fatal("COPY preview required verification", err)
	}
	r = httptest.NewRequest("GET", "/dashboard/data", nil)
	r.AddCookie(cookie)
	copyPage := httptest.NewRecorder()
	handler.ServeHTTP(copyPage, r)
	if copyPage.Code != 200 || !strings.Contains(copyPage.Body.String(), "Table copy progress") || !strings.Contains(copyPage.Body.String(), "1234 rows") || !strings.Contains(copyPage.Body.String(), "no recent observed progress") {
		t.Fatal("COPY progress dashboard missing")
	}
	// Approved-table cancellation uses its own confirmation and named CSRF,
	// and never lets the browser select an existing baseline table.
	var cancelPlan map[string]any
	if err = json.Unmarshal(copyJSON, &cancelPlan); err != nil {
		t.Fatal(err)
	}
	cancelPlan["Before"] = []map[string]string{{"Name": "orders"}}
	cancelJSON, _ := json.Marshal(cancelPlan)
	if _, err = pool.Exec(ctx, `UPDATE data_pipelines SET schema_change=$2,observed_revision=revision,observed_at=clock_timestamp() WHERE id=$1`, copyID, cancelJSON); err != nil {
		t.Fatal(err)
	}
	form = url.Values{"action": {"table_cancellation_preview"}, "pipeline_id": {copyID}, "revision": {"2"}, "cancel_table": {"customers"}, "csrf_token": {csrf.Value}}
	if out := post(false); out.Code != 400 {
		t.Fatal("cancellation preview without CSRF accepted", out.Code)
	}
	form.Set("cancel_table", "orders")
	if out := post(true); out.Code != 409 {
		t.Fatal("baseline cancellation accepted", out.Code)
	}
	form.Set("cancel_table", "customers")
	if out := post(true); out.Code != 303 {
		t.Fatal("cancellation preview rejected", out.Code, out.Body.String())
	}
	form = url.Values{"action": {"table_cancellation_discard"}, "pipeline_id": {copyID}, "revision": {"3"}, "csrf_token": {csrf.Value}}
	if out := post(false); out.Code != 400 {
		t.Fatal("cancellation dismissal without CSRF accepted", out.Code)
	}
	if out := post(true); out.Code != 303 {
		t.Fatal("cancellation preview could not be dismissed", out.Code, out.Body.String())
	}
	kept, err := pipelines.Store.Get(ctx, account.ID, copyID)
	if err != nil || kept.SchemaChange.Cancellation != nil || len(kept.Tables) != 2 || kept.SchemaApproval != tablePlanID {
		t.Fatal("preview dismissal changed onboarding", err)
	}
	if _, err = pool.Exec(ctx, `UPDATE data_pipelines SET observed_revision=revision,observed_at=clock_timestamp() WHERE id=$1`, copyID); err != nil {
		t.Fatal(err)
	}
	form = url.Values{"action": {"table_cancellation_preview"}, "pipeline_id": {copyID}, "revision": {"4"}, "cancel_table": {"customers"}, "csrf_token": {csrf.Value}}
	if out := post(true); out.Code != 303 {
		t.Fatal("fresh cancellation preview rejected", out.Code)
	}
	cancellationID := strings.Repeat("d", 64)
	cancelPlan["Cancellation"] = &datapipeline.TableCancellation{ID: cancellationID, Table: "customers", State: "pending", Stage: "preview", Retention: "retain_private"}
	cancelJSON, _ = json.Marshal(cancelPlan)
	if _, err = pool.Exec(ctx, `UPDATE data_pipelines SET schema_change=$2,reason='table_cancellation_pending',observed_revision=revision,observed_at=clock_timestamp() WHERE id=$1`, copyID, cancelJSON); err != nil {
		t.Fatal(err)
	}
	r = httptest.NewRequest("GET", "/dashboard/data", nil)
	r.AddCookie(cookie)
	cancelPage := httptest.NewRecorder()
	handler.ServeHTTP(cancelPage, r)
	if cancelPage.Code != 200 || !strings.Contains(cancelPage.Body.String(), "Cancel this table's onboarding") || !strings.Contains(cancelPage.Body.String(), "Imported rows are retained privately") {
		t.Fatal("cancellation confirmation missing")
	}
	form = url.Values{"action": {"table_cancellation_approve"}, "pipeline_id": {copyID}, "revision": {"5"}, "cancellation_id": {cancellationID}, "csrf_token": {csrf.Value}}
	if out := post(true); out.Code != 400 {
		t.Fatal("cancellation without confirmation accepted", out.Code)
	}
	form.Set("confirm_cancel_table", "yes")
	if out := post(false); out.Code != 400 {
		t.Fatal("cancellation approval without CSRF accepted", out.Code)
	}
	form.Set("cancellation_id", strings.Repeat("e", 64))
	if out := post(true); out.Code != 409 {
		t.Fatal("different cancellation preview approved", out.Code)
	}
	form.Set("cancellation_id", cancellationID)
	if out := post(true); out.Code != 303 {
		t.Fatal("confirmed cancellation rejected", out.Code, out.Body.String())
	}
	if out := post(true); out.Code != 409 {
		t.Fatal("stale cancellation approval accepted", out.Code)
	}
	cancelling, err := pipelines.Store.Get(ctx, account.ID, copyID)
	if err != nil || cancelling.SchemaChange.Cancellation.State != "approved" || len(cancelling.Tables) != 2 || cancelling.Generation != 1 {
		t.Fatal("cancellation changed selection before cleanup", err)
	}
	form = url.Values{"action": {"table_cancellation_discard"}, "pipeline_id": {copyID}, "revision": {"6"}, "csrf_token": {csrf.Value}}
	if out := post(true); out.Code != 409 {
		t.Fatal("approved cancellation was discarded", out.Code)
	}
	// Removal of a completed table has its own exact confirmation and can be
	// started from the workspace without an additional connection check.
	liveID := uuid.NewString()
	if _, err = pool.Exec(ctx, `INSERT INTO data_pipelines(id,account_id,name,source_connection_id,database_id,tables,read_subjects,state,initialized,observed_revision,observed_at) VALUES($1,$2,'live-selection',$3,$4,'["customers","orders"]','["alice"]','live',true,1,clock_timestamp())`, liveID, account.ID, source.ID, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	r = httptest.NewRequest("GET", "/dashboard/data", nil)
	r.AddCookie(cookie)
	livePage := httptest.NewRecorder()
	handler.ServeHTTP(livePage, r)
	if livePage.Code != 200 || !strings.Contains(livePage.Body.String(), "Preview live-table removal") {
		t.Fatal("workspace removal control missing")
	}
	form = url.Values{"action": {"table_removal_preview"}, "pipeline_id": {liveID}, "revision": {"1"}, "remove_table": {"customers"}, "csrf_token": {csrf.Value}}
	if out := post(false); out.Code != 400 {
		t.Fatal("removal preview without CSRF accepted", out.Code)
	}
	form.Set("remove_table", "missing")
	if out := post(true); out.Code != 409 {
		t.Fatal("unselected live table accepted", out.Code)
	}
	form.Set("remove_table", "customers")
	if out := post(true); out.Code != 303 {
		t.Fatal("live removal preview rejected", out.Code, out.Body.String())
	}
	if out := post(true); out.Code != 409 {
		t.Fatal("stale removal preview accepted", out.Code)
	}
	r = httptest.NewRequest("GET", "/dashboard/data", nil)
	r.AddCookie(cookie)
	unobserved := httptest.NewRecorder()
	handler.ServeHTTP(unobserved, r)
	if unobserved.Code != 200 || !strings.Contains(unobserved.Body.String(), "Keep syncing this table") {
		t.Fatal("unobserved removal preview cannot be dismissed")
	}
	form = url.Values{"action": {"table_removal_discard"}, "pipeline_id": {liveID}, "revision": {"2"}, "csrf_token": {csrf.Value}}
	if out := post(false); out.Code != 400 {
		t.Fatal("live removal dismissal without CSRF accepted", out.Code)
	}
	if out := post(true); out.Code != 303 {
		t.Fatal("live removal preview could not be dismissed", out.Code)
	}
	keptLive, err := pipelines.Store.Get(ctx, account.ID, liveID)
	if err != nil || keptLive.SchemaChange != nil || len(keptLive.Tables) != 2 || keptLive.State != "live" {
		t.Fatal("dismissal changed live selection", err)
	}
	if _, err = pool.Exec(ctx, `UPDATE data_pipelines SET observed_revision=revision,observed_at=clock_timestamp() WHERE id=$1`, liveID); err != nil {
		t.Fatal(err)
	}
	form = url.Values{"action": {"table_removal_preview"}, "pipeline_id": {liveID}, "revision": {"3"}, "remove_table": {"customers"}, "csrf_token": {csrf.Value}}
	if out := post(true); out.Code != 303 {
		t.Fatal("fresh live removal rejected", out.Code)
	}
	removalID := strings.Repeat("a", 64)
	livePlan := map[string]any{"ID": tablePlanID, "State": "removal_pending", "Supported": true, "RequestedTables": []string{"customers", "orders"}, "Before": []map[string]string{{"Name": "customers"}, {"Name": "orders"}}, "After": []map[string]string{{"Name": "customers"}, {"Name": "orders"}}, "Cancellation": &datapipeline.TableCancellation{Kind: "live", ID: removalID, Table: "customers", State: "pending", Stage: "preview", Retention: "retain_private"}}
	liveJSON, _ := json.Marshal(livePlan)
	if _, err = pool.Exec(ctx, `UPDATE data_pipelines SET schema_change=$2,state='blocked',reason='table_removal_pending',observed_revision=revision,observed_at=clock_timestamp() WHERE id=$1`, liveID, liveJSON); err != nil {
		t.Fatal(err)
	}
	r = httptest.NewRequest("GET", "/dashboard/data", nil)
	r.AddCookie(cookie)
	confirmedPage := httptest.NewRecorder()
	handler.ServeHTTP(confirmedPage, r)
	if confirmedPage.Code != 200 || !strings.Contains(confirmedPage.Body.String(), "Remove this live table") || !strings.Contains(confirmedPage.Body.String(), "confirm_remove_table") {
		t.Fatal("live removal confirmation missing")
	}
	form = url.Values{"action": {"table_removal_approve"}, "pipeline_id": {liveID}, "revision": {"4"}, "cancellation_id": {removalID}, "csrf_token": {csrf.Value}}
	if out := post(true); out.Code != 400 {
		t.Fatal("live removal without confirmation accepted", out.Code)
	}
	form.Set("confirm_remove_table", "yes")
	if out := post(false); out.Code != 400 {
		t.Fatal("live removal approval without CSRF accepted", out.Code)
	}
	form.Set("cancellation_id", strings.Repeat("b", 64))
	if out := post(true); out.Code != 409 {
		t.Fatal("different live removal identity approved", out.Code)
	}
	form.Set("cancellation_id", removalID)
	if out := post(true); out.Code != 303 {
		t.Fatal("confirmed live removal rejected", out.Code, out.Body.String())
	}
	if out := post(true); out.Code != 409 {
		t.Fatal("stale live removal confirmation accepted", out.Code)
	}
	removing, err := pipelines.Store.Get(ctx, account.ID, liveID)
	if err != nil || removing.SchemaChange.Cancellation.State != "approved" || len(removing.Tables) != 2 || removing.Generation != 1 {
		t.Fatal("live removal changed selection before cleanup", err)
	}
	form = url.Values{"action": {"table_removal_discard"}, "pipeline_id": {liveID}, "revision": {"5"}, "csrf_token": {csrf.Value}}
	if out := post(true); out.Code != 409 {
		t.Fatal("approved live removal dismissed", out.Code)
	}
	// A quarantined existing table uses the same separate recopy confirmation;
	// schema approval, missing confirmation and a stale preview cannot bypass it.
	repairID := uuid.NewString()
	repairPlan := datapipeline.SchemaChange{ID: tablePlanID, State: "verifying", Supported: true, SelectionID: runID, RequestedTables: []string{"customers", "orders"}, Recovery: &datapipeline.TableRecovery{Kind: "live", Table: "customers", State: "detected", Stage: "quarantined", TableOID: 401, SubscriptionOID: 402, PublicationOID: 403}}
	repairJSON, _ := json.Marshal(repairPlan)
	if _, err = pool.Exec(ctx, `INSERT INTO data_pipelines(id,account_id,name,source_connection_id,database_id,tables,read_subjects,state,reason,initialized,schema_change,schema_approval,verification_progress,observed_revision,observed_at) VALUES($1,$2,'live-repair',$3,$4,'["customers","orders"]','["alice"]','blocked','live_table_mismatch',true,$5,$6,$7,1,clock_timestamp())`, repairID, account.ID, source.ID, uuid.NewString(), repairJSON, tablePlanID, failedJSON); err != nil {
		t.Fatal(err)
	}
	form = url.Values{"action": {"table_recovery_preview"}, "pipeline_id": {repairID}, "revision": {"1"}, "copy_table": {"customers"}, "csrf_token": {csrf.Value}}
	if out := post(false); out.Code != 400 {
		t.Fatal("live repair preview without CSRF accepted", out.Code)
	}
	form.Set("copy_table", "orders")
	if out := post(true); out.Code != 409 {
		t.Fatal("healthy table offered repair", out.Code)
	}
	form.Set("copy_table", "customers")
	if out := post(true); out.Code != 303 {
		t.Fatal("live repair preview rejected", out.Code, out.Body.String())
	}
	if out := post(true); out.Code != 409 {
		t.Fatal("stale live repair preview accepted", out.Code)
	}
	repairApproval := strings.Repeat("f", 64)
	repairPlan.Recovery.ID, repairPlan.Recovery.State, repairPlan.Recovery.Stage, repairPlan.Recovery.Attempt = repairApproval, "pending", "preview", 1
	repairJSON, _ = json.Marshal(repairPlan)
	if _, err = pool.Exec(ctx, `UPDATE data_pipelines SET schema_change=$2,reason='table_recovery_pending',observed_revision=revision,observed_at=clock_timestamp() WHERE id=$1`, repairID, repairJSON); err != nil {
		t.Fatal(err)
	}
	form = url.Values{"action": {"schema_approve"}, "pipeline_id": {repairID}, "revision": {"2"}, "change_id": {tablePlanID}, "confirm_schema": {"yes"}, "csrf_token": {csrf.Value}}
	if out := post(true); out.Code != 409 {
		t.Fatal("schema approval bypassed live repair consent", out.Code)
	}
	form = url.Values{"action": {"table_recovery_approve"}, "pipeline_id": {repairID}, "revision": {"2"}, "recovery_id": {repairApproval}, "csrf_token": {csrf.Value}}
	if out := post(true); out.Code != 400 {
		t.Fatal("live repair without confirmation accepted", out.Code)
	}
	form.Set("confirm_recopy", "yes")
	if out := post(false); out.Code != 400 {
		t.Fatal("live repair approval without CSRF accepted", out.Code)
	}
	form.Set("recovery_id", strings.Repeat("e", 64))
	if out := post(true); out.Code != 409 {
		t.Fatal("different live repair approved", out.Code)
	}
	form.Set("recovery_id", repairApproval)
	if out := post(true); out.Code != 303 {
		t.Fatal("confirmed live repair rejected", out.Code, out.Body.String())
	}
	if out := post(true); out.Code != 409 {
		t.Fatal("stale live repair approval accepted", out.Code)
	}
	repairing, err := pipelines.Store.Get(ctx, account.ID, repairID)
	if err != nil || repairing.SchemaChange.Recovery.Kind != "live" || repairing.SchemaChange.Recovery.State != "approved" || repairing.Generation != 1 || len(repairing.Tables) != 2 {
		t.Fatal("repair confirmation changed selected data scope", err)
	}
	// Reader changes share this session/CSRF boundary and remain independent
	// from the in-progress repair. Confirmation approves only the stored diff.
	form = url.Values{"action": {"readers_preview"}, "pipeline_id": {repairID}, "revision": {"3"}, "readers": {"bob"}, "csrf_token": {csrf.Value}}
	if out := post(false); out.Code != 400 {
		t.Fatal("reader preview without CSRF accepted", out.Code)
	}
	if out := post(true); out.Code != 303 {
		t.Fatal("reader preview rejected", out.Code, out.Body.String())
	}
	readerPreview, err := pipelines.Store.Get(ctx, account.ID, repairID)
	if err != nil || readerPreview.ReadSubjects[0] != "alice" || readerPreview.ReaderChange.State != "preview" {
		t.Fatal("preview applied access", err)
	}
	form = url.Values{"action": {"readers_approve"}, "pipeline_id": {repairID}, "revision": {"4"}, "reader_change_id": {readerPreview.ReaderChange.ID}, "csrf_token": {csrf.Value}}
	if out := post(true); out.Code != 400 {
		t.Fatal("reader confirmation omitted", out.Code)
	}
	form.Set("confirm_readers", "yes")
	if out := post(false); out.Code != 400 {
		t.Fatal("reader approval without CSRF accepted", out.Code)
	}
	form.Set("reader_change_id", strings.Repeat("f", 64))
	if out := post(true); out.Code != 409 {
		t.Fatal("different reader preview approved", out.Code)
	}
	form.Set("reader_change_id", readerPreview.ReaderChange.ID)
	if out := post(true); out.Code != 303 {
		t.Fatal("reader approval rejected", out.Code, out.Body.String())
	}
	if out := post(true); out.Code != 409 {
		t.Fatal("stale reader approval accepted", out.Code)
	}
	readerApproved, err := pipelines.Store.Get(ctx, account.ID, repairID)
	if err != nil || readerApproved.ReaderChange.State != "approved" || readerApproved.ReadSubjects[0] != "alice" || readerApproved.SchemaChange.Recovery.State != "approved" {
		t.Fatal("reader approval altered repair or applied early", err)
	}
	form = url.Values{"action": {"rotate"}, "connection_id": {source.ID}, "revision": {"1"}, "connection_url": {"postgres://r:replacement-sentinel@source.example/db?sslmode=verify-full"}, "csrf_token": {csrf.Value}}
	if out := post(true); out.Code != 303 || strings.Contains(out.Body.String(), "replacement-sentinel") {
		t.Fatalf("source rotation: %d", out.Code)
	}
	rotated, err := pipelines.Store.Connection(ctx, account.ID, source.ID)
	if err != nil || rotated.Revision != 2 {
		t.Fatal("browser credential rotation was not saved")
	}

	foreign := uuid.NewString()
	c, err := pipelines.Store.CreateConnection(ctx, datapipeline.Connection{DataConnection: api.DataConnection{ID: uuid.NewString(), Name: "foreign-source", Revision: 1}, AccountID: foreign, Ciphertext: []byte("sealed"), Identity: strings.Repeat("a", 64)})
	if err != nil {
		t.Fatal(err)
	}
	r = httptest.NewRequest("GET", "/dashboard/data?connection="+c.ID, nil)
	r.AddCookie(cookie)
	out = httptest.NewRecorder()
	handler.ServeHTTP(out, r)
	if out.Code != 404 {
		t.Fatalf("foreign metadata: %d", out.Code)
	}
}

func TestDashboardPipelinesConfiguresReadOnlyAppThroughExistingAppService(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	app, err := (*e.Store()).CreateApp(t.Context(), state.App{AccountID: e.Acct().ID, Slug: "pipeline-api", Type: state.AppTypeApp, RequireAuthn: true, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err = (*e.S()).ConfigurePipelineAPIApp(t.Context(), *e.Acct(), app); err != nil {
		t.Fatal(err)
	}
	configured, err := (*e.Store()).AppByID(t.Context(), app.ID)
	if err != nil || configured.RequireAuthn || configured.PublicAuthMode != state.AppPublicAuthModeOpen || len(configured.EgressPorts) != 1 || configured.EgressPorts[0] != 5432 {
		t.Fatalf("JWT app configuration was not applied: %+v %v", configured, err)
	}
	// Like the CLI's Data APIs, the runtime closes its SQL sessions before a snapshot.
	if hook := configured.Manifest.BeforeCheckpoint; hook == nil || hook.Path != api.DataAPIBeforeCheckpointPath {
		t.Fatalf("before-checkpoint hook = %+v", hook)
	}
	if hook := configured.Manifest.AfterRestore; hook == nil || hook.Path != api.DataAPIAfterRestorePath {
		t.Fatalf("after-restore hook = %+v", hook)
	}
	foreign, err := (*e.Store()).CreateAccount(t.Context(), "foreign-pipeline-api@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	if err = (*e.S()).ConfigurePipelineAPIApp(t.Context(), foreign, app); err == nil {
		t.Fatal("foreign account configured the app")
	}
}
