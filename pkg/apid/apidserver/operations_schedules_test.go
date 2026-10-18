// adr: 20261018161833
package apidserver_test

// Operation schedules: hidden until the account is allowlisted, scoped to one
// customer, fired by apid under the same admission rules as a direct start.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

type scheduleRoutesFixture struct {
	t        *testing.T
	store    *state.MemStore
	srv      *apidserver.Server
	handler  http.Handler
	acct     state.Account
	key      string
	def      api.OperationDefinitionResponse
	tenants  []state.PlatformTenant
	tokens   []string // one manage+read token per tenant
	accounts string   // the allowlist value that opens schedules to acct
}

func newScheduleRoutesFixture(t *testing.T) *scheduleRoutesFixture {
	t.Helper()
	t.Setenv("FAAS_SCAN_SPOOL_ROOT", t.TempDir())
	t.Setenv("FAAS_OPERATION_SCHEDULES_ACCOUNTS", "")
	ctx := context.Background()
	f := &scheduleRoutesFixture{t: t, store: state.NewMemStore()}
	var err error
	if f.acct, err = f.store.CreateAccount(ctx, "operation-schedules@example.com", api.PlanPro); err != nil {
		t.Fatal(err)
	}
	key, hash, _ := api.GenerateAPIKey()
	if _, err := f.store.CreateAPIKey(ctx, f.acct.ID, hash, "operation-schedules", api.ScopesAdminOnly); err != nil {
		t.Fatal(err)
	}
	f.key, f.accounts = key, "other-account, "+f.acct.ID
	f.srv = apidserver.NewServer(f.store, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", apidserver.NoopNotifier{})
	(*f.srv.Operation()).AdmissionEnabled = true
	f.handler = f.srv.Handler()
	app, err := f.store.CreateApp(ctx, state.App{AccountID: f.acct.ID, Slug: "reports", Type: state.AppTypeApp})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := f.store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.MarkDeploymentLive(ctx, dep.ID); err != nil {
		t.Fatal(err)
	}
	spec := api.OperationDefinitionSpec{Name: "export", Method: "POST", Path: "/exports", Owner: api.OperationOwnerPlatformTenant,
		InputSchema:  json.RawMessage(`{"type":"object","properties":{"format":{"type":"string"}},"additionalProperties":false}`),
		OutputSchema: json.RawMessage(`{"type":"object"}`), ProgressStages: []string{"generating"}}
	f.decode(f.do("PUT", "/v1/apps/reports/deployments/"+dep.ID+"/operation-definitions/export", f.key, spec), http.StatusOK, &f.def)
	for _, ref := range []string{"alice", "bob"} {
		tenant, _, err := f.store.CreatePlatformTenant(ctx, f.acct.ID, ref, ref, 100)
		if err != nil {
			t.Fatal(err)
		}
		var token api.CreatePlatformTenantAccessTokenResponse
		f.decode(f.do("POST", "/v1/account/platform-tenants/"+tenant.ID+"/access-tokens", f.key, api.CreatePlatformTenantAccessTokenRequest{Name: "browser",
			Scopes: []string{api.ScopePlatformTenantOperationsRead, api.ScopePlatformTenantOperationsManage}}), http.StatusCreated, &token)
		f.tenants, f.tokens = append(f.tenants, tenant), append(f.tokens, token.Token)
	}
	return f
}

func (f *scheduleRoutesFixture) do(method, path, bearer string, body any) *httptest.ResponseRecorder {
	f.t.Helper()
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(mustJSON(f.t, body))
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Authorization", "Bearer "+bearer)
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	return rec
}

func (f *scheduleRoutesFixture) decode(rec *httptest.ResponseRecorder, status int, out any) {
	f.t.Helper()
	if rec.Code != status {
		f.t.Fatalf("HTTP %d, want %d: %s", rec.Code, status, rec.Body.String())
	}
	if out != nil {
		if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
			f.t.Fatal(err)
		}
	}
}

func (f *scheduleRoutesFixture) fire(at time.Time) {
	f.t.Helper()
	if _, err := f.srv.Operation().FireDueOperationSchedules(context.Background(), at); err != nil {
		f.t.Fatal(err)
	}
}

