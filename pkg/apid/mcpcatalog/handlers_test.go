package mcpcatalog

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/mcphosting"
	"github.com/onebox-faas/faas/pkg/state"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type testLoader struct{ store state.Store }

func (l testLoader) LoadApp(w http.ResponseWriter, r *http.Request, account state.Account, slug string) (state.App, bool) {
	app, err := l.store.AppBySlug(r.Context(), slug)
	if err != nil || app.AccountID != account.ID {
		respond(w, nil, state.ErrNotFound)
		return app, false
	}
	return app, true
}
func TestCatalogMonitorMemory(t *testing.T) { testCatalogMonitor(t, state.NewMemStore()) }
func TestCatalogMonitorPostgres(t *testing.T) {
	testCatalogMonitor(t, state.NewPgStore(pgtest.OpenMigrated(t)))
}
func testCatalogMonitor(t *testing.T, store state.Store) {
	ctx := t.Context()
	account, err := store.CreateAccount(ctx, "catalog@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "catalog", Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1, IdleTimeoutS: 30, Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := mcphosting.NewCatalogContract(mcphosting.ProtocolVersion, mcphosting.Catalog{Capabilities: []string{"tools", "resources", "prompts"}, Tools: []mcphosting.Tool{{Name: "reviewed", InputSchema: map[string]any{"type": "object"}}}, Resources: []mcphosting.Resource{{URI: "test://resource", Name: "resource"}}, Prompts: []mcphosting.Prompt{{Name: "reviewed"}}})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := mcphosting.MarshalContract(baseline)
	call := func(method string, body any, query string, acct state.Account) *httptest.ResponseRecorder {
		data, _ := json.Marshal(body)
		r := httptest.NewRequest(method, "/v1/apps/catalog/mcp-catalog-monitor"+query, bytes.NewReader(data))
		r.SetPathValue("slug", app.Slug)
		w := httptest.NewRecorder()
		Handler(store, testLoader{store})(w, r, acct)
		return w
	}
	configured := call("PUT", api.MCPCatalogMonitorConfigRequest{Enabled: true, Baseline: raw, FreshnessSeconds: 300}, "", account)
	if configured.Code != 200 {
		t.Fatalf("configure %s", configured.Body.String())
	}
	var snapshot api.MCPCatalogMonitorSnapshot
	_ = json.Unmarshal(configured.Body.Bytes(), &snapshot)
	revision := snapshot.Config.Revision
	post := func(observed []byte, unavailable bool) api.MCPCatalogReceipt {
		w := call("POST", api.MCPCatalogObservationRequest{Revision: revision, Observed: observed, Unavailable: unavailable}, "", account)
		if w.Code != 200 {
			t.Fatalf("observation %d %s", w.Code, w.Body.String())
		}
		var receipt api.MCPCatalogReceipt
		_ = json.Unmarshal(w.Body.Bytes(), &receipt)
		return receipt
	}
	healthy := post(raw, false)
	if healthy.State != "healthy" || healthy.ChangedCount != 0 {
		t.Fatalf("healthy %+v", healthy)
	}
	repeat := post(raw, false)
	if repeat.ID != healthy.ID {
		t.Fatal("duplicate observation retained")
	}
	baseline.Tools = []mcphosting.Tool{}
	baseline.Resources = nil
	baseline.Prompts = nil
	driftRaw, _ := mcphosting.MarshalContract(baseline)
	drift := post(driftRaw, false)
	if drift.State != "drift" || drift.ChangedCount < 3 {
		t.Fatalf("all catalogs not monitored: %+v", drift)
	}
	recovered := post(raw, false)
	if recovered.State != "healthy" || recovered.ID == healthy.ID {
		t.Fatal("recovery evidence absent")
	}
	failed := post(nil, true)
	if failed.State != "unavailable" {
		t.Fatal("failed probe became healthy")
	}
	w := call("GET", nil, "", account)
	_ = json.Unmarshal(w.Body.Bytes(), &snapshot)
	if len(snapshot.History) != 4 || snapshot.State != "unavailable" {
		t.Fatalf("snapshot %+v", snapshot)
	}
	if snapshot.CheckedAt == nil || snapshot.CheckedAt.Before(snapshot.Latest.CapturedAt) {
		t.Fatal("heartbeat missing")
	}
	if api.CatalogMonitorSignal(snapshot, "catalog", snapshot.CheckedAt.Add(301*time.Second)).State != "unavailable" {
		t.Fatal("stale evidence became healthy")
	}
	w = call("GET", nil, "?evidence="+drift.ID, account)
	if w.Code != 200 || !bytes.Contains(w.Body.Bytes(), []byte(`"state":"drift"`)) {
		t.Fatal("exact retained diff unavailable")
	}
	other := account
	other.ID = uuid.NewString()
	if w = call("GET", nil, "?evidence="+drift.ID, other); w.Code != 404 {
		t.Fatal("cross-account evidence exposed")
	}
	source := store.(state.MCPCatalogMonitorStore)
	if _, err = source.GetMCPCatalogMonitor(ctx, other.ID, app.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("storage ownership unbound")
	}
	w = call("PUT", api.MCPCatalogMonitorConfigRequest{Enabled: true, Baseline: raw, FreshnessSeconds: 300}, "", account)
	var next api.MCPCatalogMonitorSnapshot
	_ = json.Unmarshal(w.Body.Bytes(), &next)
	w = call("POST", api.MCPCatalogObservationRequest{Revision: revision, Observed: driftRaw}, "", account)
	if w.Code != 409 {
		t.Fatal("old monitor cleared new configuration")
	}
	revision = next.Config.Revision
	// Exercise bounded persistence using distinct observed digests through the store.
	for i := 0; i < 40; i++ {
		_, err := source.RecordMCPCatalogObservation(ctx, account.ID, app.ID, api.MCPCatalogReceipt{ID: uuid.NewString(), Revision: revision, State: "drift", ObservedSHA256: uuid.NewString(), ChangedCount: 1})
		if err != nil {
			t.Fatal(err)
		}
	}
	snapshot, err = source.GetMCPCatalogMonitor(ctx, account.ID, app.ID)
	if err != nil || len(snapshot.History) != api.MCPCatalogMonitorHistoryLimit {
		t.Fatal("history unbounded")
	}
	w = call("PUT", api.MCPCatalogMonitorConfigRequest{Enabled: false, Baseline: raw, FreshnessSeconds: 300}, "", account)
	if w.Code != 200 {
		t.Fatal("disable failed")
	}
	w = call("GET", nil, "", account)
	_ = json.Unmarshal(w.Body.Bytes(), &next)
	if next.State != "disabled" {
		t.Fatal("disabled monitor still active")
	}
}

func TestCatalogMonitorRetainsTruncatedDiff(t *testing.T) {
	tools := make([]mcphosting.Tool, 200)
	for i := range tools {
		tools[i] = mcphosting.Tool{Name: fmt.Sprintf("tool_%03d", i), InputSchema: map[string]any{"type": "object"}}
	}
	baseline, err := mcphosting.NewCatalogContract(mcphosting.ProtocolVersion, mcphosting.Catalog{Capabilities: []string{"tools"}, Tools: tools})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := mcphosting.MarshalContract(baseline)
	observed, _ := mcphosting.NewCatalogContract(mcphosting.ProtocolVersion, mcphosting.Catalog{Capabilities: []string{"tools"}, Tools: []mcphosting.Tool{}})
	current, _ := mcphosting.MarshalContract(observed)
	receipt, problem := evaluateObservation(api.MCPCatalogMonitorConfig{Revision: uuid.NewString(), Baseline: raw, BaselineSHA256: digest(raw)}, api.MCPCatalogObservationRequest{Observed: current})
	if problem != nil || receipt.State != "drift" || !receipt.Truncated || receipt.ChangedCount < 200 || len(receipt.Diff) > api.MCPCatalogMonitorMaxDiffBytes {
		t.Fatalf("large diff: %+v %v", receipt, problem)
	}
	var diff mcphosting.ContractDiff
	_ = json.Unmarshal(receipt.Diff, &diff)
	if len(diff.Changes) != api.MCPCatalogMonitorMaxChanges {
		t.Fatal("diff truncation lost full count or boundedness")
	}
}

func TestCatalogMonitorRolesMemory(t *testing.T) { testCatalogMonitorRoles(t, state.NewMemStore()) }
func TestCatalogMonitorRolesPostgres(t *testing.T) {
	testCatalogMonitorRoles(t, state.NewPgStore(pgtest.OpenMigrated(t)))
}
func testCatalogMonitorRoles(t *testing.T, store state.Store) {
	ctx := t.Context()
	account, err := store.CreateAccount(ctx, "roles@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "roles", Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1, IdleTimeoutS: 30, Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	baseline, _ := mcphosting.NewCatalogContract(mcphosting.ProtocolVersion, mcphosting.Catalog{Capabilities: []string{"tools"}, Tools: []mcphosting.Tool{{Name: "reviewed", InputSchema: map[string]any{"type": "object"}}}})
	raw, _ := mcphosting.MarshalContract(baseline)
	call := func(method, query string, body any) *httptest.ResponseRecorder {
		data, _ := json.Marshal(body)
		r := httptest.NewRequest(method, "/v1/apps/roles/mcp-catalog-monitor"+query, bytes.NewReader(data))
		r.SetPathValue("slug", app.Slug)
		w := httptest.NewRecorder()
		Handler(store, testLoader{store})(w, r, account)
		return w
	}
	setup := func(role string) api.MCPCatalogMonitorSnapshot {
		w := call("PUT", "", api.MCPCatalogMonitorConfigRequest{Role: role, TokenEnv: "MCP_" + role, Enabled: true, Baseline: raw, FreshnessSeconds: 300})
		if w.Code != 200 {
			t.Fatalf("setup %s: %d %s", role, w.Code, w.Body)
		}
		var out api.MCPCatalogMonitorSnapshot
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		return out
	}
	reader := setup("reader")
	admin := setup("admin")
	if w := call("GET", "", nil); w.Code != 404 {
		t.Fatal("named-only app manufactured default monitor")
	}
	post := func(role, revision string, observed []byte, unavailable bool) api.MCPCatalogReceipt {
		w := call("POST", "", api.MCPCatalogObservationRequest{Role: role, Revision: revision, Observed: observed, Unavailable: unavailable})
		if w.Code != 200 {
			t.Fatalf("report: %d %s", w.Code, w.Body)
		}
		var out api.MCPCatalogReceipt
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		return out
	}
	healthy := post("reader", reader.Config.Revision, raw, false)
	if w := call("POST", "", api.MCPCatalogObservationRequest{Role: "admin", Revision: reader.Config.Revision, Observed: raw}); w.Code != 409 {
		t.Fatal("cross-role revision accepted")
	}
	baseline.Tools = []mcphosting.Tool{}
	driftRaw, _ := mcphosting.MarshalContract(baseline)
	drift := post("admin", admin.Config.Revision, driftRaw, false)
	if drift.Role != "admin" || healthy.Role != "reader" {
		t.Fatal("receipt role missing")
	}
	summary := func() api.MCPCatalogMonitorSummary {
		w := call("GET", "?all_roles=true", nil)
		if w.Code != 200 {
			t.Fatalf("summary %d %s", w.Code, w.Body)
		}
		var out api.MCPCatalogMonitorSummary
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		return out
	}
	if got := summary(); got.State != "drift" || len(got.Roles) != 2 || got.Roles["reader"].Latest.ID != healthy.ID {
		t.Fatal("healthy role hid admin drift")
	}
	if w := call("GET", "?role=reader&evidence="+drift.ID, nil); w.Code != 404 {
		t.Fatal("receipt leaked across roles")
	}
	if w := call("GET", "?role=admin&evidence="+drift.ID, nil); w.Code != 200 {
		t.Fatal("role receipt unavailable")
	}
	admin = setup("admin")
	if got := summary(); got.State != "unavailable" || got.Roles["reader"].Latest.ID != healthy.ID || len(got.Roles["admin"].History) != 1 {
		t.Fatal("reconfiguration changed another role or lost history")
	}
	if w := call("POST", "", api.MCPCatalogObservationRequest{Role: "admin", Revision: drift.Revision, Observed: raw}); w.Code != 409 {
		t.Fatal("superseded revision accepted")
	}
	recovered := post("admin", admin.Config.Revision, raw, false)
	if got := summary(); got.State != "healthy" || recovered.ID == drift.ID {
		t.Fatal("role recovery missing")
	}
	post("admin", admin.Config.Revision, nil, true)
	if summary().State != "unavailable" {
		t.Fatal("healthy reader hid admin outage")
	}
	w := call("PUT", "", api.MCPCatalogMonitorConfigRequest{Role: "admin", Enabled: false, Baseline: raw, FreshnessSeconds: 300})
	if w.Code != 200 || summary().State != "healthy" {
		t.Fatal("disable did not exclude role")
	}
	setup("default")
	for i := 0; i < api.MCPCatalogMonitorRoleLimit-3; i++ {
		setup(fmt.Sprintf("role%d", i))
	}
	if w := call("PUT", "", api.MCPCatalogMonitorConfigRequest{Role: "overflow", Enabled: true, Baseline: raw, FreshnessSeconds: 300}); w.Code != 400 {
		t.Fatalf("role bound %d %s", w.Code, w.Body)
	}
	if w := call("PUT", "", api.MCPCatalogMonitorConfigRequest{Role: "../invalid", Enabled: true, Baseline: raw}); w.Code != 400 {
		t.Fatal("invalid role accepted")
	}
	if w := call("GET", "?all_roles=true&role=reader", nil); w.Code != 400 {
		t.Fatal("ambiguous summary accepted")
	}
}

func TestCatalogMonitorConcurrentRolesMemory(t *testing.T) {
	testCatalogMonitorConcurrentRoles(t, state.NewMemStore())
}
func TestCatalogMonitorConcurrentRolesPostgres(t *testing.T) {
	testCatalogMonitorConcurrentRoles(t, state.NewPgStore(pgtest.OpenMigrated(t)))
}
func testCatalogMonitorConcurrentRoles(t *testing.T, store state.Store) {
	ctx := t.Context()
	account, err := store.CreateAccount(ctx, "concurrent-roles@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "concurrent-roles", Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1, IdleTimeoutS: 30, Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	source := store.(state.MCPCatalogMonitorStore)
	revisions := map[string]string{}
	for _, role := range []string{"reader", "admin"} {
		revision := uuid.NewString()
		revisions[role] = revision
		_, err = source.ConfigureMCPCatalogMonitor(ctx, account.ID, app.ID, api.MCPCatalogMonitorConfig{Role: role, Enabled: true, Revision: revision, FreshnessSeconds: 300})
		if err != nil {
			t.Fatal(err)
		}
	}
	done := make(chan error, 32)
	for role, revision := range revisions {
		for n := 0; n < 16; n++ {
			go func() {
				_, err := source.RecordMCPCatalogObservation(ctx, account.ID, app.ID, api.MCPCatalogReceipt{Role: role, ID: uuid.NewString(), Revision: revision, State: "healthy", ObservedSHA256: fmt.Sprintf("%s-%d", role, n)})
				done <- err
			}()
		}
	}
	for n := 0; n < 32; n++ {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	out, err := source.GetMCPCatalogMonitor(ctx, account.ID, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	for role := range revisions {
		view := out.Roles[role]
		if len(view.History) != 16 {
			t.Fatalf("%s lost concurrent receipts: %d", role, len(view.History))
		}
		for _, receipt := range view.History {
			if receipt.Role != role {
				t.Fatal("concurrent receipt stored in wrong role")
			}
		}
	}
}
