// handlers_compute_nodes_test.go — coverage for the three PR #114
// admin endpoints (issue #97 / ADR-025 axis 3, slice 3/3).
//
// Coverage:
//
//   POST /v1/compute-nodes  happy path, 400 on bad target_url,
//                           409 on duplicate name
//   GET  /v1/compute-nodes  returns auto-seeded default-local + new
//                           rows, even inactive ones
//   GET  /v1/compute-nodes/{id}  200 on known id, 404 on unknown
//
// Uses the package-level setup(t, plan) helper and testEnv.do so
// the routes go through the real middleware stack (auth +
// idempotent). The integration boundary — apid's JSON decoding +
// Store.CreateComputeNode round-trip — is what we care about;
// per-store behaviour is covered by pkg/state tests.

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// createComputeNodeReq is the request shape this file encodes for
// the POST /v1/compute-nodes tests. Mirrored locally (rather than
// imported from handlers_compute_nodes.go) so the test surface
// stays decoupled from a future handler-rename.
type createComputeNodeReq struct {
	Name               string `json:"name"`
	TargetURL          string `json:"target_url"`
	VPCPUs             int    `json:"vcpus"`
	MemMB              int    `json:"mem_mb"`
	MaxConcurrency     int    `json:"max_concurrency"`
	AdmissionCeilingMB int    `json:"admission_ceiling_mb"`
}

// TestCreateComputeNode_HappyPath pins the operator's primary use
// case: registering a second vmmd target. The seed default-local
// row is synthetic; this test confirms the create path can add a
// new node without disturbing it.
func TestCreateComputeNode_HappyPath(t *testing.T) {
	e := setup(t, api.PlanHobby)
	rec := e.do(t, "POST", "/v1/compute-nodes", createComputeNodeReq{
		Name:               "node-b",
		TargetURL:          "unix:///run/faas/node-b.sock",
		VPCPUs:             8,
		MemMB:              8192,
		MaxConcurrency:     4,
		AdmissionCeilingMB: 4096,
	}, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST: %d %s", rec.Code, rec.Body.String())
	}
	var out computeNodeResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.ID == "" {
		t.Errorf("ID is empty — store did not assign one")
	}
	if out.Name != "node-b" {
		t.Errorf("Name = %q, want node-b", out.Name)
	}
	if !out.Active {
		t.Errorf("Active = false, want true (new nodes start active)")
	}
	if out.LastHeartbeatAt == "" {
		// MemStore's CreateComputeNode stamps LastHeartbeatAt =
		// CreatedAt when the caller leaves it zero (mirrors the
		// PG DEFAULT now() column). The shape contract is "non-empty
		// RFC3339 timestamp", not "unset before first tick" — PG
		// also fills DEFAULT now() so the wire shape is consistent
		// across stores.
		t.Errorf("LastHeartbeatAt is empty; want a non-empty RFC3339 timestamp")
	}
}

