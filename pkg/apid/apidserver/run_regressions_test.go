package apidserver_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

func runRegressionMaterialize(t *testing.T, e apidserver.TestEnv, name string) {
	t.Helper()
	store := *e.Store()
	ctx := t.Context()
	job, err := store.JobGetByName(ctx, e.Acct().ID, name)
	if err != nil {
		t.Fatal(err)
	}
	claim, err := store.JobClaimImageMaterialization(ctx, job.ID, "probe", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.JobRecordImageConfig(ctx, job.ID, job.ImageRef, "probe", claim.ImageMaterializationAttempts, nil, []string{"true"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.JobPublishImageMaterialization(ctx, job.ID, job.ImageRef, "probe", claim.ImageMaterializationAttempts,
		"sha256:"+strings.Repeat("e", 64), "jobs/"+job.ID+"__probe.ext4"); err != nil {
		t.Fatal(err)
	}
}
func runRegressionPost(t *testing.T, e apidserver.TestEnv, image, key string) *httptest.ResponseRecorder {
	t.Helper()
	return e.Do(t, http.MethodPost, "/v1/image-runs", api.CreateImageRunRequest{Image: image}, map[string]string{"Idempotency-Key": key})
}
func runRegressionImageResponse(t *testing.T, r *httptest.ResponseRecorder) api.ImageRunResponse {
	t.Helper()
	var p api.ImageRunResponse
	if err := json.Unmarshal(r.Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestRunRegressionImagePullIdempotencyAdvancesToRun(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	(*e.S()).WithImageRunsEnabled(true)
	first := runRegressionPost(t, e, "alpine", "same-logical-run")
	if first.Code != http.StatusAccepted {
		t.Fatalf("first=%d %s", first.Code, first.Body)
	}
	p := runRegressionImageResponse(t, first)
	runRegressionMaterialize(t, e, p.Job)
	next := runRegressionPost(t, e, "alpine", "same-logical-run")
	if next.Code != http.StatusCreated {
		t.Errorf("ready image cannot advance to a run with the original key: status=%d replay=%q body=%s", next.Code, next.Header().Get("Idempotent-Replayed"), next.Body)
	}
	replay := runRegressionPost(t, e, "alpine", "same-logical-run")
	if replay.Code != http.StatusCreated || replay.Header().Get("Idempotent-Replayed") != "true" ||
		runRegressionImageResponse(t, replay).RunID != runRegressionImageResponse(t, next).RunID {
		t.Fatalf("accepted run was not replayed: %d %s", replay.Code, replay.Body)
	}
}

func TestRunRegressionImageCacheFullRetryRechecksCapacity(t *testing.T) {
	e := apidserver.Setup(t, api.PlanHobby)
	(*e.S()).WithImageRunsEnabled(true)
	var active []api.ImageRunResponse
	for i := 0; i < 3; i++ {
		image := fmt.Sprintf("busy-probe-%d", i)
		first := runRegressionPost(t, e, image, fmt.Sprintf("pull-%d", i))
		if first.Code != http.StatusAccepted {
			t.Fatalf("pull=%d %s", first.Code, first.Body)
		}
		runRegressionMaterialize(t, e, runRegressionImageResponse(t, first).Job)
		run := runRegressionPost(t, e, image, fmt.Sprintf("run-%d", i))
		if run.Code != http.StatusCreated {
			t.Fatalf("run=%d %s", run.Code, run.Body)
		}
		active = append(active, runRegressionImageResponse(t, run))
	}
	first := runRegressionPost(t, e, "fourth-probe", "retry-after-cache-full")
	if first.Code != http.StatusConflict {
		t.Fatalf("full=%d %s", first.Code, first.Body)
	}
	if err := (*e.Store()).JobTaskMarkTerminal(t.Context(), active[0].RunID, 0, "succeeded", 0, "", "", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	next := runRegressionPost(t, e, "fourth-probe", "retry-after-cache-full")
	if next.Code != http.StatusAccepted {
		t.Errorf("cache-full refusal survives available capacity: status=%d replay=%q body=%s", next.Code, next.Header().Get("Idempotent-Replayed"), next.Body)
	}
	fresh := runRegressionPost(t, e, "fourth-probe", "fresh-key-after-cache-full")
	if fresh.Code != http.StatusAccepted {
		t.Fatalf("capacity did not actually return: fresh key=%d %s", fresh.Code, fresh.Body)
	}
}

func TestRunRegressionManagedWorkflowIdempotencyKeepsKeyFamiliesSeparate(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	identity := apidserver.EnableExecutionAPIForTest(t, &e)
	old := *apidserver.MfaIdentities
	*apidserver.MfaIdentities = func() []*age.X25519Identity { return []*age.X25519Identity{identity} }
	t.Cleanup(func() { *apidserver.MfaIdentities = old })
	owner := apidserver.CreateRunsOnlyKey(t, e, "owner-agent")
	other := apidserver.CreateRunsOnlyKey(t, e, "other-agent")
	req := api.CreateManagedExecutionWorkflowRequest{WorkflowID: "private-run-plan", Version: "v1", Steps: []api.CreateManagedExecutionWorkflowStep{
		{Label: "step", Request: api.CreateExecutionRequest{Runtime: api.ExecutionRuntimeNode22, Source: "return 1"}},
	}}
	headers := map[string]string{"Idempotency-Key": "agent-stable-workflow-key"}
	a := e.DoAs(t, http.MethodPost, "/v1/execution-workflows", req, headers, owner)
	if a.Code != http.StatusAccepted {
		t.Fatalf("owner submit=%d %s", a.Code, a.Body)
	}
	b := e.DoAs(t, http.MethodPost, "/v1/execution-workflows", req, headers, other)
	if b.Code != http.StatusAccepted {
		t.Fatalf("other submit=%d %s", b.Code, b.Body)
	}
	status := e.DoAs(t, http.MethodGet, "/v1/execution-workflows/"+req.WorkflowID, nil, nil, other)
	if b.Header().Get("Idempotent-Replayed") == "true" || status.Code != http.StatusOK {
		t.Errorf("other agent received owner's workflow acceptance and no own job: replay=%q acceptance=%s own_status=%d own_body=%s", b.Header().Get("Idempotent-Replayed"), b.Body, status.Code, status.Body)
	}
}

func TestRunRegressionImageEvictionPreservesRetainedLogs(t *testing.T) {
	e := apidserver.Setup(t, api.PlanHobby)
	(*e.S()).WithImageRunsEnabled(true)
	first := runRegressionPost(t, e, "logged-probe", "pull-log")
	if first.Code != http.StatusAccepted {
		t.Fatal(first.Body)
	}
	p := runRegressionImageResponse(t, first)
	runRegressionMaterialize(t, e, p.Job)
	queued := runRegressionPost(t, e, "logged-probe", "run-log")
	if queued.Code != http.StatusCreated {
		t.Fatal(queued.Body)
	}
	run := runRegressionImageResponse(t, queued)
	if err := (*e.Store()).JobTaskMarkTerminalWithLogs(t.Context(), run.RunID, 0, "succeeded", 0, "", "", "retained output\n", false, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	path := "/v1/jobs/" + run.Job + "/runs/" + run.RunID + "/tasks/0/logs"
	before := e.Do(t, http.MethodGet, path, nil, nil)
	if before.Code != http.StatusOK {
		t.Fatalf("before eviction=%d %s", before.Code, before.Body)
	}
	for i := 0; i < 3; i++ {
		r := runRegressionPost(t, e, fmt.Sprintf("new-probe-%d", i), fmt.Sprintf("new-pull-%d", i))
		if r.Code != http.StatusAccepted {
			t.Fatalf("new image=%d %s", r.Code, r.Body)
		}
	}
	stored, err := (*e.Store()).JobTaskGet(t.Context(), run.RunID, 0)
	if err != nil {
		t.Fatal(err)
	}
	after := e.Do(t, http.MethodGet, path, nil, nil)
	if after.Code != http.StatusOK {
		t.Errorf("automatic image eviction hid unexpired stored logs: status=%d retained=%q body=%s", after.Code, stored.LogContent, after.Body)
	}
	base := "/v1/jobs/" + run.Job + "/runs/" + run.RunID
	other, err := (*e.Store()).CreateAccount(t.Context(), "other-history@example.test", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	otherToken, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (*e.Store()).CreateAPIKey(t.Context(), other.ID, hash, "history", api.ScopesAdminOnly); err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"", "/tasks", "/tasks/0/logs", "/tasks/0/attempts"} {
		if response := e.Do(t, http.MethodGet, base+suffix, nil, nil); response.Code != http.StatusOK {
			t.Errorf("retained history %s = %d: %s", suffix, response.Code, response.Body)
		}
		if response := e.DoAs(t, http.MethodGet, base+suffix, nil, nil, otherToken); response.Code != http.StatusNotFound {
			t.Errorf("cross-account history %s = %d: %s", suffix, response.Code, response.Body)
		}
	}
	if response := e.Do(t, http.MethodPost, base+"/tasks/0/retry", nil, nil); response.Code != http.StatusNotFound {
		t.Errorf("evicted image run admitted a retry: %d %s", response.Code, response.Body)
	}
}