const selfSchedules = "/v1/platform-tenant-self/customer-operation-schedules"

func TestOperationScheduleRoutesAreHiddenUntilAllowlisted(t *testing.T) {
	f := newScheduleRoutesFixture(t)
	at := time.Now().Add(time.Hour)
	create := api.OperationScheduleRequest{DefinitionID: f.def.ID, At: &at}
	for _, probe := range []struct {
		method, path, bearer string
		body                 any
	}{
		{"POST", selfSchedules, f.tokens[0], create},
		{"GET", selfSchedules, f.tokens[0], nil},
		{"GET", selfSchedules + "/00000000-0000-0000-0000-000000000000", f.tokens[0], nil},
		{"POST", selfSchedules + "/00000000-0000-0000-0000-000000000000/pause", f.tokens[0], nil},
		{"GET", "/v1/apps/reports/operation-schedules", f.key, nil},
		{"POST", "/v1/apps/reports/operation-schedules/00000000-0000-0000-0000-000000000000/cancel", f.key, nil},
	} {
		rec := f.do(probe.method, probe.path, probe.bearer, probe.body)
		if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "no such API route") {
			t.Fatalf("%s %s before the allowlist: HTTP %d %s", probe.method, probe.path, rec.Code, rec.Body.String())
		}
	}
	t.Setenv("FAAS_OPERATION_SCHEDULES_ACCOUNTS", f.accounts)
	f.decode(f.do("POST", selfSchedules, f.tokens[0], create), http.StatusCreated, nil)
}

func TestOperationScheduleRoutesScopeAndFire(t *testing.T) {
	f := newScheduleRoutesFixture(t)
	t.Setenv("FAAS_OPERATION_SCHEDULES_ACCOUNTS", f.accounts)
	alice, bob := f.tokens[0], f.tokens[1]
	at := time.Now().Add(time.Hour)

	var once, cron api.OperationScheduleResponse
	f.decode(f.do("POST", selfSchedules, alice, api.OperationScheduleRequest{DefinitionID: f.def.ID, At: &at, Input: json.RawMessage(`{"format":"csv"}`)}), http.StatusCreated, &once)
	f.decode(f.do("POST", selfSchedules, alice, api.OperationScheduleRequest{DefinitionID: f.def.ID, Cron: "0 * * * *", Timezone: "Europe/Istanbul"}), http.StatusCreated, &cron)
	if once.PlatformTenantID != f.tenants[0].ID || once.State != api.OperationScheduleActive || cron.NextFireAt == nil {
		t.Fatalf("created once=%+v cron=%+v", once, cron)
	}
	for _, bad := range []api.OperationScheduleRequest{
		{DefinitionID: f.def.ID, Cron: "* * * * *", Timezone: "Mars/Olympus"},
		{DefinitionID: f.def.ID, At: &at, Input: json.RawMessage(`{"format":1}`)},
	} {
		f.decode(f.do("POST", selfSchedules, alice, bad), http.StatusUnprocessableEntity, nil)
	}

	var list api.OperationScheduleListResponse
	f.decode(f.do("GET", selfSchedules, bob, nil), http.StatusOK, &list)
	if len(list.Schedules) != 0 {
		t.Fatalf("bob listed alice's schedules: %+v", list)
	}
	f.decode(f.do("GET", selfSchedules+"/"+cron.ID, bob, nil), http.StatusNotFound, nil)
	f.decode(f.do("POST", selfSchedules+"/"+cron.ID+"/cancel", bob, nil), http.StatusNotFound, nil)

	var paused api.OperationScheduleResponse
	f.decode(f.do("POST", selfSchedules+"/"+cron.ID+"/pause", alice, nil), http.StatusOK, &paused)
	f.decode(f.do("POST", selfSchedules+"/"+once.ID+"/pause", alice, nil), http.StatusConflict, nil)
	if paused.State != api.OperationSchedulePaused || paused.NextFireAt != nil {
		t.Fatalf("paused = %+v", paused)
	}
	f.decode(f.do("POST", selfSchedules+"/"+cron.ID+"/resume", alice, nil), http.StatusOK, nil)

	// Two hours on, both are due: the once schedule admits its Operation.
	f.fire(time.Now().Add(2 * time.Hour))
	var fired api.OperationScheduleResponse
	f.decode(f.do("GET", selfSchedules+"/"+once.ID, alice, nil), http.StatusOK, &fired)
	if fired.State != api.OperationScheduleCompleted || fired.Fires != 1 || fired.LastOperationID == "" || fired.LastError != "" {
		t.Fatalf("fired once = %+v", fired)
	}
	var op api.OperationResponse
	f.decode(f.do("GET", "/v1/platform-tenant-self/customer-operations/"+fired.LastOperationID, alice, nil), http.StatusOK, &op)
	f.decode(f.do("GET", "/v1/platform-tenant-self/customer-operations/"+fired.LastOperationID, bob, nil), http.StatusNotFound, nil)

	// The account cancels the cron schedule; its admitted Operation remains.
	var accountList api.OperationScheduleListResponse
	f.decode(f.do("GET", "/v1/apps/reports/operation-schedules", f.key, nil), http.StatusOK, &accountList)
	if len(accountList.Schedules) != 2 {
		t.Fatalf("account list = %+v", accountList)
	}
	var cancelled api.OperationScheduleResponse
	f.decode(f.do("POST", "/v1/apps/reports/operation-schedules/"+cron.ID+"/cancel", f.key, nil), http.StatusOK, &cancelled)
	if cancelled.State != api.OperationScheduleCancelled || cancelled.Fires != 1 {
		t.Fatalf("cancelled = %+v", cancelled)
	}
}

