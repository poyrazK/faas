package main_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

const testRollbackPlanHash = "aabbccddaabbccddaabbccddaabbccddaabbccddaabbccddaabbccddaabbccdd"

type projectRollbackHTTP struct {
	mu              sync.Mutex
	op              api.ProjectRollbackOperation
	calls           []string
	requestIDs      []string
	planHash        string
	toDeploy        string
	targetGroup     string
	protection      *api.ProjectRollbackProtection
	ttlSeconds      int
	blocked         bool
	hold            bool
	all             bool
	ack             bool
	verify          bool
	createdID       string
	dropStart       bool
	undoSource      string
	automaticUndoID string
}

func projectRollbackTestServer(t *testing.T, blocked, hold bool) *projectRollbackHTTP {
	t.Helper()
	f := &projectRollbackHTTP{
		op: api.ProjectRollbackOperation{Project: "shop", ProjectID: uuid.NewString(), Status: "planned",
			Steps: []api.ProjectRollbackStep{
				{Service: "web", Slug: "shop-web", From: 4, To: 3, Status: "planned"},
				{Service: "api", Slug: "shop-api", From: 7, To: 6, Status: "planned"},
			}},
		blocked: blocked, hold: hold, targetGroup: uuid.NewString(),
	}
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(srv.Close)
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_test_x")
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	return f
}

