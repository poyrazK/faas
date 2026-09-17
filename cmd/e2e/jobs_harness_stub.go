//go:build metal

// jobs_harness_stub.go — Metal-tagged acceptance harness for issue #1184.
// The harness drives apid over HTTP and leaves scheduling, vmmd, guest-init,
// and meterd in their real subprocesses.

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/state"
)

// MetalJobHarness owns the real daemon harness plus the fake OCI registry and
// bearer keys used by the jobs acceptance tests. Image refs are translated to
// digest-pinned fake-registry refs before they reach apid.
type MetalJobHarness struct {
	t *testing.T
	*e2etest.Harness
	registry *e2etest.FakeRegistry

	mu          sync.RWMutex
	imageRefs   map[string]string
	accountKeys map[string]string
	defaultKey  string
	runNames    map[string]string
}

func (h *MetalJobHarness) Close() {
	if h == nil {
		return
	}
	if h.registry != nil {
		h.registry.Close()
	}
	if h.Harness != nil {
		h.Harness.Stop()
	}
}

// newMetalHarness boots the apid → schedd → vmmd path and meterd. Jobs do not
// use gatewayd or builderd, but imaged is started so this acceptance retains
// the same image/signing environment as the other metal tests.
func newMetalHarness(t *testing.T) *MetalJobHarness {
	t.Helper()
	if os.Getenv("FAAS_TEST_KERNEL") == "" {
		t.Skip("FAAS_TEST_KERNEL unset; skipping metal jobs acceptance")
	}
	if _, err := os.Stat("/dev/kvm"); err != nil {
		t.Skipf("/dev/kvm not available: %v", err)
	}

	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return nil
	}
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	registry := e2etest.NewFakeRegistry()
	t.Cleanup(registry.Close)
	builderImg, _ := e2etest.HelloImage("onebox-faas/builder-base", "")
	e2etest.OverrideBuilderBase(t, registry.AddImage("onebox-faas/builder-base", builderImg))

	// Jobs are an opt-in schedd surface. Shrink meterd's cadences so the
	// billing assertion observes a row during a single acceptance test rather
	// than waiting on the production minute/rollup defaults.
	t.Setenv("FAAS_JOBS_DISPATCH", "1")
	t.Setenv("FAAS_SAMPLE_INTERVAL", "1s")
	t.Setenv("FAAS_ROLLUP_INTERVAL", "1s")
	h := e2etest.Start(t, pool, e2etest.APID|e2etest.Schedd|e2etest.VMMD|e2etest.Imaged|e2etest.Meterd)
	jh := &MetalJobHarness{
		t: t, Harness: h, registry: registry,
		imageRefs: make(map[string]string), accountKeys: make(map[string]string),
		runNames: make(map[string]string),
	}
	acct := jh.seedAccount(t, api.PlanHobby, "default")
	jh.defaultKey = jh.accountKeys[acct.ID]
	return jh
}

// MustSeedFakeImage installs one minimal OCI image under the supplied logical
// ref and remembers the digest-pinned URL used by subsequent job creation.
func (h *MetalJobHarness) MustSeedFakeImage(t *testing.T, imageRef string) {
	t.Helper()
	if h == nil || h.registry == nil {
		t.Fatal("jobs harness is not initialized")
	}
	repo := imageRef
	if i := strings.IndexAny(repo, ":@"); i >= 0 {
		repo = repo[:i]
	}
	img, _ := e2etest.HelloImage(repo, "")
	pinned := h.registry.AddImage(repo, img)
	h.mu.Lock()
	h.imageRefs[imageRef] = pinned
	h.mu.Unlock()
}

func (h *MetalJobHarness) imageRef(t *testing.T, ref string) string {
	h.mu.RLock()
	pinned := h.imageRefs[ref]
	h.mu.RUnlock()
	if pinned == "" {
		t.Fatalf("fake image %q was not seeded", ref)
	}
	return pinned
}

func (h *MetalJobHarness) keyFor(acct *state.Account) string {
	if acct == nil {
		return h.defaultKey
	}
	h.mu.RLock()
	key := h.accountKeys[acct.ID]
	h.mu.RUnlock()
	return key
}

