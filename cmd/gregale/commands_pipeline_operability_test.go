package main_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func pipelineOperabilityCLI(t *testing.T, h http.HandlerFunc) {
	t.Helper()
	main.ResetJSONOut(t)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "owner-token")
	s := httptest.NewServer(h)
	t.Cleanup(s.Close)
	t.Setenv("FAAS_API", s.URL)
}
func TestPipelineDurablePlanDoesNotAllocate(t *testing.T) {
	calls := 0
	source := uuid.NewString()
	pipelineOperabilityCLI(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method+" "+r.URL.Path != "POST /v1/data/pipeline-setups/plan" {
			t.Error("plan reached a resource allocator", r.Method, r.URL.Path)
			http.Error(w, "unexpected", 500)
			return
		}
		var req api.DataPipelineSetupRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		if req.SourceConnectionID != source || req.FreshnessTargetSeconds != 30 {
			t.Error("lost setup selections", req)
		}
		main.WriteJSONTest(w, api.DataPipelineSetupPlan{Name: req.Name, CreatesMirror: true, BillingNotes: []string{"No resources created."}})
	})
	if code := main.Run([]string{"--json", "pipeline", "setup", "plan", "customer-orders", "--connection", source, "--tables", "orders", "--read-subjects", "alice", "--freshness-target", "30s"}); code != 0 {
		t.Fatal(code)
	}
	if calls != 1 {
		t.Fatal("plan made unexpected requests", calls)
	}
}
func TestPipelineDurableAmbiguousReplyHasNoLegacyFallback(t *testing.T) {
	calls := 0
	var key string
	source := uuid.NewString()
	pipelineOperabilityCLI(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method+" "+r.URL.Path != "POST /v1/data/pipeline-setups" {
			t.Error("ambiguous submission fell back to allocation", r.Method, r.URL.Path)
		}
		if k := r.Header.Get("Idempotency-Key"); k == "" || key != "" && k != key {
			t.Error("missing or unstable request identity", k)
		} else {
			key = k
		}
		http.Error(w, "reply lost after acceptance", 503)
	})
	args := []string{"--json", "pipeline", "create", "customer-orders", "--durable", "--connection", source, "--tables", "orders", "--read-subjects", "alice"}
	for range 2 {
		if code := main.Run(args); code == 0 {
			t.Fatal("ambiguous acceptance reported success")
		}
	}
	if calls != 2 {
		t.Fatal("unexpected recovery mutations", calls)
	}
}
func TestPipelineSetupRetryUsesSavedRevisionAndID(t *testing.T) {
	id, db := uuid.NewString(), uuid.NewString()
	mutations := 0
	pipelineOperabilityCLI(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /v1/data/pipeline-setups/" + id:
			main.WriteJSONTest(w, api.DataPipelineSetup{ID: id, DatabaseID: db, Revision: 7, State: "blocked", Recovery: "Retry the saved setup."})
		case "POST /v1/data/pipeline-setups/" + id + "/control":
			var req api.DataPipelineSetupControlRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatal(err)
			}
			if req.ExpectedRevision != 7 || req.Action != "retry" {
				t.Error(req)
			}
			mutations++
			main.WriteJSONTest(w, api.DataPipelineSetup{ID: id, DatabaseID: db, Revision: 8, State: "working"})
		default:
			t.Error("unexpected allocation", r.Method, r.URL.Path)
			http.Error(w, "unexpected", 500)
		}
	})
	if code := main.Run([]string{"--json", "pipeline", "setup", "retry", id}); code != 0 {
		t.Fatal(code)
	}
	if mutations != 1 {
		t.Fatal(mutations)
	}
}
func TestPipelineFreshnessInvalidTargetIsLocal(t *testing.T) {
	calls := 0
	pipelineOperabilityCLI(t, func(w http.ResponseWriter, r *http.Request) { calls++; http.Error(w, "unexpected", 500) })
	if code := main.Run([]string{"--json", "pipeline", "freshness", uuid.NewString(), "--target", "14s"}); code == 0 {
		t.Fatal("invalid target accepted")
	}
	if calls != 0 {
		t.Fatal("invalid target reached remote", calls)
	}
}
func TestPipelineIndexApprovalSendsOnlyFrozenID(t *testing.T) {
	id := uuid.NewString()
	mutations := 0
	pipelineOperabilityCLI(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /v1/data/pipelines/" + id:
			main.WriteJSONTest(w, api.DataPipeline{ID: id, Revision: 9})
		case "POST /v1/data/pipelines/" + id + "/indexes":
			var req api.DataPipelineIndexRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatal(err)
			}
			if req.ExpectedRevision != 9 || req.Action != "approve" || req.AdviceID != "frozen-advice" || req.Table != "" || len(req.Filters) != 0 {
				t.Error("approval replaced the preview", req)
			}
			mutations++
			main.WriteJSONTest(w, api.DataPipeline{ID: id, Revision: 10})
		default:
			t.Error("unexpected request", r.Method, r.URL.Path)
			http.Error(w, "unexpected", 500)
		}
	})
	if code := main.Run([]string{"--json", "pipeline", "indexes", id, "--approve", "frozen-advice"}); code != 0 {
		t.Fatal(code)
	}
	if mutations != 1 {
		t.Fatal(mutations)
	}
}

