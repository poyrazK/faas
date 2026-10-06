package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/exclusivework"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestExclusiveOperationAPIAdmissionReceiptAndIdempotency(t *testing.T) {
	e := setup(t, api.PlanPro)
	deployment := mustSeedDeployment(t, e, "exclusive-api")
	if err := e.store.MarkDeploymentLive(t.Context(), deployment.ID); err != nil {
		t.Fatal(err)
	}
	app, err := e.store.AppByID(t.Context(), deployment.AppID)
	if err != nil {
		t.Fatal(err)
	}
	policy := exclusivework.Policy{Name: "crm-sync", Scope: "account", MemberAppIDs: []string{app.ID},
		Contention: "queue", LeaseSeconds: 5, MaxAttemptSeconds: 60}
	saved := e.do(t, http.MethodPut, "/v1/account/operation-policies/crm-sync", policy, nil)
	if saved.Code != http.StatusOK {
		t.Fatalf("save policy: %d %s", saved.Code, saved.Body)
	}
	request := api.ExclusiveOperationRequest{Policy: "crm-sync", Key: json.RawMessage(`"customer:acme:crm-sync"`),
		Invocation: api.InvokeRequest{Method: http.MethodPost, Path: "/sync", Payload: json.RawMessage(`{"mode":"incremental"}`)}}
	post := func() *httptest.ResponseRecorder {
		t.Helper()
		return e.do(t, http.MethodPost, "/v1/apps/exclusive-api/operations", request,
			map[string]string{"Idempotency-Key": "sync-acme-2026-09-30"})
	}
	first := post()
	if first.Code != http.StatusAccepted {
		t.Fatalf("submit operation: %d %s", first.Code, first.Body)
	}
	var accepted api.ExclusiveOperationAccepted
	if err := json.Unmarshal(first.Body.Bytes(), &accepted); err != nil {
		t.Fatal(err)
	}
	if accepted.ID == "" || accepted.Joined || accepted.StatusURL != "/v1/operations/"+accepted.ID {
		t.Fatalf("unexpected accepted response: %+v", accepted)
	}
	replay := post()
	var replayed api.ExclusiveOperationAccepted
	if replay.Code != http.StatusAccepted || json.Unmarshal(replay.Body.Bytes(), &replayed) != nil || replayed.ID != accepted.ID {
		t.Fatalf("idempotency replay: %d %s", replay.Code, replay.Body)
	}
	read := e.do(t, http.MethodGet, "/v1/operations/"+accepted.ID, nil, nil)
	if read.Code != http.StatusOK || read.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("read operation: %d %s", read.Code, read.Body)
	}
	if strings.Contains(read.Body.String(), "incremental") || strings.Contains(read.Body.String(), "claim_token") ||
		strings.Contains(read.Body.String(), "request") {
		t.Fatalf("receipt exposed private request or claim data: %s", read.Body)
	}
	var receipt api.ExclusiveOperationRecord
	if err := json.Unmarshal(read.Body.Bytes(), &receipt); err != nil || receipt.State != "pending" || receipt.PolicyRevision != 1 {
		t.Fatalf("receipt=%+v decode error=%v", receipt, err)
	}
	cancel := e.do(t, http.MethodPost, "/v1/operations/"+accepted.ID+"/cancel", nil, nil)
	if cancel.Code != http.StatusNoContent {
		t.Fatalf("cancel operation: %d %s", cancel.Code, cancel.Body)
	}
}

