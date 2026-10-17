package ciapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/ciapi"
	"github.com/onebox-faas/faas/pkg/ci"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

type executionAPIBackend struct{ stopped bool }

func (executionAPIBackend) Start(context.Context, ci.StartRequest) error { return nil }
func (executionAPIBackend) Cancel(context.Context, ci.AttemptKey) error  { return nil }
func (b executionAPIBackend) Observe(_ context.Context, req ci.ObserveRequest) (ci.Observation, error) {
	if b.stopped {
		return ci.Observation{Status: ci.StatusCancelled, LogOffset: req.LogOffset}, nil
	}
	return ci.Observation{Status: ci.StatusRunning, LogOffset: req.LogOffset, Steps: []ci.StepResult{{Name: "unit", Status: ci.StatusQueued}}}, nil
}

func TestCIExecutionAPIAdmissionOwnershipAndReadAfterDisable(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	store := ci.NewPostgresStore(pool)
	account, other, job := uuid.NewString(), uuid.NewString(), uuid.NewString()
	for _, id := range []string{account, other} {
		if _, err := pool.Exec(t.Context(), "INSERT INTO accounts(id,email,plan) VALUES ($1,$2,'pro')", id, id+"@example.invalid"); err != nil {
			t.Fatal(err)
		}
	}
	image := "registry.example/ci@sha256:" + strings.Repeat("a", 64)
	if _, err := pool.Exec(t.Context(), `INSERT INTO jobs(id,account_id,kind,name,image_ref,ram_mb,task_timeout_s,max_parallelism,retry_max,env_overrides,command,image_materialization_status,image_resolved_digest,image_storage_key)
 VALUES ($1,$2,'batch','api-test',$3,$4,600,1,0,'{}',$5,'ready',$6,'private/rootfs.ext4')`, job, account, image, api.CIJobsRAMMB, []string{ci.JobsRunnerPath, ci.JobsRunnerProtocol}, strings.Split(image, "@")[1]); err != nil {
		t.Fatal(err)
	}
	revision, err := store.PutRevision(t.Context(), account, "api", ci.Definition{Version: 1, Repository: "gregale/api", Jobs: map[string]ci.Job{"test": {Image: image, Steps: []ci.Step{{Name: "unit", Run: "stored, never executed"}}, Artifacts: []string{"reports/**"}, JUnit: []string{"reports/unit.xml"}}}})
	if err != nil {
		t.Fatal(err)
	}
	binding, err := store.PutGitHubBinding(t.Context(), account, "api", ci.GitHubBindingRequest{RevisionID: revision.ID, InstallationID: 12, RepositoryID: 34, PushBranches: []string{"main"}})
	if err != nil {
		t.Fatal(err)
	}
	cfg := ci.ConfigFromEnv(func(key string) string {
		if key == ci.AccountsEnv {
			return account + "," + other
		}
		switch key {
		case ci.InternalEnv, ci.GitHubEnv, ci.DispatchEnv, ci.TransportEnv, ci.WorkerEnv, ci.GitHubChecksEnv, ci.ExecutionChecksEnv:
			return "1"
		}
		return ""
	})
	verified := 0
	h := ciapi.Handler{Config: cfg, Store: store, ExecutionReady: true, VerifyGitHub: func(context.Context, string, int64, int64, string) error { verified++; return nil }}
	request := ci.JobsSubmitRequest{SubmitRequest: ci.SubmitRequest{RevisionID: revision.ID, CommitSHA: strings.Repeat("a", 40)}, BindingID: binding.ID, Templates: map[string]string{"test": job}}
	call := func(action ciapi.Action, acct, id, attempt, query string, body any, key string) *httptest.ResponseRecorder {
		t.Helper()
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest(http.MethodPost, "/"+query, bytes.NewReader(raw))
		r.SetPathValue("id", id)
		r.SetPathValue("attempt", attempt)
		r.Header.Set("Idempotency-Key", key)
		w := httptest.NewRecorder()
		action(h, w, r, state.Account{ID: acct})
		return w
	}
	for _, gate := range []string{"worker", "transport", "readiness"} {
		h.Config = cfg
		h.ExecutionReady = true
		switch gate {
		case "worker":
			h.Config.WorkerEnabled = false
		case "transport":
			h.Config.TransportEnabled = false
		case "readiness":
			h.ExecutionReady = false
		}
		if w := call(ciapi.Handler.SubmitExecution, account, "", "", "", request, "fresh"); w.Code != http.StatusNotFound || verified != 0 {
			t.Fatal("admission escaped prerequisite gate")
		}
	}
	h.Config = cfg
	h.ExecutionReady = true
	w := call(ciapi.Handler.SubmitExecution, account, "", "", "", request, "fresh")
	if w.Code != http.StatusAccepted {
		t.Fatalf("admission: %d %s", w.Code, w.Body)
	}
	if strings.Contains(w.Body.String(), "private/rootfs.ext4") || strings.Contains(w.Body.String(), "installation_id") {
		t.Fatal("execution response exposed internal provenance")
	}
	var row ci.Execution
	if err := json.Unmarshal(w.Body.Bytes(), &row); err != nil {
		t.Fatal(err)
	}
	replay := call(ciapi.Handler.SubmitExecution, account, "", "", "", request, "fresh")
	if replay.Code != http.StatusAccepted || replay.Header().Get("Idempotency-Replayed") != "true" {
		t.Fatal("admission lost idempotency")
	}
	if w := call(ciapi.Handler.SubmitExecution, other, "", "", "", request, "foreign"); w.Code != http.StatusNotFound {
		t.Fatal("cross-account source accepted")
	}
	if w := call(ciapi.Handler.SubmitExecution, account, "", "", "", request, ""); w.Code != http.StatusUnprocessableEntity && w.Code != http.StatusBadRequest {
		t.Fatal("missing admission identity accepted")
	}
	row, err = (ci.Engine{Store: store, Backend: executionAPIBackend{}}).Tick(t.Context(), account, row.ID)
	if err != nil {
		t.Fatal(err)
	}
	logs := []byte{0, 255, 'A'}
	encoded, _ := json.Marshal(logs)
	if _, err := pool.Exec(t.Context(), "UPDATE ci_executions SET snapshot=jsonb_set(snapshot,'{jobs,0,attempt,logs}',$2::jsonb) WHERE id=$1", row.ID, string(encoded)); err != nil {
		t.Fatal(err)
	}
	h.Config.DispatchEnabled = false
	h.Config.WorkerEnabled = false
	h.Config.GitHubEnabled = false
	if w := call(ciapi.Handler.Execution, account, row.ID, "", "", nil, ""); w.Code != http.StatusOK || strings.Contains(w.Body.String(), `"logs"`) {
		t.Fatal("status is unavailable after disable or embeds raw logs")
	}
	if w := call(ciapi.Handler.Execution, other, row.ID, "", "", nil, ""); w.Code != http.StatusNotFound {
		t.Fatal("execution ownership escaped")
	}
	beforeReports := verified
	reportResponse := call(ciapi.Handler.TestReports, account, row.ID, "", "", nil, "")
	var reports ci.ExecutionTestReports
	if reportResponse.Code != http.StatusOK || json.Unmarshal(reportResponse.Body.Bytes(), &reports) != nil || reports.ExecutionID != row.ID || reports.Totals.Expected != 1 || reports.Jobs[0].State != "pending" || verified != beforeReports {
		t.Fatal("report read required provider access or lost execution identity")
	}
	for _, private := range []string{"stored, never executed", "private/rootfs.ext4", "installation_id", "lease_token"} {
		if strings.Contains(reportResponse.Body.String(), private) {
			t.Fatal("report projection exposed private execution authority")
		}
	}
	if w := call(ciapi.Handler.TestReports, other, row.ID, "", "", nil, ""); w.Code != http.StatusNotFound {
		t.Fatal("report read crossed account boundary")
	}
	page := call(ciapi.Handler.ExecutionLogs, account, row.ID, row.Jobs[0].Attempt.ID, "?offset=1&limit=1", nil, "")
	var result ci.LogPage
	if page.Code != http.StatusOK || json.Unmarshal(page.Body.Bytes(), &result) != nil || !bytes.Equal(result.Data, []byte{255}) || result.NextOffset != 2 {
		t.Fatal("logs did not preserve bounded byte cursor")
	}
	if w := call(ciapi.Handler.ExecutionLogs, account, row.ID, row.Jobs[0].Attempt.ID, "?offset=0&offset=1", nil, ""); w.Code == http.StatusOK {
		t.Fatal("ambiguous log cursor accepted")
	}
	if w := call(ciapi.Handler.CancelExecution, account, row.ID, "", "", nil, ""); w.Code != http.StatusOK {
		t.Fatal("dispatch disable blocked cancellation")
	}
	qualification, _, err := store.SubmitQualification(t.Context(), account, "qualification", request.SubmitRequest)
	if err != nil {
		t.Fatal(err)
	}
	if w := call(ciapi.Handler.Execution, account, qualification.ID, "", "", nil, ""); w.Code != http.StatusNotFound {
		t.Fatal("qualification exposed as service execution")
	}
	if w := call(ciapi.Handler.TestReports, account, qualification.ID, "", "", nil, ""); w.Code != http.StatusNotFound {
		t.Fatal("qualification exposed as service test reports")
	}
	list := call(ciapi.Handler.ListExecutions, account, "", "", "", nil, "")
	if list.Code != http.StatusOK || strings.Contains(list.Body.String(), qualification.ID) || !strings.Contains(list.Body.String(), row.ID) {
		t.Fatal("execution list crossed its mode boundary")
	}
	testCICheckRecoveryAPI(t, &h, pool, account, other, row.ID, qualification.ID, call)
	testCIArchiveAPI(t, &h, account, other, row.ID, qualification.ID, request, call)
}

