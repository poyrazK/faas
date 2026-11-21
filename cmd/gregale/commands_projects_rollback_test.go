package main_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

type rollbackFixture struct {
	mu           sync.Mutex
	current      map[string]int
	members      []api.ProjectRollbackMember
	calls        []string
	receipts     map[string]api.RollbackOperation
	fail         string
	loseResponse string
}

func rollbackID(slug string, revision int) string {
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte(slug+":"+strconv.Itoa(revision))).String()
}

func projectRollbackFixture(t *testing.T) *rollbackFixture {
	t.Helper()
	f := &rollbackFixture{
		current:  map[string]int{"shop-web": 4, "shop-api": 7, "shop-billing": 2},
		receipts: make(map[string]api.RollbackOperation),
	}
	for slug, revision := range f.current {
		f.members = append(f.members, api.ProjectRollbackMember{Slug: slug, Revision: revision, DeploymentID: rollbackID(slug, revision),
			PredecessorID: rollbackID(slug, revision-1), PredecessorRevision: revision - 1, Status: "live", Scope: "default"})
	}
	srv := httptest.NewServer(http.HandlerFunc(f.serveHTTP))
	t.Cleanup(srv.Close)
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_test_x")
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	return f
}

func (f *rollbackFixture) recordedCalls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

func (f *rollbackFixture) serveHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	write := func(v any) { _ = json.NewEncoder(w).Encode(v) }
	if r.Method == "GET" && r.URL.Path == "/v1/projects/shop" {
		p := api.ProjectResponse{ProjectSummaryResponse: api.ProjectSummaryResponse{ID: rollbackID("shop", 0), Slug: "shop"},
			Workloads: []api.ProjectWorkloadResponse{
				{Slug: "shop-web", WorkloadName: "web", Status: "active", DependsOn: []string{"api"}},
				{Slug: "shop-api", WorkloadName: "api", Status: "active", DependsOn: []string{"billing"}},
				{Slug: "shop-billing", WorkloadName: "billing", Status: "active"},
			}}
		if r.URL.Query().Get("include_rollback") == "true" && len(f.members) > 0 {
			p.RollbackBatch = &api.ProjectRollbackBatch{ID: rollbackID("batch", 1), Members: f.members}
		}
		write(p)
		return
	}
	path := strings.Split(strings.TrimPrefix(r.URL.Path, "/v1/apps/"), "/")
	slug, revision := path[0], f.current[path[0]]
	switch {
	case r.Method == "GET" && len(path) == 2 && path[1] == "deployments":
		write(map[string]any{"items": []api.DeploymentResponse{{ID: rollbackID(slug, revision), AppID: rollbackID(slug, 0),
			Status: "live", RolloutState: "complete", TrafficPercent: 100, Scope: "default", Revision: revision}}})
	case r.Method == "GET" && len(path) == 4 && path[3] == "summary":
		write(api.DeploymentSummaryResponse{RollbackTargetID: rollbackID(slug, revision-1), RollbackTargetRevision: revision - 1})
	case r.Method == "GET" && len(path) == 3 && path[1] == "rollbacks":
		op, ok := f.receipts[path[2]]
		if !ok {
			api.WriteProblem(w, api.NewProblem(404, "not_found", "Not found", "Unknown rollback"))
			return
		}
		if op.Status == "ready" {
			now := time.Now()
			op.Status, op.AuditID, op.CompletedAt = "complete", "1", &now
			f.current[slug]--
			f.receipts[op.ID] = op
		}
		write(op)
	case r.Method == "POST" && len(path) == 2 && path[1] == "rollback":
		var req api.RollbackRequest
		if json.NewDecoder(r.Body).Decode(&req) != nil || req.ExpectedCurrentDeploymentID == nil || req.TargetDeploymentID == nil ||
			*req.ExpectedCurrentDeploymentID != rollbackID(slug, revision) || *req.TargetDeploymentID != rollbackID(slug, revision-1) {
			api.WriteProblem(w, api.NewProblem(409, "conflict", "Unexpected rollback pair", ""))
			return
		}
		f.calls = append(f.calls, slug)
		attempt, err := uuid.Parse(r.Header.Get(api.RollbackAttemptHeader))
		if err != nil {
			api.WriteProblem(w, api.NewProblem(400, "validation", "Missing attempt ID", ""))
			return
		}
		op := api.RollbackOperation{ID: attempt.String(), AppID: rollbackID(slug, 0), Scope: "default",
			Status: "ready", CurrentDeploymentID: *req.ExpectedCurrentDeploymentID, TargetDeploymentID: *req.TargetDeploymentID}
		if f.fail == slug {
			op.Status, op.Code = "failed", "rollback_target_not_activated"
		}
		f.receipts[op.ID] = op
		if f.loseResponse == slug {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusAccepted)
		write(api.DeploymentResponse{ID: op.TargetDeploymentID, RollbackOperation: &op})
	default:
		http.NotFound(w, r)
	}
}