func (f *projectRollbackHTTP) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, r.Method+" "+r.URL.Path)
	root := "/v1/projects/shop/rollbacks"
	out := f.op
	status := http.StatusOK
	switch {
	case r.Method == http.MethodPost && r.URL.Path == root+"/targets/"+f.targetGroup+"/protection":
		var req api.ProtectProjectRollbackDeployRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			api.WriteProblem(w, api.ErrValidation(err.Error()))
			return
		}
		f.ttlSeconds = req.TTLSeconds
		f.protection = &api.ProjectRollbackProtection{
			DeployGroupID: f.targetGroup, ExpiresAt: time.Now().UTC().Add(time.Duration(req.TTLSeconds) * time.Second), LayerBytes: 8192,
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(f.protection)
		return
	case r.Method == http.MethodGet && r.URL.Path == root+"/targets":
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(api.ProjectRollbackTargets{
			Project: out.Project, ProjectID: out.ProjectID, Deploys: []api.ProjectRollbackTargetDeploy{
				{DeployGroupID: f.targetGroup, CreatedAt: time.Now().UTC(), ServiceCount: 2, Protection: f.protection},
			},
		})
		return
	case r.Method == http.MethodGet && r.URL.Path == root+"/preview":
		out.DeployGroupID = r.URL.Query().Get("to_deploy")
		if !f.blocked {
			out.PlanHash = testRollbackPlanHash
		}
		f.all = r.URL.Query().Get("all") == "true"
		f.ack = r.URL.Query().Get("acknowledge_releases") == "true"
		out.Verify = r.URL.Query().Get("verify") == "true"
		out.StabilizeForSeconds, _ = strconv.Atoi(r.URL.Query().Get("stabilize_for_seconds"))
		out.UndoOnHealthFailure = r.URL.Query().Get("undo_on_health_failure") == "true"
		out.VerifyBeforeCutover = r.URL.Query().Get("verify_before_cutover") == "true"
		out.VerifyLiveTraffic = r.URL.Query().Get("verify_live_traffic") == "true"
		out.UndoOnLiveTrafficFailure = r.URL.Query().Get("undo_on_live_traffic_failure") == "true"
		if out.VerifyLiveTraffic {
			out.Steps[0].LiveTraffic = &api.ProjectRollbackLiveTraffic{Status: "pending"}
		}
		if out.VerifyBeforeCutover {
			out.Steps[0].PreCutoverHealth = &api.ProjectRollbackPreCutoverHealth{Status: "pending"}
		}
		if out.Verify {
			out.Steps[0].Health = &api.ProjectRollbackHealth{Status: "pending"}
		}
	case r.Method == http.MethodGet && r.URL.Path == root:
		history := api.ProjectRollbackHistory{Project: out.Project, ProjectID: out.ProjectID}
		if out.ID != "" {
			history.Operations = []api.ProjectRollbackSummary{
				{ID: out.ID, Status: out.Status, CutoversTotal: len(out.Steps)},
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(history)
		return
	case r.Method == http.MethodPost && r.URL.Path == root:
		var req api.ProjectRollbackRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			api.WriteProblem(w, api.ErrValidation(err.Error()))
			return
		}
		f.all, f.ack, f.verify = req.All, req.AcknowledgeReleases, req.Verify
		f.requestIDs = append(f.requestIDs, req.RequestID)
		f.planHash = req.ExpectedPlanHash
		f.toDeploy = req.ToDeploy
		out.Verify = req.Verify
		out.StabilizeForSeconds = req.StabilizeForSeconds
		out.VerifyBeforeCutover = req.VerifyBeforeCutover
		out.VerifyLiveTraffic = req.VerifyLiveTraffic
		out.UndoOnLiveTrafficFailure = req.UndoOnLiveTrafficFailure
		out.UndoOnHealthFailure = req.UndoOnHealthFailure
		if out.ID != "" && req.RequestID != "" && req.RequestID == out.RequestID {
			break
		}
		if !f.blocked {
			out.RequestID = req.RequestID
			out.PlanHash = req.ExpectedPlanHash
			out.DeployGroupID = req.ToDeploy
			out.ID, out.Status = uuid.NewString(), "running"
			if req.UndoOnHealthFailure && f.automaticUndoID != "" {
				out.Status, out.UndoOperationID = "blocked", f.automaticUndoID
				out.AutoUndo = &api.ProjectRollbackAutoUndo{Status: api.ProjectRollbackAutoUndoStarted, TriggerAppID: uuid.NewString()}
				f.createdID, f.op = out.ID, out
				break
			}
			f.createdID, f.op = out.ID, out
			status = http.StatusAccepted
			if f.dropStart {
				conn, _, err := w.(http.Hijacker).Hijack()
				if err == nil {
					_ = conn.Close()
					return
				}
			}
		}
	case r.Method == http.MethodGet && f.undoSource != "" && r.URL.Path == root+"/"+f.undoSource+"/undo/preview":
		out.UndoOf, out.PlanHash = f.undoSource, testRollbackPlanHash
		out.AcknowledgeReleases = r.URL.Query().Get("acknowledge_releases") == "true"
		expires := time.Now().Add(time.Hour).UTC()
		out.Steps = []api.ProjectRollbackStep{{
			Slug: "shop-web", Service: "web", From: 3, To: 4, Status: "planned",
			CrossedReleases: []api.RollbackCrossedRelease{{Revision: 5, Status: "succeeded", StartedAt: "2026-11-22T12:00:00Z"}},
			UndoArtifacts:   &api.ProjectRollbackUndoArtifacts{DeploymentID: uuid.NewString(), ExpiresAt: &expires},
		}}
	case r.Method == http.MethodPost && f.undoSource != "" && r.URL.Path == root+"/"+f.undoSource+"/undo":
		var req api.ProjectRollbackUndoRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			api.WriteProblem(w, api.ErrValidation(err.Error()))
			return
		}
		f.ack, f.planHash = req.AcknowledgeReleases, req.ExpectedPlanHash
		out.PlanHash = req.ExpectedPlanHash
		out.ID, out.Status, out.UndoOf = uuid.NewString(), "running", f.undoSource
		f.createdID, f.op = out.ID, out
		status = http.StatusAccepted
	case r.Method == http.MethodPost && f.op.ID != "" && r.URL.Path == root+"/"+f.op.ID+"/pause":
		now := time.Now().UTC()
		out.PauseRequestedAt = &now
		out.Phase = api.ProjectRollbackReleasing
		f.op = out
		status = http.StatusAccepted
	case r.Method == http.MethodGet && f.op.ID != "" && r.URL.Path == root+"/"+f.op.ID:
		if out.PauseRequestedAt != nil {
			out.Status = "blocked"
			f.op = out
		} else if !f.hold {
			out.Status = "complete"
			for i := range out.Steps {
				out.Steps[i].Status = "rolled_back"
			}
			f.op = out
		}
	case r.Method == http.MethodPost && f.op.ID != "" && r.URL.Path == root+"/"+f.op.ID+"/resume":
		if r.ContentLength > 0 {
			var req api.ProjectRollbackResumeRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				api.WriteProblem(w, api.ErrValidation(err.Error()))
				return
			}
			f.planHash, out.ResumePlanHash = req.ExpectedPlanHash, req.ExpectedPlanHash
		}
		out.Status = "running"
		f.op = out
	case r.Method == http.MethodGet && f.op.ID != "" && r.URL.Path == root+"/"+f.op.ID+"/resume/preview":
		out.Status, out.PlanHash, out.ResumePlanHash = "planned", "", testRollbackPlanHash
		out.Steps = slices.Clone(f.op.Steps)
		out.Steps[0].Status = "rolled_back"
		out.Steps[0].Health = &api.ProjectRollbackHealth{Status: "pending"}
	default:
		http.NotFound(w, r)
		return
	}
	if f.blocked {
		out.Status = "blocked"
		out.Steps[1].Status = "blocked"
		out.Steps[1].Reason = "Artifact missing"
		out.Steps[1].Problem = api.ErrRollbackTargetUnavailable("Artifact missing")
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(out)
}