func testCIArchiveAPI(t *testing.T, h *ciapi.Handler, account, other, id, qualification string, request ci.JobsSubmitRequest, call func(ciapi.Action, string, string, string, string, any, string) *httptest.ResponseRecorder) {
	t.Helper()
	if w := call(ciapi.Handler.ArchiveExecution, account, id, "", "", struct{}{}, ""); w.Code != http.StatusConflict {
		t.Fatal("pending Check recovery allowed archive")
	}
	cfg := h.Config
	cfg.GitHubEnabled, cfg.GitHubChecksEnabled, cfg.ExecutionChecksEnabled = true, true, true
	if _, err := (ci.Engine{Store: h.Store.(*ci.PostgresStore), Backend: executionAPIBackend{stopped: true}}).Tick(t.Context(), account, id); err != nil {
		t.Fatal(err)
	}
	reportResponse := call(ciapi.Handler.TestReports, account, id, "", "", nil, "")
	var reports ci.ExecutionTestReports
	if reportResponse.Code != http.StatusOK || json.Unmarshal(reportResponse.Body.Bytes(), &reports) != nil || reports.Totals.Problems != 1 || reports.Jobs[0].State != "incomplete" || reports.Jobs[0].Reports[0].Status != ci.ReportMissing {
		t.Fatal("cancelled execution lost missing-report diagnostics")
	}
	if _, err := ci.PublishExecutionCheckOnce(t.Context(), cfg, h.Store.(*ci.PostgresStore), h.VerifyGitHub, func(context.Context, ci.ExecutionCheck) (int64, error) { return 77, nil }); err != nil {
		t.Fatal(err)
	}
	if w := call(ciapi.Handler.ArchiveExecution, account, id, "", "", map[string]any{"force": true}, ""); w.Code != http.StatusBadRequest {
		t.Fatal("client bypassed archive safety with unknown fields")
	}
	if w := call(ciapi.Handler.ArchiveExecution, other, id, "", "", struct{}{}, ""); w.Code != http.StatusNotFound {
		t.Fatal("foreign account archived execution")
	}
	if w := call(ciapi.Handler.ArchiveExecution, account, qualification, "", "", struct{}{}, ""); w.Code != http.StatusNotFound {
		t.Fatal("qualification archived as Jobs history")
	}
	audits := 0
	h.Audit = func(_ context.Context, kind string, owner *string, data map[string]any) {
		if kind != "ci.execution.archived" || owner == nil || *owner != account || data["execution_id"] != id {
			t.Error("archive audit identity missing")
		}
		audits++
	}
	w := call(ciapi.Handler.ArchiveExecution, account, id, "", "", struct{}{}, "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"execution_id":"`+id+`"`) || strings.Contains(w.Body.String(), "stored, never executed") {
		t.Fatalf("archive response: %d %s", w.Code, w.Body)
	}
	w = call(ciapi.Handler.ArchiveExecution, account, id, "", "", struct{}{}, "")
	if w.Code != http.StatusOK || w.Header().Get("Idempotency-Replayed") != "true" || audits != 1 {
		t.Fatal("archive acknowledgement loss duplicated audit")
	}
	for _, action := range []ciapi.Action{ciapi.Handler.ExecutionArchives, ciapi.Handler.ExecutionArchive} {
		if w := call(action, account, id, "", "", nil, ""); w.Code != http.StatusOK {
			t.Fatal("archive read requires compute/publication gates")
		}
	}
	if w := call(ciapi.Handler.Execution, account, id, "", "", nil, ""); w.Code != http.StatusGone || !strings.Contains(w.Body.String(), "ci_execution_archived") {
		t.Fatal("compacted detail did not return stable gone response")
	}
	if w := call(ciapi.Handler.TestReports, account, id, "", "", nil, ""); w.Code != http.StatusGone {
		t.Fatal("archived report details remained available")
	}
	h.Config = cfg
	h.Config.DispatchEnabled, h.Config.WorkerEnabled, h.Config.TransportEnabled, h.ExecutionReady = true, true, true, true
	if w := call(ciapi.Handler.SubmitExecution, account, "", "", "", request, "fresh"); w.Code != http.StatusGone {
		t.Fatalf("archived admission key replay: %d", w.Code)
	}
	h.Config.Enabled = false
	if w := call(ciapi.Handler.ExecutionArchives, account, "", "", "", nil, ""); w.Code != http.StatusNotFound {
		t.Fatal("disabled internal gate exposed archive")
	}
	if w := call(ciapi.Handler.TestReports, account, id, "", "", nil, ""); w.Code != http.StatusNotFound {
		t.Fatal("disabled internal gate exposed reports")
	}
}

