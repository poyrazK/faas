package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	repairapi "github.com/onebox-faas/faas/pkg/apid/datarepair"
	"github.com/onebox-faas/faas/pkg/datarepair"
)

func TestDataRepairHistoryCLIReadOnlyAndScoped(t *testing.T) {
	scope := datarepair.Scope{AccountID: uuid.NewString(), AppID: uuid.NewString(), Environment: "production", PlatformTenantID: uuid.NewString()}
	source, id, digest := uuid.NewString(), uuid.NewString(), strings.Repeat("a", 64)
	page := datarepair.HistoryPage{Scope: scope, SourceOperationID: source, CheckedAt: time.Now(), Items: []datarepair.HistoryItem{{ID: id, Digest: digest, CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour), Candidates: 459, Conflicts: 41}}}
	var calls int
	var gotPath, gotToken, gotCustomer, gotCursor string
	malformed := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != http.MethodGet || r.ContentLength > 0 || r.Header.Get("Idempotency-Key") != "" {
			t.Error("history attempted a mutation")
		}
		gotPath, gotToken, gotCustomer, gotCursor = r.URL.Path, r.Header.Get("Authorization"), r.URL.Query().Get("customer"), r.URL.Query().Get("cursor")
		result := page
		if malformed {
			result.SourceOperationID = uuid.NewString()
		}
		_ = json.NewEncoder(w).Encode(result)
	}))
	defer server.Close()
	t.Setenv("GREGALE_REPAIR_TOKEN", "executor-reader")
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "hosted-reader")
	var out bytes.Buffer
	old := osStdout
	osStdout = &out
	defer func() { osStdout = old }()
	if cmdDataRepair([]string{"history", source, "--url", server.URL, "--limit", "1"}) != 0 || gotPath != "/v1/data-repair/sources/"+source+"/history" || gotToken != "Bearer executor-reader" {
		t.Fatal("standalone history lost its fixed origin or authority")
	}
	var got datarepair.HistoryPage
	if json.Unmarshal(out.Bytes(), &got) != nil || !got.Valid(1) || got.Items[0].Digest != digest {
		t.Fatal("history lost plan identity/digest")
	}
	out.Reset()
	if cmdDataRepair([]string{"history", source, "--app", "products", "--environment", scope.Environment, "--customer", scope.PlatformTenantID, "--limit", "1", "--cursor", "opaque+/?&"}) != 0 ||
		gotPath != "/v1/apps/products/data-repair/sources/"+source+"/history" || gotToken != "Bearer hosted-reader" || gotCustomer != scope.PlatformTenantID || gotCursor != "opaque+/?&" {
		t.Fatal("hosted history changed filters, cursor or authority")
	}
	before := calls
	for _, args := range [][]string{
		{"history", "invalid"}, {"history", source, "--limit", "0"}, {"history", source, "--limit", "101"},
		{"history", source, "--customer", scope.PlatformTenantID}, {"history", source, "--app", "products", "--url", server.URL},
		{"apply", id, "--app", "products", "--source", source, "--digest", digest, "--yes"},
		{"get", id, "--app", "products", "--environment", scope.Environment},
	} {
		if cmdDataRepair(args) == 0 {
			t.Fatal("invalid history query or write shortcut accepted", args)
		}
	}
	if calls != before {
		t.Fatal("invalid history request contacted an API")
	}
	malformed = true
	out.Reset()
	if cmdDataRepair([]string{"history", source, "--url", server.URL}) == 0 || out.Len() != 0 {
		t.Fatal("history accepted a different source identity")
	}
}

func TestDataRepairHistoryRetainedCLIGetAndVerify(t *testing.T) {
	scope := datarepair.Scope{AccountID: uuid.NewString(), AppID: uuid.NewString(), Environment: "production"}
	source, id, repair, digest := uuid.NewString(), uuid.NewString(), uuid.NewString(), strings.Repeat("a", 64)
	checked := time.Now()
	var calls int
	report := datarepair.VerificationReport{PlanID: id, PlanDigest: digest, Scope: scope, SourceOperationID: source, RepairOperationID: repair,
		AppliedAt: checked, CheckedAt: checked, Restored: 459, Excluded: 41, Available: true, Checked: 459, Unchanged: 459, Rows: []datarepair.ValidationRow{}, Issues: []string{}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		base := "/v1/apps/products/data-repair/sources/" + source + "/plans/" + id
		if r.Header.Get("Authorization") != "Bearer history-reader" || r.Header.Get("Idempotency-Key") != "" || !r.URL.Query().Has("customer") || r.URL.Query().Get("customer") != "" {
			t.Error("retained read used incorrect authority or scope")
		}
		if r.Method == "GET" && r.URL.Path == base {
			_ = json.NewEncoder(w).Encode(repairapi.HistoricalPlanResponse{Plan: datarepair.Plan{ID: id, Digest: digest, SourceOperationID: source, Scope: scope}, Execution: &datarepair.HistoryExecution{Availability: "not_retained"}})
			return
		}
		var body map[string]any
		if r.Method != "POST" || r.URL.Path != base+"/verify" || json.NewDecoder(r.Body).Decode(&body) != nil || len(body) != 1 || body["digest"] != digest {
			t.Error("retained verification supplied mutation authority")
		}
		_ = json.NewEncoder(w).Encode(report)
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "history-reader")
	var out bytes.Buffer
	old := osStdout
	osStdout = &out
	defer func() { osStdout = old }()
	for _, verb := range []string{"get", "verify"} {
		args := []string{verb, id, "--app", "products", "--source", source, "--environment", scope.Environment, "--customer", ""}
		if verb == "verify" {
			args = append(args, "--digest", digest)
		}
		out.Reset()
		if cmdDataRepair(args) != 0 {
			t.Fatal("cannot recover retained plan/receipt", verb)
		}
	}
	if calls != 2 {
		t.Fatal("retained reads added retries or execution-ledger dependencies", calls)
	}
	report.Available, report.Checked, report.Unchanged, report.Issues = false, 0, 0, []string{"journal_purged"}
	out.Reset()
	args := []string{"verify", id, "--app", "products", "--source", source, "--customer", "", "--digest", digest}
	if cmdDataRepair(args) == 0 || !strings.Contains(out.String(), "journal_purged") {
		t.Fatal("retained verification hid unavailable journal evidence")
	}
	report.SourceOperationID = uuid.NewString()
	out.Reset()
	if cmdDataRepair(args) == 0 || out.Len() != 0 {
		t.Fatal("verification crossed discovered source")
	}
}