func runProjectRollback(t *testing.T, args ...string) (int, string) {
	t.Helper()
	out, restore := main.CaptureStdout(t)
	code := main.Run(append([]string{"project", "rollback", "shop"}, args...))
	restore()
	return code, out.String()
}

func TestProjectRollbackUsesServerReceiptFromFreshStateDirectory(t *testing.T) {
	f := projectRollbackTestServer(t, false, true)
	code, out := runProjectRollback(t, "--yes", "--timeout", "10ms", "--all", "--acknowledge-releases", "--json")
	var receipt api.ProjectRollbackOperation
	if err := json.Unmarshal([]byte(out), &receipt); err != nil || code == 0 || receipt.ID == "" || receipt.Status != "running" {
		t.Fatalf("exit %d: %s (%v)", code, out, err)
	}
	f.mu.Lock()
	if got := strings.Join(f.calls, ","); got != "POST /v1/projects/shop/rollbacks" || f.createdID != receipt.ID || !f.all || !f.ack {
		t.Errorf("start was not made through the project API: %s", got)
	}
	f.hold = false
	f.mu.Unlock()

	t.Setenv("XDG_STATE_HOME", t.TempDir())
	code, out = runProjectRollback(t, "--status", receipt.ID, "--json")
	var got api.ProjectRollbackOperation
	if err := json.Unmarshal([]byte(out), &got); err != nil || code != 0 || got.ID != receipt.ID || got.Status != "complete" {
		t.Fatalf("fresh status exit %d: %s (%v)", code, out, err)
	}
}

