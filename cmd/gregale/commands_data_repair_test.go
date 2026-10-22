package main_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
	repairapi "github.com/onebox-faas/faas/pkg/apid/datarepair"
	"github.com/onebox-faas/faas/pkg/datarepair"
)

func TestDataRepairRetentionCLIRequiresReviewedBatch(t *testing.T) {
	// Authorization errors must fail locally, even when the configured database
	// would be unreachable. The real PostgreSQL journey checks the allowed path.
	t.Setenv("GREGALE_REPAIR_DATABASE_URL", "postgres://127.0.0.1:1/unreachable")
	id, digest := uuid.NewString(), strings.Repeat("a", 64)
	for _, args := range [][]string{
		{"purge", id, "--digest", digest, "--actor", "operator"},
		{"purge", id, "--digest", digest, "--yes"},
		{"purge", id, "--digest", "bad", "--actor", "operator", "--yes"},
		{"purge", id, "--digest", digest, "--actor", strings.Repeat("x", api.DataRepairActorMaxBytes+1), "--yes"},
		{"purge", id, "--digest", strings.ToUpper(digest), "--actor", "operator", "--yes"},
		{"purge", id, "--digest", digest, "--actor", "operator", "--yes", "--before", "2020-01-01T00:00:00Z"},
		{"plan", "--limit", "0"},
		{"get", id, "--yes"},
	} {
		if main.CmdDataRepair(append([]string{"retention"}, args...)) == 0 {
			t.Fatalf("unreviewed or ambiguous purge accepted: %v", args)
		}
	}
	path := filepath.Join(t.TempDir(), "scope.json")
	scope := datarepair.Scope{AccountID: uuid.NewString(), AppID: uuid.NewString(), Environment: "production"}
	raw, err := json.Marshal(scope)
	if err != nil {
		t.Fatal(err)
	}
	for _, content := range []string{string(raw[:len(raw)-1]) + `,"unexpected":true}`, string(raw) + `{}`, string(raw) + strings.Repeat(" ", api.DataRepairMaxRequestBytes)} {
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := main.DataRepairRetentionScope(path); err == nil {
			t.Fatal("ambiguous or oversized maintenance scope accepted")
		}
	}
	var out bytes.Buffer
	oldOut := (*main.OsStdout)
	(*main.OsStdout) = &out
	defer func() { (*main.OsStdout) = oldOut }()
	if main.CmdDataRepair([]string{"retention", "schema"}) != 0 || out.String() != datarepair.RetentionSchema() {
		t.Fatal("owner upgrade was not emitted without connecting")
	}
	out.Reset()
	if main.CmdDataRepair([]string{"schema", "--upgrade"}) != 0 || out.String() != datarepair.UpgradeSchema() {
		t.Fatal("contract upgrade was not emitted without connecting")
	}
}

// ADR-20261016: commands never submit approval/apply without explicit review
// intent and a fixed plan digest. Executor tokens never follow redirects.
func TestDataRepairCLIApprovalAndTransport(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "Bearer "+strings.Repeat("x", 32) {
			t.Error("missing executor token")
		}
		var in map[string]string
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			t.Error(err)
		}
		if in["digest"] != strings.Repeat("a", 64) {
			t.Error("plan digest changed")
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(datarepair.Plan{ID: uuid.NewString(), Digest: in["digest"]})
	}))
	defer server.Close()
	t.Setenv("GREGALE_REPAIR_TOKEN", strings.Repeat("x", 32))
	id := uuid.NewString()
	digest := strings.Repeat("a", 64)
	for _, verb := range []string{"approve", "apply"} {
		if status := main.CmdDataRepair([]string{verb, id, "--url", server.URL, "--digest", digest}); status == 0 {
			t.Fatalf("unconfirmed %s submitted", verb)
		}
	}
	if calls != 0 {
		t.Fatal("invalid commands contacted executor")
	}
	if status := main.CmdDataRepair([]string{"approve", id, "--url", server.URL, "--digest", digest, "--yes"}); status != 0 || calls != 1 {
		t.Fatalf("approved request: %d %d", status, calls)
	}
	for _, base := range []string{"http://external.example", "https://user:password@example.com", "https://example.com?token=bad", "file:///tmp/repair"} {
		if _, err := main.RepairEndpoint(base); err == nil {
			t.Fatalf("unsafe URL accepted: %s", base)
		}
	}
}

func TestDataRepairManagedCLIIdentityAndApproval(t *testing.T) {
	id, source, digest := uuid.NewString(), uuid.NewString(), strings.Repeat("a", 64)
	calls, posts := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "Bearer managed-test-token" {
			t.Error("normal login was not used")
		}
		if r.Method == "POST" {
			posts++
			if strings.HasSuffix(r.URL.Path, "/approvals/repair") {
				if r.URL.Path != "/v1/apps/products/operations/"+id+"/approvals/repair" {
					t.Error("approval left its account/app namespace")
				}
				var in api.OperationApprovalRequest
				if json.NewDecoder(r.Body).Decode(&in) != nil || in.Decision != "approved" || in.ExpectedGeneration != 1 {
					t.Error("approval contract changed")
				}
				_ = json.NewEncoder(w).Encode(api.OperationResponse{ID: id})
				return
			}
			if r.Header.Get("Idempotency-Key") != id {
				t.Error("prepare lost its saved identity")
			}
			var in map[string]string
			if json.NewDecoder(r.Body).Decode(&in) != nil || in["source_operation_id"] != source {
				t.Error("source identity changed")
			}
		}
		_ = json.NewEncoder(w).Encode(repairapi.ManagedResponse{Operation: api.OperationResponse{ID: id, Generation: 1, LatestSequence: 4}, Plan: datarepair.Plan{ID: id, Digest: digest}, ApprovalURL: "https://untrusted.invalid/approval"})
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "managed-test-token")
	for _, args := range [][]string{{"approve", id, "--digest", digest, "--app", "products"}, {"apply", id, "--digest", digest, "--app", "products"}, {"plan", source, "--app", "products"}} {
		if main.CmdDataRepair(args) == 0 {
			t.Fatal("unconfirmed/unreserved managed request accepted")
		}
	}
	if calls != 0 {
		t.Fatal("invalid command contacted management API")
	}
	if main.CmdDataRepair([]string{"plan", source, "--app", "products", "--repair-operation", id}) != 0 {
		t.Fatal("reserved prepare failed")
	}
	if main.CmdDataRepair([]string{"approve", id, "--app", "products", "--digest", digest, "--yes"}) != 0 {
		t.Fatal("reviewed managed approval failed")
	}
	if posts != 2 || calls != 3 {
		t.Fatalf("requests: calls=%d mutations=%d", calls, posts)
	}
}