func testCICheckRecoveryAPI(t *testing.T, h *ciapi.Handler, pool *pgxpool.Pool, account, other, id, qualification string, call func(ciapi.Action, string, string, string, string, any, string) *httptest.ResponseRecorder) {
	t.Helper()
	for _, action := range []ciapi.Action{ciapi.Handler.ListCheckDeliveries, ciapi.Handler.CheckDelivery} {
		w := call(action, account, id, "", "", nil, "")
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), id) || strings.Contains(w.Body.String(), "lease_token") || strings.Contains(w.Body.String(), "private/rootfs.ext4") {
			t.Fatal("delivery recovery read unavailable or exposed private data")
		}
	}
	if w := call(ciapi.Handler.CheckDelivery, other, id, "", "", nil, ""); w.Code != http.StatusNotFound {
		t.Fatal("foreign account read Check delivery")
	}
	if w := call(ciapi.Handler.CheckDelivery, account, qualification, "", "", nil, ""); w.Code != http.StatusNotFound {
		t.Fatal("qualification exposed as Check delivery")
	}
	if w := call(ciapi.Handler.RetryCheckDelivery, account, id, "", "", ci.CheckRetryRequest{}, "retry"); w.Code != http.StatusNotFound {
		t.Fatal("disabled GitHub gate admitted retry")
	}
	h.Config.GitHubEnabled = true
	h.ExecutionReady = false
	for _, body := range []any{map[string]any{}, map[string]any{"expected_retry_generation": -1}, map[string]any{"expected_retry_generation": 0, "check_id": 77}} {
		if w := call(ciapi.Handler.RetryCheckDelivery, account, id, "", "", body, "retry"); w.Code != http.StatusBadRequest {
			t.Fatalf("invalid retry accepted: %d", w.Code)
		}
	}
	if w := call(ciapi.Handler.RetryCheckDelivery, account, id, "", "", ci.CheckRetryRequest{}, ""); w.Code != http.StatusBadRequest {
		t.Fatal("retry omitted durable request identity")
	}
	store := h.Store.(*ci.PostgresStore)
	_, err := ci.PublishExecutionCheckOnce(t.Context(), h.Config, store, h.VerifyGitHub, func(context.Context, ci.ExecutionCheck) (int64, error) { return 0, ci.ErrGitHubAccess })
	if err != nil {
		t.Fatal(err)
	}
	if w := call(ciapi.Handler.RetryCheckDelivery, account, id, "", "", ci.CheckRetryRequest{}, "denied"); w.Code != http.StatusConflict {
		t.Fatal("permission-denied publication was revived")
	}
	if _, err := pool.Exec(t.Context(), "UPDATE ci_execution_checks SET status='dead',attempts=$2,last_error='unavailable' WHERE execution_id=$1", id, api.CIGitHubMaxAttempts); err != nil {
		t.Fatal(err)
	}
	audits := 0
	h.Audit = func(_ context.Context, kind string, owner *string, data map[string]any) {
		if kind != "ci.check.retry_requested" || owner == nil || *owner != account || data["execution_id"] != id {
			t.Error("retry audit identity missing")
		}
		audits++
	}
	first := call(ciapi.Handler.RetryCheckDelivery, account, id, "", "", ci.CheckRetryRequest{}, "retry")
	if first.Code != http.StatusAccepted {
		t.Fatalf("retry admission: %d %s", first.Code, first.Body)
	}
	replay := call(ciapi.Handler.RetryCheckDelivery, account, id, "", "", ci.CheckRetryRequest{}, "retry")
	if replay.Code != http.StatusAccepted || replay.Header().Get("Idempotency-Replayed") != "true" || audits != 1 {
		t.Fatal("retry response loss duplicated intent or audit")
	}
	if w := call(ciapi.Handler.RetryCheckDelivery, account, id, "", "", ci.CheckRetryRequest{}, "other-key"); w.Code != http.StatusConflict {
		t.Fatal("pending retry admitted twice")
	}
	if w := call(ciapi.Handler.RetryCheckDelivery, other, id, "", "", ci.CheckRetryRequest{}, "foreign"); w.Code != http.StatusNotFound {
		t.Fatal("foreign account retried Check")
	}
	h.Config.GitHubEnabled, h.Config.GitHubChecksEnabled, h.Config.ExecutionChecksEnabled = false, false, false
	if w := call(ciapi.Handler.CheckDelivery, account, id, "", "", nil, ""); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"retry_pending":true`) {
		t.Fatal("publication disable hid recovery intent")
	}
}