func (h *MetalJobHarness) request(t *testing.T, key, method, path string, body any) ([]byte, int, http.Header) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		blob, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal %s %s: %v", method, path, err)
		}
		reader = bytes.NewReader(blob)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, h.APIDURL+path, reader)
	if err != nil {
		t.Fatalf("request %s %s: %v", method, path, err)
	}
	req.Header.Set("Authorization", "Bearer "+key)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := h.HTTPClient().Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read %s %s: %v", method, path, err)
	}
	return raw, resp.StatusCode, resp.Header.Clone()
}

func decodeOK[T any](t *testing.T, raw []byte, status int, out *T, method, path string) {
	t.Helper()
	if status < 200 || status >= 300 {
		t.Fatalf("%s %s: status=%d body=%s", method, path, status, raw)
	}
	if out != nil && len(raw) != 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			t.Fatalf("decode %s %s: %v body=%s", method, path, err, raw)
		}
	}
}

func (h *MetalJobHarness) MustCreateJob(t *testing.T, name, imageRef string, command []string, ramMB int) *api.JobResponse {
	path := "/v1/jobs"
	raw, status, _ := h.request(t, h.defaultKey, http.MethodPost, path, api.CreateJobRequest{
		Name: name, ImageRef: h.imageRef(t, imageRef), Command: command, RAMMB: ramMB,
	})
	var out api.JobResponse
	decodeOK(t, raw, status, &out, http.MethodPost, path)
	return &out
}

func (h *MetalJobHarness) MustUpdateJob(t *testing.T, job *api.JobResponse, mut func(*api.UpdateJobRequest)) *api.JobResponse {
	var req api.UpdateJobRequest
	mut(&req)
	path := "/v1/jobs/" + job.Name
	raw, status, _ := h.request(t, h.defaultKey, http.MethodPatch, path, req)
	var out api.JobResponse
	decodeOK(t, raw, status, &out, http.MethodPatch, path)
	return &out
}

func (h *MetalJobHarness) MustDispatchRun(t *testing.T, job *api.JobResponse, tasks int) *api.JobRunResponse {
	path := "/v1/jobs/" + job.Name + "/runs"
	raw, status, _ := h.request(t, h.defaultKey, http.MethodPost, path, api.CreateJobRunRequest{Tasks: tasks})
	var out api.JobRunResponse
	decodeOK(t, raw, status, &out, http.MethodPost, path)
	h.mu.Lock()
	h.runNames[out.ID] = job.Name
	h.mu.Unlock()
	return &out
}

func (h *MetalJobHarness) runName(t *testing.T, run *api.JobRunResponse) string {
	h.mu.RLock()
	name := h.runNames[run.ID]
	h.mu.RUnlock()
	if name == "" {
		t.Fatalf("run %s was not dispatched by this harness", run.ID)
	}
	return name
}

func (h *MetalJobHarness) refreshRun(t *testing.T, run *api.JobRunResponse) {
	name := h.runName(t, run)
	path := "/v1/jobs/" + name + "/runs/" + run.ID
	raw, status, _ := h.request(t, h.defaultKey, http.MethodGet, path, nil)
	decodeOK(t, raw, status, run, http.MethodGet, path)
}

