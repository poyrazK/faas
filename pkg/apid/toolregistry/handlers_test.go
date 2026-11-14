package toolregistry_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/toolregistry"
	"github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/mcphosting"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/toolpublicationdb"
)

func TestToolRegistryMemory(t *testing.T) {
	store := state.NewMemStore()
	exerciseRegistry(t, store, func() state.Store { return store })
}

func TestToolRegistryPostgres(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	exerciseRegistry(t, state.NewPgStore(pool), func() state.Store { return state.NewPgStore(pool) })
}

func exerciseRegistry(t *testing.T, store state.Store, reconnect func() state.Store) {
	t.Helper()
	ctx := t.Context()
	owner, err := store.CreateAccount(ctx, "registry-owner@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	other, err := store.CreateAccount(ctx, "registry-other@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: owner.ID, Slug: "csv-tools", Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1, IdleTimeoutS: 30, Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindTarball, Status: state.DeployLive, TrafficPercent: 100, SourceSHA256: strings.Repeat("d", 64), SourceBytes: 100, SourcePath: "/fixture/source.tar.gz"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateDeploymentStatus(ctx, dep.ID, state.DeployLive, ""); err != nil {
		t.Fatal(err)
	}
	id := strings.Repeat("a", 64)
	in := api.ToolPublicationIntent{App: app.Slug, AppID: app.ID, SourceSHA256: strings.Repeat("b", 64), FixturesSHA256: strings.Repeat("c", 64), AccessMode: "open", ToolScopes: map[string][]string{"csv_profile": {}}, TestCases: []string{"profiles reviewed sample"}}
	call := func(method, path string, body any, account state.Account, key *state.APIKey) *httptest.ResponseRecorder {
		data, _ := json.Marshal(body)
		r := httptest.NewRequest(method, path, bytes.NewReader(data))
		r.SetPathValue("id", id)
		r = r.WithContext(middleware.WithPrincipal(r.Context(), account, key, nil))
		w := httptest.NewRecorder()
		if strings.HasPrefix(path, "/v1/tool-registry") {
			toolregistry.Search(store)(w, r, account)
		} else {
			toolregistry.Handler(store)(w, r, account)
		}
		return w
	}
	started := call("PUT", "/v1/tool-publications/"+id, in, owner, nil)
	if started.Code != 200 {
		t.Fatalf("begin: %d %s", started.Code, started.Body.String())
	}
	var begin api.BeginToolPublicationResponse
	_ = json.Unmarshal(started.Body.Bytes(), &begin)
	if !begin.Created || begin.Record.Status != "running" {
		t.Fatalf("begin: %+v", begin)
	}
	if strings.Contains(started.Body.String(), "source.tar.gz") || started.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("private journal leaked source path or cache policy")
	}
	// A newly constructed store uses the same durable database, with no
	// management-server memory, filesystem paths or process identity.
	store = reconnect()
	again := call("PUT", "/v1/tool-publications/"+id, in, owner, nil)
	_ = json.Unmarshal(again.Body.Bytes(), &begin)
	if again.Code != 200 || begin.Created {
		t.Fatal("reconnect authorized a second execution")
	}
	changed := in
	changed.SourceSHA256 = strings.Repeat("e", 64)
	if w := call("PUT", "/v1/tool-publications/"+id, changed, owner, nil); w.Code != 409 {
		t.Fatalf("changed review: %d %s", w.Code, w.Body.String())
	}
	if w := call("GET", "/v1/tool-publications/"+id, nil, other, nil); w.Code != 404 {
		t.Fatalf("cross-account read: %d", w.Code)
	}
	for _, key := range []*state.APIKey{{AppID: app.ID}, {ProjectLimited: true}, {PlatformTenantID: "tenant"}, {OrgID: "foreign-org"}, {HostingSiteID: "site"}, {Scopes: []string{api.ScopeAgent, api.ScopeAppsRead}}} {
		if w := call("GET", "/v1/tool-publications/"+id, nil, owner, key); w.Code != 403 {
			t.Fatalf("scoped read: %d", w.Code)
		}
	}
	for _, key := range []*state.APIKey{
		{Scopes: []string{api.ScopeAgent, api.ScopeAppsRead, api.ScopeDeployWrite}},
		{Label: api.MCPLoginKeyLabel, Scopes: []string{api.ScopeAppsRead, api.ScopeDeployWrite}},
	} {
		if w := call("GET", "/v1/tool-publications/"+id, nil, owner, key); w.Code != 200 {
			t.Fatalf("publisher receipt: %d %s", w.Code, w.Body.String())
		}
		w := call("PUT", "/v1/tool-publications/"+id, in, owner, key)
		var duplicate api.BeginToolPublicationResponse
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &duplicate) != nil || duplicate.Created {
			t.Fatalf("publisher replay: %d %s", w.Code, w.Body.String())
		}
	}
	if w := call("GET", "/v1/tool-registry?org=team", nil, owner, nil); w.Code != 403 {
		t.Fatal("organization hint escaped boundary")
	}
	if w := call("GET", "/v1/tool-registry?limit=0", nil, owner, nil); w.Code != 400 {
		t.Fatal("unbounded registry page accepted")
	}
	catalog, err := mcphosting.NewCatalogContract(mcphosting.ProtocolVersion, mcphosting.Catalog{Tools: []mcphosting.Tool{{Name: "csv_profile", Description: "Profile a CSV", InputSchema: map[string]any{"type": "object"}}}})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := mcphosting.MarshalContract(catalog)
	outcome := api.ToolPublicationOutcome{Status: "completed", DeploymentID: dep.ID, CatalogSHA256: strings.Repeat("f", 64), Catalog: raw, ProtocolVerified: true, Checks: []api.ToolPublicationCheck{{Name: in.TestCases[0], Status: "passed"}}}
	bad := outcome
	bad.Checks = []api.ToolPublicationCheck{{Name: "unreviewed", Status: "passed"}}
	if w := call("POST", "/v1/tool-publications/"+id+"/outcome", bad, owner, nil); w.Code != 400 {
		t.Fatal("unreviewed tests registered")
	}
	bad = outcome
	bad.Catalog = json.RawMessage(`{"version":2,"protocol_version":"2026-07-28","tools":[{"name":"other"}]}`)
	if w := call("POST", "/v1/tool-publications/"+id+"/outcome", bad, owner, nil); w.Code != 400 {
		t.Fatal("unreviewed tool registered")
	}
	foreignApp, err := store.CreateApp(ctx, state.App{AccountID: other.ID, Slug: "foreign-tool", Type: state.AppTypeApp, RAMMB: 256})
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := store.CreateDeployment(ctx, state.Deployment{AppID: foreignApp.ID, Kind: state.DeploymentKindTarball, Status: state.DeployLive, SourceSHA256: strings.Repeat("d", 64)})
	if err != nil {
		t.Fatal(err)
	}
	bad = outcome
	bad.DeploymentID = foreign.ID
	if w := call("POST", "/v1/tool-publications/"+id+"/outcome", bad, owner, nil); w.Code != 404 {
		t.Fatal("foreign deployment registered")
	}
	finished := call("POST", "/v1/tool-publications/"+id+"/outcome", outcome, owner, nil)
	if finished.Code != 200 {
		t.Fatalf("finish: %d %s", finished.Code, finished.Body.String())
	}
	var record api.ToolPublicationRecord
	_ = json.Unmarshal(finished.Body.Bytes(), &record)
	if record.DeploymentSourceSHA256 != dep.SourceSHA256 || record.EvidenceOrigin != "publisher_attested" || record.FinishedAt == nil {
		t.Fatal("native source identity or evidence origin lost")
	}
	if !bytes.Equal(record.Outcome.Catalog, raw) {
		t.Fatal("database changed canonical catalog bytes")
	}
	if w := call("POST", "/v1/tool-publications/"+id+"/outcome", outcome, owner, nil); w.Code != 200 {
		t.Fatalf("repeat finish: %d %s", w.Code, w.Body.String())
	}
	bad = outcome
	bad.CatalogSHA256 = strings.Repeat("0", 64)
	if w := call("POST", "/v1/tool-publications/"+id+"/outcome", bad, owner, nil); w.Code != 409 {
		t.Fatal("completed evidence overwritten")
	}
	page := call("GET", "/v1/tool-registry?query=CSV_PROFILE&limit=1", nil, owner, nil)
	if page.Code != 200 || !strings.Contains(page.Body.String(), id) {
		t.Fatalf("discovery: %d %s", page.Code, page.Body.String())
	}
	if strings.Contains(page.Body.String(), "catalog_base64") {
		t.Fatal("search returned full catalog bodies")
	}
	if w := call("GET", "/v1/tool-registry", nil, other, nil); strings.Contains(w.Body.String(), id) {
		t.Fatal("cross-account discovery leaked version")
	}
	// A failed update remains a tombstone and leaves the old version visible.
	id = strings.Repeat("b", 64)
	if w := call("PUT", "/v1/tool-publications/"+id, in, owner, nil); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if w := call("POST", "/v1/tool-publications/"+id+"/outcome", api.ToolPublicationOutcome{Status: "failed"}, owner, nil); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	again = call("PUT", "/v1/tool-publications/"+id, in, owner, nil)
	_ = json.Unmarshal(again.Body.Bytes(), &begin)
	if again.Code != 200 || begin.Created || begin.Record.Status != "failed" {
		t.Fatal("failed publication authorized retry")
	}
	page = call("GET", "/v1/tool-registry", nil, owner, nil)
	var versions api.ToolRegistryPage
	if json.Unmarshal(page.Body.Bytes(), &versions) != nil || len(versions.Versions) != 1 || versions.Versions[0].Version != strings.Repeat("a", 64) {
		t.Fatal("failed update corrupted registry")
	}
	// Concurrent begins elect one publisher, including across pg connections.
	id = strings.Repeat("c", 64)
	journal := store.(state.ToolPublicationStore)
	var created atomic.Int32
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			result, err := journal.BeginToolPublication(ctx, owner.ID, id, in)
			if err != nil {
				t.Error(err)
				return
			}
			if result.Created {
				created.Add(1)
			}
		})
	}
	wg.Wait()
	if created.Load() != 1 {
		t.Fatalf("concurrent publishers: %d", created.Load())
	}
	for i := 0; i < api.ToolPublicationsPerAccount-3; i++ {
		if _, err := journal.BeginToolPublication(ctx, owner.ID, fmt.Sprintf("%064x", i+256), in); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := journal.BeginToolPublication(ctx, owner.ID, strings.Repeat("d", 64), in); !errors.Is(err, state.ErrToolPublicationLimit) {
		t.Fatalf("journal limit: %v", err)
	}
	if result, err := journal.BeginToolPublication(ctx, owner.ID, id, in); err != nil || result.Created {
		t.Fatal("journal limit stopped receipt recovery")
	}
}