func TestExclusiveJobOperationAPIQueuesValidatedRunAndReturnsJobReceipt(t *testing.T) {
	e := setup(t, api.PlanPro)
	jobName := seedJob(t, e, "exclusive-job-api", "registry.example/importer:v1")
	job, err := e.store.JobGetByName(t.Context(), e.acct.ID, jobName)
	if err != nil {
		t.Fatal(err)
	}
	policy := exclusivework.Policy{Name: "job-import", Scope: "account", MemberJobIDs: []string{job.ID},
		Contention: "queue", LeaseSeconds: 5, MaxAttemptSeconds: 60}
	if saved := e.do(t, http.MethodPut, "/v1/account/operation-policies/job-import", policy, nil); saved.Code != http.StatusOK {
		t.Fatalf("save Job policy: %d %s", saved.Code, saved.Body)
	}
	request := api.ExclusiveJobOperationRequest{Policy: "job-import", Key: json.RawMessage(`"customer:acme:crm-import"`),
		Run: api.CreateJobRunRequest{Tasks: 1, EnvOverrides: map[string]string{"SYNC_MODE": "incremental"}}}
	post := func() *httptest.ResponseRecorder {
		t.Helper()
		return e.do(t, http.MethodPost, "/v1/jobs/"+jobName+"/operations", request,
			map[string]string{"Idempotency-Key": "crm-import-2026-10-01"})
	}
	first := post()
	var accepted api.ExclusiveOperationAccepted
	if first.Code != http.StatusAccepted || json.Unmarshal(first.Body.Bytes(), &accepted) != nil || accepted.ID == "" || accepted.StatusURL != "/v1/operations/"+accepted.ID {
		t.Fatalf("submit managed Job run: %d %s", first.Code, first.Body)
	}
	replay := post()
	var replayed api.ExclusiveOperationAccepted
	if replay.Code != http.StatusAccepted || json.Unmarshal(replay.Body.Bytes(), &replayed) != nil || replayed.ID != accepted.ID {
		t.Fatalf("managed Job idempotency replay: %d %s", replay.Code, replay.Body)
	}
	receipt := e.do(t, http.MethodGet, accepted.StatusURL, nil, nil)
	if receipt.Code != http.StatusOK || !strings.Contains(receipt.Body.String(), `"job_id":"`+job.ID+`"`) || strings.Contains(receipt.Body.String(), `"app_id"`) {
		t.Fatalf("managed Job operation receipt: %d %s", receipt.Code, receipt.Body)
	}
	runs, err := e.store.JobRunListByJob(t.Context(), job.ID, 10, 0)
	if err != nil || len(runs) != 0 {
		t.Fatalf("accepted request created a JobRun before ownership dispatch: runs=%d err=%v", len(runs), err)
	}
}

func TestExclusiveAppTaskOperationQueuesValidatedTaskUntilOwnershipDispatch(t *testing.T) {
	e := setup(t, api.PlanPro)
	enableAppTaskAPIForTest(&e)
	deployment := mustSeedDeployment(t, e, "exclusive-task-api")
	if err := e.store.MarkDeploymentLive(t.Context(), deployment.ID); err != nil {
		t.Fatal(err)
	}
	app, err := e.store.AppByID(t.Context(), deployment.AppID)
	if err != nil {
		t.Fatal(err)
	}
	policy := exclusivework.Policy{Name: "exclusive-maintenance", Scope: "account", MemberAppIDs: []string{app.ID},
		Contention: "queue", LeaseSeconds: 5, MaxAttemptSeconds: 60}
	if saved := e.do(t, http.MethodPut, "/v1/account/operation-policies/exclusive-maintenance", policy, nil); saved.Code != http.StatusOK {
		t.Fatalf("save AppTask policy: %d %s", saved.Code, saved.Body)
	}
	request := api.ExclusiveAppTaskOperationRequest{Policy: "exclusive-maintenance", Key: json.RawMessage(`"maintenance:acme"`),
		Task: api.CreateAppTaskRequest{Command: []string{"/bin/sh", "-c", "maintenance"}, TimeoutSeconds: 30}}
	post := func() *httptest.ResponseRecorder {
		t.Helper()
		return e.do(t, http.MethodPost, "/v1/apps/exclusive-task-api/operations/tasks", request,
			map[string]string{"Idempotency-Key": "maintenance-2026-10-01"})
	}
	first := post()
	var accepted api.ExclusiveOperationAccepted
	if first.Code != http.StatusAccepted || json.Unmarshal(first.Body.Bytes(), &accepted) != nil || accepted.ID == "" {
		t.Fatalf("submit managed AppTask: %d %s", first.Code, first.Body)
	}
	replay := post()
	var replayed api.ExclusiveOperationAccepted
	if replay.Code != http.StatusAccepted || json.Unmarshal(replay.Body.Bytes(), &replayed) != nil || replayed.ID != accepted.ID {
		t.Fatalf("managed AppTask idempotency replay: %d %s", replay.Code, replay.Body)
	}
	receipt := e.do(t, http.MethodGet, accepted.StatusURL, nil, nil)
	if receipt.Code != http.StatusOK || !strings.Contains(receipt.Body.String(), `"app_id":"`+app.ID+`"`) || strings.Contains(receipt.Body.String(), `"job_id"`) {
		t.Fatalf("managed AppTask operation receipt: %d %s", receipt.Code, receipt.Body)
	}
	tasks, err := e.store.ListAppTasks(t.Context(), e.acct.ID, app.ID, 10, 0)
	if err != nil || len(tasks) != 0 {
		t.Fatalf("accepted operation created AppTask before ownership dispatch: tasks=%d err=%v", len(tasks), err)
	}
}