func (h *MetalJobHarness) MustWaitRunTerminal(t *testing.T, run *api.JobRunResponse, want string, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		h.refreshRun(t, run)
		if (want == "any-terminal" && isTerminalRun(run.AggregateStatus)) || run.AggregateStatus == want {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("run %s aggregate_status=%q, want %q within %s", run.ID, run.AggregateStatus, want, timeout)
}

func isTerminalRun(status string) bool {
	switch status {
	case "succeeded", "failed", "cancelled", "dead_letter":
		return true
	default:
		return false
	}
}

func (h *MetalJobHarness) listTasks(t *testing.T, run *api.JobRunResponse) []api.JobTaskResponse {
	name := h.runName(t, run)
	path := "/v1/jobs/" + name + "/runs/" + run.ID + "/tasks"
	raw, status, _ := h.request(t, h.defaultKey, http.MethodGet, path, nil)
	var out api.ListJobTasksResponse
	decodeOK(t, raw, status, &out, http.MethodGet, path)
	return out.Tasks
}

func findTask(tasks []api.JobTaskResponse, index int) (api.JobTaskResponse, bool) {
	for _, task := range tasks {
		if task.TaskIndex == index {
			return task, true
		}
	}
	// The first M14 test prose used one-based indexes; accept that form too.
	if index > 0 {
		for _, task := range tasks {
			if task.TaskIndex == index-1 {
				return task, true
			}
		}
	}
	return api.JobTaskResponse{}, false
}

func (h *MetalJobHarness) MustWaitTaskStatus(t *testing.T, run *api.JobRunResponse, taskIndex int, want string, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		task, ok := findTask(h.listTasks(t, run), taskIndex)
		if ok && task.Status == want {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("task %d did not reach status=%q within %s", taskIndex, want, timeout)
}

func (h *MetalJobHarness) MustWaitTaskAttempt(t *testing.T, run *api.JobRunResponse, taskIndex, wantAttempt int, wantStatus string, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		task, ok := findTask(h.listTasks(t, run), taskIndex)
		if ok && task.Attempt == wantAttempt && task.Status == wantStatus {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("task %d did not reach attempt=%d status=%q within %s", taskIndex, wantAttempt, wantStatus, timeout)
}

func (h *MetalJobHarness) MustCancelRun(t *testing.T, run *api.JobRunResponse) {
	name := h.runName(t, run)
	path := "/v1/jobs/" + name + "/runs/" + run.ID + "/cancel"
	raw, status, _ := h.request(t, h.defaultKey, http.MethodPost, path, nil)
	var out api.JobRunCancelledResponse
	decodeOK(t, raw, status, &out, http.MethodPost, path)
	*run = out.Run
}

func (h *MetalJobHarness) MustAssertTaskExitCodes(t *testing.T, run *api.JobRunResponse, want int) {
	tasks := h.listTasks(t, run)
	if len(tasks) == 0 {
		t.Fatalf("run %s has no tasks", run.ID)
	}
	for _, task := range tasks {
		if task.ExitCode != want {
			t.Errorf("task %d exit_code=%d, want %d (status=%s)", task.TaskIndex, task.ExitCode, want, task.Status)
		}
	}
}

func (h *MetalJobHarness) MustAssertTaskExitCode(t *testing.T, run *api.JobRunResponse, taskIndex, want int) {
	task, ok := findTask(h.listTasks(t, run), taskIndex)
	if !ok {
		t.Fatalf("task %d not found in run %s", taskIndex, run.ID)
	}
	if task.ExitCode != want {
		t.Errorf("task %d exit_code=%d, want %d", taskIndex, task.ExitCode, want)
	}
}

func (h *MetalJobHarness) MustAssertRunDeadLetter(t *testing.T, run *api.JobRunResponse, want int) {
	h.refreshRun(t, run)
	if run.DeadLetterCount != want {
		t.Errorf("run %s dead_letter_count=%d, want %d", run.ID, run.DeadLetterCount, want)
	}
}

func (h *MetalJobHarness) MustAssertNoInstancesForRun(t *testing.T, run *api.JobRunResponse) {
	var count int
	err := h.Pool.QueryRow(context.Background(), `
		SELECT count(*) FROM instances i
		JOIN job_tasks jt ON jt.instance_id = i.id
		WHERE i.kind = 'job_task' AND jt.run_id = $1::uuid`, run.ID).Scan(&count)
	if err != nil {
		t.Fatalf("query instances for run %s: %v", run.ID, err)
	}
	if count != 0 {
		t.Errorf("run %s has %d job_task instances, want 0", run.ID, count)
	}
}

func (h *MetalJobHarness) MustAssertUsageDailyRows(t *testing.T, accountID, jobID string, ramMB int, wantDur, tolerance time.Duration) {
	want := int64(ramMB) * int64(wantDur/time.Second)
	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		var rows int
		var mbSeconds int64
		err := h.Pool.QueryRow(context.Background(), `
			SELECT count(*), coalesce(sum(mb_seconds), 0) FROM usage_daily
			WHERE account_id = $1::uuid AND meter_kind = 'job' AND job_id = $2::uuid`, accountID, jobID).Scan(&rows, &mbSeconds)
		if err == nil && rows > 0 {
			delta := mbSeconds - want
			if delta < 0 {
				delta = -delta
			}
			// meterd samples in one-minute quanta. A short-lived test job
			// can therefore contribute one full quantum even when its wall
			// time is only a few seconds; include that bounded quantisation
			// error in the wall-clock tolerance.
			allowed := int64(ramMB) * (int64(tolerance/time.Second) + 60)
			if delta <= allowed {
				return
			}
		}
		time.Sleep(time.Second)
	}
	t.Fatalf("usage_daily job row for account=%s job=%s did not reach ~%d MB-s", accountID, jobID, want)
}

func (h *MetalJobHarness) MustKillSchedd(t *testing.T) {
	if err := h.Harness.KillSchedd(); err != nil {
		t.Fatalf("kill schedd: %v", err)
	}
}

func (h *MetalJobHarness) MustRestartSchedd(t *testing.T) {
	if err := h.Harness.RestartSchedd(); err != nil {
		t.Fatalf("restart schedd: %v", err)
	}
}

func (h *MetalJobHarness) MustSetEnv(t *testing.T, key, value string) {
	t.Setenv(key, value)
	if err := h.Harness.SetScheddEnv(key, value); err != nil {
		t.Fatalf("set schedd env %s: %v", key, err)
	}
	if err := h.Harness.RestartSchedd(); err != nil {
		t.Fatalf("apply schedd env %s: %v", key, err)
	}
}

func (h *MetalJobHarness) seedAccount(t *testing.T, plan api.Plan, label string) *state.Account {
	store := state.NewPgStore(h.Pool)
	res, err := store.CreateAccountWithPersonalOrg(context.Background(), state.CreateAccountWithPersonalOrgParams{
		Email: "e2e+jobs+" + label + "+" + uuid.NewString() + "@test.example", Plan: plan,
	})
	if err != nil {
		t.Fatalf("seed %s account: %v", plan, err)
	}
	plain, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatalf("generate %s API key: %v", plan, err)
	}
	if _, err := store.CreateAPIKey(context.Background(), res.Account.ID, hash, "jobs-e2e", api.ScopesAdminOnly); err != nil {
		t.Fatalf("store %s API key: %v", plan, err)
	}
	h.mu.Lock()
	h.accountKeys[res.Account.ID] = plain
	h.mu.Unlock()
	return &res.Account
}

func (h *MetalJobHarness) MustCreateFreeAccount(t *testing.T) *state.Account {
	return h.seedAccount(t, api.PlanFree, "free")
}

func (h *MetalJobHarness) MustCreateHobbyAccount(t *testing.T) *state.Account {
	acct := h.seedAccount(t, api.PlanHobby, "hobby")
	// BillingRollup creates the account and then uses the convenience job
	// helpers (which authenticate with the current account) rather than the
	// explicit *AsAccount variant.
	h.mu.Lock()
	h.defaultKey = h.accountKeys[acct.ID]
	h.mu.Unlock()
	return acct
}

func (h *MetalJobHarness) MustCreateJobAsAccount(t *testing.T, acct *state.Account, name, imageRef string, command []string, ramMB int) *api.JobResponse {
	path := "/v1/jobs"
	// Quota/plan-gate cases exercise apid only and intentionally do not boot
	// imaged. Preserve their logical ref when no fixture was requested.
	h.mu.RLock()
	pinned := h.imageRefs[imageRef]
	h.mu.RUnlock()
	if pinned == "" {
		pinned = imageRef
	}
	raw, status, _ := h.request(t, h.keyFor(acct), http.MethodPost, path, api.CreateJobRequest{
		Name: name, ImageRef: pinned, Command: command, RAMMB: ramMB,
	})
	var out api.JobResponse
	decodeOK(t, raw, status, &out, http.MethodPost, path)
	return &out
}

func (h *MetalJobHarness) MustPostAsAccount(t *testing.T, acct *state.Account, path string, body any) *httptest.ResponseRecorder {
	raw, status, headers := h.request(t, h.keyFor(acct), http.MethodPost, path, body)
	rec := httptest.NewRecorder()
	for key, values := range headers {
		for _, value := range values {
			rec.Header().Add(key, value)
		}
	}
	rec.WriteHeader(status)
	_, _ = rec.Body.Write(raw)
	return rec
}