func TestProjectRollbackPreflightReportsBlockerBeforeSubmission(t *testing.T) {
	for _, flag := range []string{"--dry-run", "--yes"} {
		t.Run(flag, func(t *testing.T) {
			f := projectRollbackTestServer(t, true, false)
			code, out := runProjectRollback(t, flag, "--json")
			var plan api.ProjectRollbackOperation
			if err := json.Unmarshal([]byte(out), &plan); err != nil || code == 0 || plan.ID != "" || plan.Status != "blocked" ||
				len(plan.Steps) != 2 || plan.Steps[1].Problem == nil || plan.Steps[1].Problem.Code != api.CodeRollbackTargetUnavailable {
				t.Fatalf("exit %d: %s (%v)", code, out, err)
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.createdID != "" || len(f.calls) != 1 || flag == "--dry-run" && !strings.HasPrefix(f.calls[0], "GET ") {
				t.Fatalf("unexpected submissions %v", f.calls)
			}
		})
	}
}

func TestProjectRollbackResumeUsesServerOperation(t *testing.T) {
	f := projectRollbackTestServer(t, false, false)
	f.mu.Lock()
	f.op.ID, f.op.Status = uuid.NewString(), "blocked"
	id := f.op.ID
	f.mu.Unlock()
	code, out := runProjectRollback(t, "--resume", id, "--json")
	var op api.ProjectRollbackOperation
	if err := json.Unmarshal([]byte(out), &op); err != nil || code != 0 || op.ID != id || op.Status != "complete" {
		t.Fatalf("exit %d: %s (%v)", code, out, err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.calls) != 2 || !strings.HasSuffix(f.calls[0], "/resume") || !strings.HasPrefix(f.calls[1], "GET ") {
		t.Fatalf("unexpected calls %v", f.calls)
	}
}

func TestProjectRollbackVerifyFlag(t *testing.T) {
	f := projectRollbackTestServer(t, false, false)
	if code, out := runProjectRollback(t, "--dry-run", "--verify"); code != 0 ||
		!strings.Contains(out, "shop-web will run its configured health probes after cutover") {
		t.Fatalf("verified preview = %d %s", code, out)
	}
	if code, out := runProjectRollback(t, "--yes", "--verify"); code != 0 {
		t.Fatalf("verified rollback = %d %s", code, out)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.verify {
		t.Fatal("verify was not sent with the accepted rollback")
	}
}

func TestProjectRollbackUndoUsesItsOwnReceipt(t *testing.T) {
	f := projectRollbackTestServer(t, false, false)
	source := uuid.NewString()
	f.mu.Lock()
	f.undoSource = source
	f.mu.Unlock()
	code, out := runProjectRollback(t, "--undo", source, "--acknowledge-releases")
	if code != 0 || !strings.Contains(out, "rollback undo complete") {
		t.Fatalf("undo = %d %s", code, out)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	root := "/v1/projects/shop/rollbacks/"
	if !f.ack || len(f.calls) != 2 || f.calls[0] != "POST "+root+source+"/undo" ||
		f.calls[1] != "GET "+root+f.createdID || f.createdID == source {
		t.Fatalf("CLI did not follow its distinct undo receipt: %v", f.calls)
	}
}

func TestProjectRollbackPreviewsAndAppliesReviewedUndo(t *testing.T) {
	f := projectRollbackTestServer(t, false, false)
	source := uuid.NewString()
	f.mu.Lock()
	f.undoSource = source
	f.mu.Unlock()

	if code, out := runProjectRollback(t, "--preview-undo", source, "--acknowledge-releases"); code != 0 ||
		!strings.Contains(out, "v3 -> v4") || !strings.Contains(out, "crosses release v5") ||
		!strings.Contains(out, "undo artifacts held until") ||
		!strings.Contains(out, testRollbackPlanHash) {
		t.Fatalf("undo preview = %d %s", code, out)
	}
	code, out := runProjectRollback(t, "--preview-undo", source, "--acknowledge-releases", "--json")
	var preview api.ProjectRollbackOperation
	if code != 0 || json.Unmarshal([]byte(out), &preview) != nil || preview.UndoOf != source ||
		!preview.AcknowledgeReleases || preview.ID != "" || preview.PlanHash != testRollbackPlanHash {
		t.Fatalf("JSON undo preview = %d %s", code, out)
	}
	if code, _ := runProjectRollback(t, "--preview-undo", source, "--plan-hash", preview.PlanHash); code == 0 {
		t.Fatal("preview accepted a submit-only plan hash")
	}
	if code, out := runProjectRollback(t, "--undo", source, "--plan-hash", preview.PlanHash, "--acknowledge-releases"); code != 0 ||
		!strings.Contains(out, "rollback undo complete") {
		t.Fatalf("reviewed undo = %d %s", code, out)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	root := "/v1/projects/shop/rollbacks/"
	want := []string{"GET " + root + source + "/undo/preview", "GET " + root + source + "/undo/preview",
		"POST " + root + source + "/undo", "GET " + root + f.createdID}
	if !f.ack || f.planHash != preview.PlanHash || !slices.Equal(f.calls, want) {
		t.Fatalf("reviewed undo sent hash %q and calls %v", f.planHash, f.calls)
	}
}

func TestProjectRollbackPreviewsAndAppliesReviewedResume(t *testing.T) {
	f := projectRollbackTestServer(t, false, false)
	id := uuid.NewString()
	f.mu.Lock()
	f.op.ID, f.op.Status, f.op.PlanHash = id, "blocked", strings.Repeat("d", 64)
	f.mu.Unlock()
	if code, out := runProjectRollback(t, "--preview-resume", id); code != 0 ||
		!strings.Contains(out, "restored v3") || !strings.Contains(out, "v7 -> v6") ||
		!strings.Contains(out, "rerun its configured health probes") || !strings.Contains(out, testRollbackPlanHash) {
		t.Fatalf("resume preview = %d %s", code, out)
	}
	if code, out := runProjectRollback(t, "--resume", id, "--plan-hash", testRollbackPlanHash); code != 0 ||
		!strings.Contains(out, "rollback complete") {
		t.Fatalf("reviewed resume = %d %s", code, out)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	root := "/v1/projects/shop/rollbacks/" + id
	want := []string{"GET " + root + "/resume/preview", "POST " + root + "/resume", "GET " + root}
	if f.planHash != testRollbackPlanHash || !slices.Equal(f.calls, want) {
		t.Fatalf("resume sent hash %q and calls %v", f.planHash, f.calls)
	}
}

func TestProjectRollbackPauseWaitsForCleanup(t *testing.T) {
	f := projectRollbackTestServer(t, false, false)
	id := uuid.NewString()
	f.mu.Lock()
	f.op.ID, f.op.Status = id, "running"
	f.mu.Unlock()
	code, out := runProjectRollback(t, "--pause", id)
	if code != 0 || !strings.Contains(out, "is paused") {
		t.Fatalf("pause = %d %s", code, out)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	url := "/v1/projects/shop/rollbacks/" + id
	if len(f.calls) != 2 || f.calls[0] != "POST "+url+"/pause" || f.calls[1] != "GET "+url {
		t.Fatalf("CLI did not follow the paused receipt: %v", f.calls)
	}
}

func TestProjectRollbackHistoryRecoversLostStartResponse(t *testing.T) {
	f := projectRollbackTestServer(t, false, true)
	f.mu.Lock()
	f.dropStart = true
	f.mu.Unlock()
	if code, out := runProjectRollback(t, "--yes"); code == 0 {
		t.Fatalf("start response was not lost: %s", out)
	}
	f.mu.Lock()
	id := f.createdID
	f.mu.Unlock()
	if id == "" {
		t.Fatal("server did not accept the rollback before dropping the response")
	}

	t.Setenv("XDG_STATE_HOME", t.TempDir())
	if code, out := runProjectRollback(t, "--history"); code != 0 ||
		!strings.Contains(out, id) || !strings.Contains(out, "running") || !strings.Contains(out, "0/2") {
		t.Fatalf("history = %d %s", code, out)
	}
	if code, out := runProjectRollback(t, "--status", id); code != 0 || !strings.Contains(out, id) {
		t.Fatalf("status = %d %s", code, out)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	root := "/v1/projects/shop/rollbacks"
	if len(f.calls) != 3 || f.calls[0] != "POST "+root || f.calls[1] != "GET "+root ||
		f.calls[2] != "GET "+root+"/"+id {
		t.Fatalf("history or status resubmitted the rollback: %v", f.calls)
	}
}

func TestProjectRollbackStartRequestCanBeRetriedFromAFreshSession(t *testing.T) {
	f := projectRollbackTestServer(t, false, true)
	f.mu.Lock()
	f.dropStart = true
	f.mu.Unlock()
	requestID := uuid.NewString()
	if code, out := runProjectRollback(t, "--yes", "--request-id", requestID); code == 0 || !strings.Contains(out, requestID) {
		t.Fatalf("losing the start response did not preserve the request ID: %d %s", code, out)
	}
	f.mu.Lock()
	id := f.createdID
	f.op.Status, f.dropStart = "complete", false
	f.mu.Unlock()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	if code, out := runProjectRollback(t, "--yes", "--request-id", requestID); code != 0 ||
		!strings.Contains(out, "rollback complete") {
		t.Fatalf("retry = %d %s", code, out)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if id == "" || f.createdID != id || len(f.requestIDs) != 2 ||
		f.requestIDs[0] != requestID || f.requestIDs[1] != requestID || len(f.calls) != 2 {
		t.Fatalf("retry did not reuse the same request and receipt: %v %v", f.calls, f.requestIDs)
	}
}

func TestProjectRollbackAppliesTheReviewedPlan(t *testing.T) {
	f := projectRollbackTestServer(t, false, false)
	if code, out := runProjectRollback(t, "--dry-run"); code != 0 || !strings.Contains(out, testRollbackPlanHash) {
		t.Fatalf("text preview = %d %s", code, out)
	}
	code, out := runProjectRollback(t, "--dry-run", "--json")
	var preview api.ProjectRollbackOperation
	if err := json.Unmarshal([]byte(out), &preview); err != nil || code != 0 || preview.PlanHash != testRollbackPlanHash {
		t.Fatalf("JSON preview = %d %s (%v)", code, out, err)
	}
	if code, _ := runProjectRollback(t, "--dry-run", "--plan-hash", preview.PlanHash); code == 0 {
		t.Fatal("--plan-hash must require --yes")
	}
	if code, _ := runProjectRollback(t, "--yes", "--plan-hash="); code == 0 {
		t.Fatal("an empty plan hash would start an unreviewed rollback")
	}
	if code, out := runProjectRollback(t, "--yes", "--plan-hash", preview.PlanHash); code != 0 || !strings.Contains(out, "rollback complete") {
		t.Fatalf("reviewed rollback = %d %s", code, out)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.planHash != preview.PlanHash || len(f.requestIDs) != 1 {
		t.Fatalf("expected one start with the reviewed hash, got %q: %v", f.planHash, f.calls)
	}
}

func TestProjectRollbackListsAndRestoresRecordedDeploy(t *testing.T) {
	f := projectRollbackTestServer(t, false, false)
	code, out := runProjectRollback(t, "--targets", "--json")
	var targets api.ProjectRollbackTargets
	if err := json.Unmarshal([]byte(out), &targets); err != nil || code != 0 || len(targets.Deploys) != 1 {
		t.Fatalf("deploy targets = %d %s (%v)", code, out, err)
	}
	target := targets.Deploys[0].DeployGroupID
	if code, out := runProjectRollback(t, "--targets"); code != 0 || !strings.Contains(out, target) {
		t.Fatalf("text targets = %d %s", code, out)
	}
	code, out = runProjectRollback(t, "--dry-run", "--to-deploy", target, "--json")
	var preview api.ProjectRollbackOperation
	if err := json.Unmarshal([]byte(out), &preview); err != nil || code != 0 || preview.DeployGroupID != target ||
		preview.PlanHash != testRollbackPlanHash {
		t.Fatalf("selected preview = %d %s (%v)", code, out, err)
	}
	if code, _ := runProjectRollback(t, "--yes", "--to-deploy", target, "--all"); code == 0 {
		t.Fatal("target selection must be exclusive with --all")
	}
	if code, out := runProjectRollback(t, "--yes", "--to-deploy", target, "--plan-hash", preview.PlanHash); code != 0 ||
		!strings.Contains(out, "rollback complete") {
		t.Fatalf("selected rollback = %d %s", code, out)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.toDeploy != target || f.planHash != preview.PlanHash || len(f.requestIDs) != 1 {
		t.Fatalf("expected one start for the reviewed deploy, got %q: %v", f.toDeploy, f.calls)
	}
}

func TestProjectRollbackProtectsDeployAndListsExpiry(t *testing.T) {
	f := projectRollbackTestServer(t, false, false)
	if code, _ := runProjectRollback(t, "--targets", "--ttl", "2h"); code == 0 {
		t.Fatal("--ttl requires --protect-deploy")
	}
	code, out := runProjectRollback(t, "--protect-deploy", f.targetGroup, "--ttl", "2h", "--json")
	var result api.ProjectRollbackProtection
	if err := json.Unmarshal([]byte(out), &result); err != nil || code != 0 ||
		result.DeployGroupID != f.targetGroup || result.ExpiresAt.IsZero() || result.LayerBytes != 8192 {
		t.Fatalf("protect deploy = %d %s (%v)", code, out, err)
	}
	if code, out := runProjectRollback(t, "--targets"); code != 0 ||
		!strings.Contains(out, result.ExpiresAt.UTC().Format(time.RFC3339)) || !strings.Contains(out, "8.0 KiB") {
		t.Fatalf("list protection = %d %s", code, out)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ttlSeconds != 7200 || len(f.calls) != 2 {
		t.Fatalf("protection request = %v %d", f.calls, f.ttlSeconds)
	}
}

func TestProjectRollbackReportsLinkedAutomaticUndo(t *testing.T) {
	f := projectRollbackTestServer(t, false, false)
	f.mu.Lock()
	undo := uuid.NewString()
	f.automaticUndoID = undo
	f.mu.Unlock()
	if code, _ := runProjectRollback(t, "--yes", "--undo-on-health-failure"); code == 0 {
		t.Fatal("automatic undo requires --verify")
	}
	if code, out := runProjectRollback(t, "--dry-run", "--verify", "--undo-on-health-failure"); code != 0 ||
		!strings.Contains(out, "5xx after cutover will trigger automatic undo") {
		t.Fatalf("preview omitted the opt-in policy: %d %s", code, out)
	}
	if code, out := runProjectRollback(t, "--yes", "--verify", "--undo-on-health-failure"); code == 0 ||
		!strings.Contains(out, "Automatic undo "+undo+" accepted") || !strings.Contains(out, "--status "+undo) {
		t.Fatalf("automatic undo receipt = %d %s", code, out)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.op.UndoOnHealthFailure || f.op.UndoOperationID != undo || len(f.requestIDs) != 1 {
		t.Fatalf("expected one authorized automatic undo start: %+v %v", f.op, f.calls)
	}
}

func TestProjectRollbackPreCutoverVerificationCLI(t *testing.T) {
	f := projectRollbackTestServer(t, false, false)
	if code, _ := runProjectRollback(t, "--yes", "--verify-before-cutover"); code == 0 {
		t.Fatal("pre-cutover checks require --verify")
	}
	if code, out := runProjectRollback(t, "--dry-run", "--verify", "--verify-before-cutover"); code != 0 ||
		!strings.Contains(out, "will run its configured health probes before project traffic moves") {
		t.Fatalf("expected a pre-cutover preview, got %d %s", code, out)
	}
	if code, out := runProjectRollback(t, "--yes", "--verify", "--verify-before-cutover"); code != 0 {
		t.Fatalf("pre-cutover rollback = %d %s", code, out)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.op.VerifyBeforeCutover || len(f.requestIDs) != 1 {
		t.Fatalf("expected one start with pre-cutover verification: %+v %v", f.op, f.calls)
	}
}

func TestProjectRollbackStabilizationCLI(t *testing.T) {
	f := projectRollbackTestServer(t, false, false)
	if code, _ := runProjectRollback(t, "--yes", "--stabilize-for", "30s"); code == 0 {
		t.Fatal("stabilization requires --verify")
	}
	if code, _ := runProjectRollback(t, "--dry-run", "--verify", "--stabilize-for", "61s"); code == 0 {
		t.Fatal("stabilization requires an in-range duration")
	}
	if code, out := runProjectRollback(t, "--dry-run", "--verify", "--stabilize-for", "30s"); code != 0 ||
		!strings.Contains(out, "require 30s of healthy probes after cutover") {
		t.Fatalf("expected a stabilization preview, got %d %s", code, out)
	}
	if code, out := runProjectRollback(t, "--yes", "--verify", "--stabilize-for", "30s"); code != 0 {
		t.Fatalf("stabilized rollback = %d %s", code, out)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.op.StabilizeForSeconds != 30 || len(f.requestIDs) != 1 {
		t.Fatalf("expected one 30s stabilization start: %+v %v", f.op, f.calls)
	}
}

func TestProjectRollbackLiveTrafficCLI(t *testing.T) {
	f := projectRollbackTestServer(t, false, false)
	if code, _ := runProjectRollback(t, "--yes", "--verify-live-traffic"); code == 0 {
		t.Fatal("live-traffic verification requires --verify")
	}
	if code, out := runProjectRollback(t, "--dry-run", "--verify", "--verify-live-traffic"); code != 0 ||
		!strings.Contains(out, "verify its configured production 5xx budgets after cutover") {
		t.Fatalf("expected a live-traffic preview, got %d %s", code, out)
	}
	if code, out := runProjectRollback(t, "--yes", "--verify", "--verify-live-traffic"); code != 0 {
		t.Fatalf("live-traffic rollback = %d %s", code, out)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.op.VerifyLiveTraffic || len(f.requestIDs) != 1 {
		t.Fatalf("expected one start with live verification: %+v %v", f.op, f.calls)
	}
}

func TestProjectRollbackLiveTrafficUndoCLI(t *testing.T) {
	f := projectRollbackTestServer(t, false, false)
	if code, _ := runProjectRollback(t, "--yes", "--verify", "--undo-on-live-traffic-failure"); code == 0 {
		t.Fatal("live-traffic undo requires --verify-live-traffic")
	}
	flags := []string{"--verify", "--verify-live-traffic", "--undo-on-live-traffic-failure"}
	if code, out := runProjectRollback(t, append([]string{"--dry-run"}, flags...)...); code != 0 ||
		!strings.Contains(out, "sustained production 5xx breach will trigger automatic undo") {
		t.Fatalf("expected a live-traffic undo preview, got %d %s", code, out)
	}
	if code, out := runProjectRollback(t, append([]string{"--yes"}, flags...)...); code != 0 {
		t.Fatalf("live-traffic undo selection = %d %s", code, out)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.op.UndoOnLiveTrafficFailure || len(f.requestIDs) != 1 {
		t.Fatalf("expected one start with live-traffic undo: %+v %v", f.op, f.calls)
	}
}