func TestToolRegistryPostgresAbandonedAttempt(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	store := state.NewPgStore(pool)
	account, err := store.CreateAccount(t.Context(), "abandoned-tool@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	var accountID pgtype.UUID
	if err = accountID.Scan(account.ID); err != nil {
		t.Fatal(err)
	}
	in := api.ToolPublicationIntent{App: "abandoned-tool", SourceSHA256: strings.Repeat("a", 64), FixturesSHA256: strings.Repeat("b", 64), AccessMode: "open", ToolScopes: map[string][]string{"add": {}}, TestCases: []string{"adds"}}
	record := api.ToolPublicationRecord{ID: strings.Repeat("e", 64), AccountID: account.ID, Intent: in, Status: "running", CreatedAt: time.Now().Add(-time.Hour), Deadline: time.Now().Add(-time.Minute), EvidenceOrigin: "publisher_attested"}
	raw, _ := json.Marshal(record)
	if _, err = toolpublicationdb.New().InsertToolPublication(t.Context(), pool, toolpublicationdb.InsertToolPublicationParams{AccountID: accountID, ID: record.ID, Payload: raw}); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetToolPublication(t.Context(), account.ID, record.ID)
	if err != nil || got.Status != "outcome_unknown" {
		t.Fatalf("abandoned: %+v %v", got, err)
	}
	begin, err := store.BeginToolPublication(t.Context(), account.ID, record.ID, in)
	if err != nil || begin.Created || begin.Record.Status != "outcome_unknown" {
		t.Fatal("abandoned work became executable")
	}
	if _, err = store.FinishToolPublication(t.Context(), account.ID, record.ID, api.ToolPublicationOutcome{Status: "outcome_unknown"}, ""); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("expired worker finalized: %v", err)
	}
}