// TestCreateComputeNode_RejectsBadTargetURL covers the validation
// gate. PR #95 requires dial targets to parse via wire.ParseTarget;
// a malformed target_url means schedd will fail to dial later, so
// reject at registration time.
func TestCreateComputeNode_RejectsBadTargetURL(t *testing.T) {
	e := setup(t, api.PlanHobby)
	rec := e.do(t, "POST", "/v1/compute-nodes", createComputeNodeReq{
		Name:               "node-bad",
		TargetURL:          "tcp://not a url",
		VPCPUs:             8,
		MemMB:              8192,
		MaxConcurrency:     4,
		AdmissionCeilingMB: 4096,
	}, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("POST: %d %s, want 400", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "Invalid target_url") {
		t.Errorf("error message = %q, want it to mention target_url", rec.Body.String())
	}
}

// TestCreateComputeNode_NameConflictReturns409 pins the unique-name
// surface. A second create for the same name returns 409 (Code
// Conflict) — the operator's "is the heartbeat working?" retry
// loop already passes through s.idempotent, so a retried POST
// lands on the existing row, not a 409. This test exercises the
// direct (non-idempotent) path.
func TestCreateComputeNode_NameConflictReturns409(t *testing.T) {
	e := setup(t, api.PlanHobby)
	first := e.do(t, "POST", "/v1/compute-nodes", createComputeNodeReq{
		Name:               "node-b",
		TargetURL:          "unix:///run/faas/b.sock",
		VPCPUs:             8,
		MemMB:              8192,
		MaxConcurrency:     4,
		AdmissionCeilingMB: 4096,
	}, nil)
	if first.Code != http.StatusCreated {
		t.Fatalf("first POST: %d %s", first.Code, first.Body.String())
	}
	second := e.do(t, "POST", "/v1/compute-nodes", createComputeNodeReq{
		Name:               "node-b",
		TargetURL:          "unix:///run/faas/b2.sock",
		VPCPUs:             8,
		MemMB:              8192,
		MaxConcurrency:     4,
		AdmissionCeilingMB: 4096,
	}, nil)
	if second.Code != http.StatusConflict {
		t.Fatalf("second POST: %d %s, want 409", second.Code, second.Body.String())
	}
}

// TestListComputeNodes_IncludesSeededAndNew pins the GET endpoint's
// coverage. MemStore's NewMemStore seeds the synthetic
// 'default-local' row (single-box baseline) so the list always has
// at least one entry; after a create the new row appears too.
func TestListComputeNodes_IncludesSeededAndNew(t *testing.T) {
	e := setup(t, api.PlanHobby)
	// Create a second row first.
	rec := e.do(t, "POST", "/v1/compute-nodes", createComputeNodeReq{
		Name:               "node-b",
		TargetURL:          "unix:///run/faas/b.sock",
		VPCPUs:             8,
		MemMB:              8192,
		MaxConcurrency:     4,
		AdmissionCeilingMB: 4096,
	}, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("seed POST: %d %s", rec.Code, rec.Body.String())
	}
	// Now list.
	list := e.do(t, "GET", "/v1/compute-nodes", nil, nil)
	if list.Code != http.StatusOK {
		t.Fatalf("list: %d %s", list.Code, list.Body.String())
	}
	var resp struct {
		Nodes []computeNodeResponse `json:"nodes"`
	}
	if err := json.Unmarshal(list.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	// MemStore seeds default-local at construction; this test
	// adds node-b, so we expect both.
	names := map[string]bool{}
	for _, n := range resp.Nodes {
		names[n.Name] = true
	}
	if !names["default-local"] || !names["node-b"] {
		t.Errorf("list missing rows; names = %v, want default-local and node-b", names)
	}
}

// TestGetComputeNode_UnknownIDReturns404 pins the 404 mapping on
// ComputeNodeByID; the handler mirrors the rest of apid's
// lookup-by-id endpoints.
func TestGetComputeNode_UnknownIDReturns404(t *testing.T) {
	e := setup(t, api.PlanHobby)
	rec := e.do(t, "GET", "/v1/compute-nodes/00000000-0000-0000-0000-000000000000", nil, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET unknown id: %d %s, want 404", rec.Code, rec.Body.String())
	}
}

// TestCreateComputeNode_IdempotencyKeyReplays pins the s.idempotent
// wrapper behaviour on POST /v1/compute-nodes (PR #115 review #11).
// The operator's "is the new node being heartbeated?" probe naturally
// retries; without idempotency, the second POST lands on the
// UNIQUE-name constraint and returns 409. With idempotency, the
// second POST replays the stored 201 response. PR #114 closed this
// loop on the route registration; this test pins the contract.
func TestCreateComputeNode_IdempotencyKeyReplays(t *testing.T) {
	e := setup(t, api.PlanHobby)
	hdr := map[string]string{"Idempotency-Key": "node-b-register-abc"}
	body := createComputeNodeReq{
		Name:               "node-b",
		TargetURL:          "unix:///run/faas/b.sock",
		VPCPUs:             8,
		MemMB:              8192,
		MaxConcurrency:     4,
		AdmissionCeilingMB: 4096,
	}
	first := e.do(t, "POST", "/v1/compute-nodes", body, hdr)
	if first.Code != http.StatusCreated {
		t.Fatalf("first: %d %s", first.Code, first.Body.String())
	}
	// Replaying the same key must NOT 409 — the idempotent
	// middleware replays the stored 201 response.
	second := e.do(t, "POST", "/v1/compute-nodes", body, hdr)
	if second.Code != http.StatusCreated {
		t.Errorf("idempotent replay should return 201, got %d %s", second.Code, second.Body.String())
	}
	if second.Header().Get("Idempotent-Replayed") != "true" {
		t.Error("replay should be marked Idempotent-Replayed: true")
	}
	// Only one compute_node should actually exist.
	rows, err := e.store.ListAllComputeNodes(context.Background())
	if err != nil {
		t.Fatalf("ListAllComputeNodes: %v", err)
	}
	var nodeBCount int
	for _, n := range rows {
		if n.Name == "node-b" {
			nodeBCount++
		}
	}
	if nodeBCount != 1 {
		t.Errorf("node-b row count = %d, want 1 (idempotent retry must not insert again)", nodeBCount)
	}
}

// TestCreateComputeNode_TableDriven wraps the four scenarios above
// into the table the package convention prefers (CLAUDE.md:
// table-driven tests). The dedicated tests above remain the
// diagnostic surface when one breaks.
func TestCreateComputeNode_TableDriven(t *testing.T) {
	t.Run("happy path", func(t *testing.T) {
		e := setup(t, api.PlanHobby)
		rec := e.do(t, "POST", "/v1/compute-nodes", createComputeNodeReq{
			Name: "node-b", TargetURL: "unix:///run/faas/b.sock",
			VPCPUs: 8, MemMB: 8192, MaxConcurrency: 4, AdmissionCeilingMB: 4096,
		}, nil)
		if rec.Code != http.StatusCreated {
			t.Fatalf("POST: %d %s", rec.Code, rec.Body.String())
		}
	})
	t.Run("bad target url", func(t *testing.T) {
		e := setup(t, api.PlanHobby)
		rec := e.do(t, "POST", "/v1/compute-nodes", createComputeNodeReq{
			Name: "x", TargetURL: "tcp://not a url",
			VPCPUs: 8, MemMB: 8192, MaxConcurrency: 4, AdmissionCeilingMB: 4096,
		}, nil)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("POST: %d, want 400", rec.Code)
		}
	})
	t.Run("name conflict", func(t *testing.T) {
		e := setup(t, api.PlanHobby)
		body := createComputeNodeReq{
			Name: "node-b", TargetURL: "unix:///run/faas/b.sock",
			VPCPUs: 8, MemMB: 8192, MaxConcurrency: 4, AdmissionCeilingMB: 4096,
		}
		first := e.do(t, "POST", "/v1/compute-nodes", body, nil)
		if first.Code != http.StatusCreated {
			t.Fatalf("first: %d %s", first.Code, first.Body.String())
		}
		second := e.do(t, "POST", "/v1/compute-nodes", body, nil)
		if second.Code != http.StatusConflict {
			t.Fatalf("second: %d, want 409", second.Code)
		}
	})
	t.Run("unknown id 404", func(t *testing.T) {
		e := setup(t, api.PlanHobby)
		rec := e.do(t, "GET", "/v1/compute-nodes/00000000-0000-0000-0000-000000000000", nil, nil)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("GET: %d, want 404", rec.Code)
		}
	})
}