func TestPipelineDurableDiscoveryRetryKeepsOneSubmissionIdentity(t *testing.T) {
	source, id := uuid.NewString(), uuid.NewString()
	posts := 0
	var key string
	pipelineOperabilityCLI(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "POST /v1/data/pipeline-setups":
			posts++
			if posts == 1 {
				key = r.Header.Get("Idempotency-Key")
				api.WriteProblem(w, api.NewProblem(409, "data_connection_check_required", "Discovery required", "Check selected tables first."))
				return
			}
			if key == "" || r.Header.Get("Idempotency-Key") != key {
				t.Error("discovery retry changed submission identity")
			}
			main.WriteJSONTest(w, api.DataPipelineSetup{ID: id, State: "working", Revision: 1})
		case "GET /v1/data/connections":
			main.WriteJSONTest(w, api.DataConnectionList{Items: []api.DataConnection{{ID: source, Revision: 3}}})
		case "POST /v1/data/connections/" + source + "/check":
			main.WriteJSONTest(w, api.DataConnectionCheck{ConnectionID: source, State: "passed", Fresh: true, Tables: []api.DataSourceTable{{Name: "orders", Supported: true}}})
		default:
			t.Error("unexpected allocation or legacy fallback", r.Method, r.URL.Path)
			http.Error(w, "unexpected", 500)
		}
	})
	if code := main.Run([]string{"--json", "pipeline", "setup", "create", "customer-orders", "--connection", source, "--tables", "orders", "--read-subjects", "alice"}); code != 0 {
		t.Fatal(code)
	}
	if posts != 2 {
		t.Fatal("setup discovery did not retry", posts)
	}
}

func TestPipelineDurableWaitEmitsOneJSONReceipt(t *testing.T) {
	id, source := uuid.NewString(), uuid.NewString()
	pipelineOperabilityCLI(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method+" "+r.URL.Path != "POST /v1/data/pipeline-setups" {
			t.Error("unexpected setup request", r.Method, r.URL.Path)
		}
		main.WriteJSONTest(w, api.DataPipelineSetup{ID: id, Revision: 1, State: "ready"})
	})
	out, restore := main.CaptureStdout(t)
	defer restore()
	if code := main.Run([]string{"--json", "pipeline", "setup", "create", "customer-orders", "--connection", source, "--tables", "orders", "--read-subjects", "alice", "--wait"}); code != 0 {
		t.Fatal(code)
	}
	decoder := json.NewDecoder(strings.NewReader(out.String()))
	var receipt api.DataPipelineSetup
	if err := decoder.Decode(&receipt); err != nil || receipt.ID != id {
		t.Fatal("missing setup receipt", out.String(), err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		t.Fatal("wait printed multiple JSON documents", out.String(), err)
	}
}