func TestExclusiveTriggerBindingAPIResolvesAccountOwnedCron(t *testing.T) {
	e := setup(t, api.PlanPro)
	deployment := mustSeedDeployment(t, e, "exclusive-trigger-binding")
	if err := e.store.MarkDeploymentLive(t.Context(), deployment.ID); err != nil {
		t.Fatal(err)
	}
	app, err := e.store.AppByID(t.Context(), deployment.AppID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.UpsertExclusiveWorkPolicy(t.Context(), e.acct.ID, exclusivework.Policy{
		Name: "crm-sync", Scope: "account", MemberAppIDs: []string{app.ID}, Contention: "queue",
		LeaseSeconds: 5, MaxAttemptSeconds: 60,
	}); err != nil {
		t.Fatal(err)
	}
	cron, err := e.store.CreateCronWithOptions(t.Context(), app.ID, "0 * * * *", "/sync", true, state.CronOptions{Timezone: "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	path := "/v1/account/operation-trigger-bindings/cron/" + cron.ID
	request := api.ExclusiveTriggerBindingRequest{Policy: "crm-sync", Key: json.RawMessage(`"customer:acme:crm-sync"`), EquivalenceKey: "sync"}
	saved := e.do(t, http.MethodPut, path, request, nil)
	var binding api.ExclusiveTriggerBindingRecord
	if saved.Code != http.StatusOK || json.Unmarshal(saved.Body.Bytes(), &binding) != nil || binding.AppID != app.ID || binding.TriggerID != cron.ID {
		t.Fatalf("bind cron: %d %s", saved.Code, saved.Body)
	}
	read := e.do(t, http.MethodGet, path, nil, nil)
	if read.Code != http.StatusOK || !strings.Contains(read.Body.String(), `"policy":"crm-sync"`) {
		t.Fatalf("read binding: %d %s", read.Code, read.Body)
	}
	deleted := e.do(t, http.MethodDelete, path, nil, nil)
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("delete binding: %d %s", deleted.Code, deleted.Body)
	}
}

func TestExclusiveTriggerBindingAPIResolvesRecurringJobSchedule(t *testing.T) {
	e := setup(t, api.PlanPro)
	job, err := e.store.JobCreateScheduledIfUnderQuota(t.Context(), state.Job{
		AccountID: e.acct.ID, Name: "nightly-import", Kind: "recurring", ImageRef: "ghcr.io/acme/import:v1",
		Command: []string{"/app/import"}, RAMMB: 256, TaskTimeoutS: 60, MaxParallelism: 1,
		CronSchedule: "0 2 * * *", CronTimezone: "UTC",
	}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.UpsertExclusiveWorkPolicy(t.Context(), e.acct.ID, exclusivework.Policy{
		Name: "nightly-import", Scope: "account", MemberJobIDs: []string{job.ID},
		Contention: "queue", LeaseSeconds: 5, MaxAttemptSeconds: 60,
	}); err != nil {
		t.Fatal(err)
	}
	path := "/v1/account/operation-trigger-bindings/job_schedule/" + job.ID
	request := api.ExclusiveTriggerBindingRequest{Policy: "nightly-import", Key: json.RawMessage(`"nightly-import"`)}
	saved := e.do(t, http.MethodPut, path, request, nil)
	var binding api.ExclusiveTriggerBindingRecord
	if saved.Code != http.StatusOK || json.Unmarshal(saved.Body.Bytes(), &binding) != nil || binding.JobID != job.ID || binding.AppID != "" {
		t.Fatalf("bind Job schedule: %d %s", saved.Code, saved.Body)
	}
	read := e.do(t, http.MethodGet, path, nil, nil)
	if read.Code != http.StatusOK || !strings.Contains(read.Body.String(), `"job_id":"`+job.ID+`"`) || strings.Contains(read.Body.String(), `"app_id"`) {
		t.Fatalf("read Job schedule binding: %d %s", read.Code, read.Body)
	}
	deleted := e.do(t, http.MethodDelete, path, nil, nil)
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("delete Job schedule binding: %d %s", deleted.Code, deleted.Body)
	}
}

func TestPlatformTenantSelfExclusiveOperationRequiresLinkedAppAndReturnsScopedURL(t *testing.T) {
	withTenantSurfacesEnabled(t)
	e := setup(t, api.PlanPro)
	ctx := context.Background()
	linkedDeployment := mustSeedDeployment(t, e, "tenant-exclusive-linked")
	unlinkedDeployment := mustSeedDeployment(t, e, "tenant-exclusive-unlinked")
	for _, deployment := range []state.Deployment{linkedDeployment, unlinkedDeployment} {
		if err := e.store.MarkDeploymentLive(ctx, deployment.ID); err != nil {
			t.Fatal(err)
		}
	}
	linked, err := e.store.AppByID(ctx, linkedDeployment.AppID)
	if err != nil {
		t.Fatal(err)
	}
	unlinked, err := e.store.AppByID(ctx, unlinkedDeployment.AppID)
	if err != nil {
		t.Fatal(err)
	}
	tenant, _, err := e.store.CreatePlatformTenant(ctx, e.acct.ID, "exclusive-customer", "Exclusive customer", 20)
	if err != nil {
		t.Fatal(err)
	}
	limits := api.MustLimitsFor(e.acct.Plan)
	surface, err := e.store.CreateTenantSurfaceIfUnderQuota(ctx, state.CreateTenantSurfaceParams{
		AccountID: e.acct.ID, AppID: linked.ID, Name: "exclusive-surface",
	}, limits)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.store.UpdateTenantSurfaceStatus(ctx, surface.ID, state.SurfaceStatusActive); err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.LinkPlatformTenantSurface(ctx, e.acct.ID, tenant.ID, surface.ID); err != nil {
		t.Fatal(err)
	}
	policy := exclusivework.Policy{Name: "crm-sync", Scope: "platform_tenant", MemberAppIDs: []string{linked.ID, unlinked.ID},
		Contention: "queue", LeaseSeconds: 5, MaxAttemptSeconds: 60}
	if rec := e.do(t, http.MethodPut, "/v1/account/operation-policies/crm-sync", policy, nil); rec.Code != http.StatusOK {
		t.Fatalf("save tenant policy: %d %s", rec.Code, rec.Body)
	}
	created := e.do(t, http.MethodPost, "/v1/account/platform-tenants/"+tenant.ID+"/access-tokens",
		api.CreatePlatformTenantAccessTokenRequest{Name: "operation submitter", Scopes: []string{api.ScopePlatformTenantInvocationsManage, api.ScopePlatformTenantInvocationsRead}}, nil)
	if created.Code != http.StatusCreated {
		t.Fatalf("create tenant token: %d %s", created.Code, created.Body)
	}
	var token api.CreatePlatformTenantAccessTokenResponse
	if err := json.Unmarshal(created.Body.Bytes(), &token); err != nil {
		t.Fatal(err)
	}
	post := func(slug string) *httptest.ResponseRecorder {
		t.Helper()
		body, err := json.Marshal(api.ExclusiveOperationRequest{Policy: "crm-sync", Key: json.RawMessage(`"crm-sync"`),
			Invocation: api.InvokeRequest{Method: http.MethodPost, Path: "/sync", Payload: json.RawMessage(`{}`)}})
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodPost, "/v1/platform-tenant-self/apps/"+slug+"/operations", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token.Token)
		req.Header.Set("Idempotency-Key", "tenant-sync-once")
		recorder := httptest.NewRecorder()
		e.h.ServeHTTP(recorder, req)
		return recorder
	}
	acceptedRec := post("tenant-exclusive-linked")
	if acceptedRec.Code != http.StatusAccepted {
		t.Fatalf("tenant submit: %d %s", acceptedRec.Code, acceptedRec.Body)
	}
	var accepted api.ExclusiveOperationAccepted
	if err := json.Unmarshal(acceptedRec.Body.Bytes(), &accepted); err != nil {
		t.Fatal(err)
	}
	wantURL := "/v1/platform-tenant-self/operations/" + accepted.ID
	if accepted.StatusURL != wantURL {
		t.Fatalf("tenant status URL=%q want %q", accepted.StatusURL, wantURL)
	}
	unlinkedRec := post("tenant-exclusive-unlinked")
	if unlinkedRec.Code != http.StatusNotFound {
		t.Fatalf("tenant submitted for an unlinked app: %d %s", unlinkedRec.Code, unlinkedRec.Body)
	}
	readReq := httptest.NewRequest(http.MethodGet, wantURL, nil)
	readReq.Header.Set("Authorization", "Bearer "+token.Token)
	readRec := httptest.NewRecorder()
	e.h.ServeHTTP(readRec, readReq)
	if readRec.Code != http.StatusOK || !strings.Contains(readRec.Body.String(), tenant.ID) {
		t.Fatalf("tenant read: %d %s", readRec.Code, readRec.Body)
	}
}