func runProjectRollback(t *testing.T, args ...string) (int, string) {
	t.Helper()
	out, restore := main.CaptureStdout(t)
	code := main.Run(append([]string{"project", "rollback", "shop"}, args...))
	restore()
	return code, out.String()
}

func rollbackJSON(t *testing.T, out string) (string, []struct {
	Slug, Status string
}) {
	t.Helper()
	var receipt struct {
		ID    string `json:"operation_id"`
		Steps []struct{ Slug, Status string }
	}
	if err := json.Unmarshal([]byte(out), &receipt); err != nil {
		t.Fatalf("decode receipt: %v\n%s", err, out)
	}
	return receipt.ID, receipt.Steps
}

func TestProjectRollbackSelectsExactMembersAndWaitsInDependencyOrder(t *testing.T) {
	f := projectRollbackFixture(t)
	for i, member := range f.members {
		if member.Slug == "shop-billing" {
			f.members = append(f.members[:i], f.members[i+1:]...)
			break
		}
	}
	code, out := runProjectRollback(t, "--yes", "--json")
	id, steps := rollbackJSON(t, out)
	if code != 0 || id == "" || strings.Join(f.recordedCalls(), ",") != "shop-web,shop-api" ||
		len(steps) != 3 || steps[0].Status != "rolled_back" || steps[2].Status != "skipped" {
		t.Fatalf("exit %d, calls %v\n%s", code, f.recordedCalls(), out)
	}
}

func TestProjectRollbackResumesFrozenPlanAfterTerminalFailure(t *testing.T) {
	f := projectRollbackFixture(t)
	f.fail = "shop-api"
	code, out := runProjectRollback(t, "--yes", "--json")
	id, steps := rollbackJSON(t, out)
	if code == 0 || id == "" || len(steps) != 3 || steps[1].Status != "failed" || steps[2].Status != "planned" ||
		strings.Join(f.recordedCalls(), ",") != "shop-web,shop-api" {
		t.Fatalf("exit %d, calls %v\n%s", code, f.recordedCalls(), out)
	}
	f.mu.Lock()
	f.fail, f.members = "", nil
	f.mu.Unlock()
	code, out = runProjectRollback(t, "--resume", id, "--json")
	resumed, _ := rollbackJSON(t, out)
	if code != 0 || resumed != id || strings.Join(f.recordedCalls(), ",") != "shop-web,shop-api,shop-api,shop-billing" {
		t.Fatalf("resume exit %d, calls %v\n%s", code, f.recordedCalls(), out)
	}
}

func TestProjectRollbackResumeStopsForNewerTargetedDeployment(t *testing.T) {
	f := projectRollbackFixture(t)
	f.fail = "shop-api"
	_, out := runProjectRollback(t, "--yes", "--json")
	id, _ := rollbackJSON(t, out)
	f.mu.Lock()
	f.current["shop-billing"]++
	f.mu.Unlock()
	code, out := runProjectRollback(t, "--resume", id, "--json")
	if code == 0 || strings.Join(f.recordedCalls(), ",") != "shop-web,shop-api" || !strings.Contains(out, "production changed") {
		t.Fatalf("resume exit %d, calls %v\n%s", code, f.recordedCalls(), out)
	}
}

func TestProjectRollbackRecoversLostAcceptance(t *testing.T) {
	f := projectRollbackFixture(t)
	f.loseResponse = "shop-web"
	code, out := runProjectRollback(t, "--yes", "--json")
	if code != 0 || strings.Join(f.recordedCalls(), ",") != "shop-web,shop-api,shop-billing" {
		t.Fatalf("exit %d, calls %v\n%s", code, f.recordedCalls(), out)
	}
}

func TestProjectRollbackDryRunDoesNotSubmit(t *testing.T) {
	f := projectRollbackFixture(t)
	code, out := runProjectRollback(t, "--dry-run")
	if code != 0 || len(f.recordedCalls()) != 0 || !strings.Contains(out, "shop-web: v4 -> v3") {
		t.Fatalf("exit %d, calls %v\n%s", code, f.recordedCalls(), out)
	}
}

func TestProjectRollbackLegacyDeployRequiresExplicitAll(t *testing.T) {
	f := projectRollbackFixture(t)
	f.members = nil
	if code, _ := runProjectRollback(t, "--yes"); code == 0 || len(f.recordedCalls()) != 0 {
		t.Fatalf("unrecorded deploy: exit %d, calls %v", code, f.recordedCalls())
	}
	if code, out := runProjectRollback(t, "--yes", "--all"); code != 0 || len(f.recordedCalls()) != 3 {
		t.Fatalf("explicit all: exit %d, calls %v\n%s", code, f.recordedCalls(), out)
	}
}