func TestOperationScheduleFiresRecordApidRefusals(t *testing.T) {
	f := newScheduleRoutesFixture(t)
	t.Setenv("FAAS_OPERATION_SCHEDULES_ACCOUNTS", f.accounts)
	alice := f.tokens[0]
	var cron api.OperationScheduleResponse
	f.decode(f.do("POST", selfSchedules, alice, api.OperationScheduleRequest{DefinitionID: f.def.ID, Cron: "* * * * *"}), http.StatusCreated, &cron)
	read := func() api.OperationScheduleResponse {
		t.Helper()
		var out api.OperationScheduleResponse
		f.decode(f.do("GET", selfSchedules+"/"+cron.ID, alice, nil), http.StatusOK, &out)
		return out
	}

	(*f.srv.Operation()).AdmissionEnabled = false
	f.fire(time.Now().Add(2 * time.Minute))
	if got := read(); got.Fires != 0 || got.State != api.OperationScheduleActive || !strings.Contains(got.LastError, "admission is disabled") {
		t.Fatalf("fire while admission is closed = %+v", got)
	}
	(*f.srv.Operation()).AdmissionEnabled = true

	if _, err := f.store.SetPlatformTenantStatus(context.Background(), f.acct.ID, f.tenants[0].ID, state.PlatformTenantSuspended); err != nil {
		t.Fatal(err)
	}
	f.fire(time.Now().Add(4 * time.Minute))
	f.decode(f.do("GET", selfSchedules+"/"+cron.ID, alice, nil), http.StatusForbidden, nil)
	if _, err := f.store.SetPlatformTenantStatus(context.Background(), f.acct.ID, f.tenants[0].ID, state.PlatformTenantActive); err != nil {
		t.Fatal(err)
	}
	if got := read(); got.Fires != 0 || got.LastError != "customer is suspended" {
		t.Fatalf("fire while suspended = %+v", got)
	}

	f.fire(time.Now().Add(6 * time.Minute))
	if got := read(); got.Fires != 1 || got.LastError != "" || got.LastOperationID == "" {
		t.Fatalf("fire once reopened = %+v", got)
	}

	// Removing the account from the allowlist hides the routes and refuses fires.
	t.Setenv("FAAS_OPERATION_SCHEDULES_ACCOUNTS", "")
	f.fire(time.Now().Add(8 * time.Minute))
	t.Setenv("FAAS_OPERATION_SCHEDULES_ACCOUNTS", f.accounts)
	if got := read(); got.Fires != 1 || !strings.Contains(got.LastError, "not enabled") {
		t.Fatalf("fire after the allowlist closed = %+v", got)
	}
}
