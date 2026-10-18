package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	repairapi "github.com/onebox-faas/faas/pkg/apid/datarepair"
	"github.com/onebox-faas/faas/pkg/datarepair"
)

func TestDataRepairSourcesCLIReadOnlyAndScoped(t *testing.T) {
	scope := datarepair.Scope{AccountID: uuid.NewString(), AppID: uuid.NewString(), Environment: "production", PlatformTenantID: uuid.NewString()}
	id := uuid.NewString()
	var calls int
	var gotPath, gotToken, gotRelease, gotCustomer, gotSince string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != http.MethodGet || r.ContentLength > 0 || r.Header.Get("Idempotency-Key") != "" {
			t.Error("discovery attempted a mutation")
		}
		gotPath, gotToken = r.URL.Path, r.Header.Get("Authorization")
		gotRelease, gotCustomer, gotSince = r.URL.Query().Get("release"), r.URL.Query().Get("customer"), r.URL.Query().Get("since")
		if r.URL.Path == "/v1/apps/products/data-repair/source-scopes" {
			_ = json.NewEncoder(w).Encode(repairapi.SourceScopesResponse{Scopes: []datarepair.Scope{scope}})
			return
		}
		_ = json.NewEncoder(w).Encode(datarepair.SourcePage{Scope: scope, Items: []datarepair.Source{{ID: id}}})
	}))
	defer server.Close()
	t.Setenv("REPAIR_DISCOVERY_TEST_TOKEN", "executor-token")
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "account-token")
	var out bytes.Buffer
	previous := osStdout
	osStdout = &out
	defer func() { osStdout = previous }()
	if cmdDataRepair([]string{"sources", "--url", server.URL, "--token-env", "REPAIR_DISCOVERY_TEST_TOKEN", "--release", "bad & percent%", "--since", "2026-10-17T03:00:00+03:00"}) != 0 {
		t.Fatal("executor discovery failed")
	}
	if gotPath != "/v1/data-repair/sources" || gotToken != "Bearer executor-token" || gotRelease != "bad & percent%" || gotSince != "2026-10-17T00:00:00Z" {
		t.Fatal("executor scope, filters or credentials changed in transport")
	}
	var page datarepair.SourcePage
	if json.Unmarshal(out.Bytes(), &page) != nil || len(page.Items) != 1 || page.Items[0].ID != id {
		t.Fatal("source identity lost from CLI output")
	}
	out.Reset()
	if cmdDataRepair([]string{"sources", "--app", "products", "--environment", "production", "--customer", scope.PlatformTenantID}) != 0 {
		t.Fatal("hosted discovery failed")
	}
	if gotPath != "/v1/apps/products/data-repair/sources" || gotToken != "Bearer account-token" || gotCustomer != scope.PlatformTenantID {
		t.Fatal("hosted discovery used executor authority or lost its configured scope")
	}
	out.Reset()
	if cmdDataRepair([]string{"sources", "--app", "products", "--scopes"}) != 0 || gotPath != "/v1/apps/products/data-repair/source-scopes" {
		t.Fatal("configured scope discovery failed")
	}
	before := calls
	for _, args := range [][]string{
		{"--limit", "0"}, {"--since", "2026-10-17T00:00:00Z", "--before", "2026-10-17T00:00:00Z"},
		{"--customer", scope.PlatformTenantID}, {"--app", "products", "--url", server.URL},
	} {
		if cmdDataRepair(append([]string{"sources"}, args...)) == 0 {
			t.Fatalf("ambiguous source query accepted: %v", args)
		}
	}
	if calls != before {
		t.Fatal("invalid source query contacted an API")
	}
}
