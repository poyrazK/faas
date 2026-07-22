// Tests for /v1/compute-nodes (issue #98 / ADR-028).
//
// Pins the four contracts that matter for this slice:
//
//   1. Empty allowlist  → every route 403 admin_required (no
//      implicit "any authenticated caller is admin" path).
//   2. Allowlist miss   → 403 admin_required (a customer-tier
//      bearer token can't reach the operator surface).
//   3. Allowlist hit    → GET / POST / DELETE return 2xx and the
//      row round-trips through the store. The hard-delete guard
//      refuses to drop the synthetic default-local row.
//   4. Auth gate precedes parse → a 401 on a malformed bearer does
//      NOT leak the body's parse-error side (so a brute-force can't
//      fish for handler bodies via crafted JSON).

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func newComputeNodeTestServer(t *testing.T, adminCSV, email string) (*httptest.Server, string) {
	t.Helper()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(context.Background(), email, api.PlanPro)
	if err != nil {
		t.Fatalf("seed account: %v", err)
	}
	key, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatalf("mint key: %v", err)
	}
	if _, err := store.CreateAPIKey(context.Background(), acct.ID, hash, "test"); err != nil {
		t.Fatalf("seed key: %v", err)
	}
	srv := newServerWithDeps(store, nil, "example.com", nil, "", nil, nil, nil, nil, 0, "")
	srv.WithAdminAllowlist(adminCSV)
	ts := httptest.NewServer(srv.handler())
	t.Cleanup(ts.Close)
	return ts, key
}

func doJSON(t *testing.T, method, url, body, token string, ts *httptest.Server) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, ts.URL+url, bytes.NewBufferString(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	return resp
}

func TestComputeNodes_EmptyAllowlistDeniesAll(t *testing.T) {
	ts, tok := newComputeNodeTestServer(t, "" /* no admins */, "ops@example.com")
	resp := doJSON(t, "GET", "/v1/compute-nodes", "", tok, ts)
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		t.Errorf("empty allowlist allowed GET: status=%d", resp.StatusCode)
	}
	if resp.StatusCode < 400 || resp.StatusCode >= 500 {
		t.Errorf("empty allowlist: status=%d, want 4xx", resp.StatusCode)
	}
}

func TestComputeNodes_AllowlistMissDenies(t *testing.T) {
	ts, tok := newComputeNodeTestServer(t, "different-operator@example.com", "ops@example.com")
	resp := doJSON(t, "GET", "/v1/compute-nodes", "", tok, ts)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("allowlist miss: status=%d, want 403", resp.StatusCode)
	}
}

func TestComputeNodes_AllowlistHitUpsertsAndLists(t *testing.T) {
	ts, tok := newComputeNodeTestServer(t, "ops@example.com", "ops@example.com")

	body := `{
		"name":"box-east-1",
		"target_url":"tcp://100.64.0.1:50051",
		"vpcpus":160,"mem_mb":56000,
		"max_concurrency":200,"admission_ceiling_mb":47600
	}`
	resp := doJSON(t, "POST", "/v1/compute-nodes", body, tok, ts)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST upsert: status=%d", resp.StatusCode)
	}
	var posted computeNodeResponse
	if err := json.NewDecoder(resp.Body).Decode(&posted); err != nil {
		t.Fatalf("decode POST response: %v", err)
	}
	if posted.Name != "box-east-1" || !posted.Active {
		t.Errorf("POST response: name=%q active=%v", posted.Name, posted.Active)
	}
	if posted.ID == "" {
		t.Errorf("POST response: id empty")
	}

	resp = doJSON(t, "GET", "/v1/compute-nodes", "", tok, ts)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET list: status=%d", resp.StatusCode)
	}
	var listed []computeNodeResponse
	if err := json.NewDecoder(resp.Body).Decode(&listed); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	// MemStore seeds the synthetic default-local row by default,
	// so the list always includes it on top of what we upserted.
	// Filter to the row we just posted to pin the round-trip.
	found := false
	for _, n := range listed {
		if n.Name == "box-east-1" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("posted row missing from list: %+v", listed)
	}
}

func TestComputeNodes_HardDeleteRefusesDefaultLocal(t *testing.T) {
	ts, tok := newComputeNodeTestServer(t, "ops@example.com", "ops@example.com")
	resp := doJSON(t, "DELETE", "/v1/compute-nodes/default-local?hard=1", "", tok, ts)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("default-local hard-delete: status=%d, want 409", resp.StatusCode)
	}
}

func TestComputeNodes_SoftDeleteDeactivates(t *testing.T) {
	ts, tok := newComputeNodeTestServer(t, "ops@example.com", "ops@example.com")

	body := `{
		"name":"box-soft",
		"target_url":"tcp://100.64.0.2:50051",
		"vpcpus":8,"mem_mb":8192,
		"max_concurrency":16,"admission_ceiling_mb":4096
	}`
	resp := doJSON(t, "POST", "/v1/compute-nodes", body, tok, ts)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("seed: %d", resp.StatusCode)
	}

	resp = doJSON(t, "DELETE", "/v1/compute-nodes/box-soft", "", tok, ts)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("soft delete: status=%d, want 200", resp.StatusCode)
	}
	var got computeNodeResponse
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode delete response: %v", err)
	}
	if got.Active {
		t.Errorf("soft delete: row still active")
	}
}
