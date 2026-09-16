// project_environment_promotion_e2e_test.go — general-path acceptance for
// project environment promotion and rollback.
//
// The promotion implementation has direct handler tests, but this surface
// must also be exercised through a real apid process and PgStore. In
// particular, the HTTP route installs MFA/scope middleware, the preview token
// is recomputed from durable state, and the promotion creates target-scoped
// deployments and sidecar rows in PostgreSQL.
//
// Build tag: (none). CI-safe. No schedd, vmmd, or KVM is required: source
// deployment rows and immutable artifact metadata are seeded at the state
// boundary, while every customer-facing operation uses the real HTTP API.

package e2e_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/state"
)

type projectEnvironmentPromotionFixture struct {
	h            *e2etest.Harness
	store        *state.PgStore
	key          string
	project      state.Project
	apiApp       state.App
	workerApp    state.App
	apiSource    state.Deployment
	workerSource state.Deployment
	apiTarget    state.Deployment
}

func newProjectEnvironmentPromotionFixture(t *testing.T, label string) *projectEnvironmentPromotionFixture {
	t.Helper()
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return nil
	}
	if err := dbMigrateUp(t, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	h := e2etest.Start(t, pool, e2etest.APID)
	ctx := context.Background()
	key := h.SeedAccount(ctx, api.PlanPro, label)
	store := state.NewPgStore(pool)
	account, err := store.AccountByEmail(ctx, "e2e+pro+"+label+"@test.example")
	if err != nil {
		t.Fatalf("load seeded account: %v", err)
	}
	project, err := store.CreateProject(ctx, state.Project{
		AccountID:        account.ID,
		Slug:             "promotion-" + label,
		ProductionBranch: "main",
		ScanSource:       state.ProjectScanSourceConvention,
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	apiApp := createPromotionApp(t, store, account.ID, project.ID, "frontend")
	workerApp := createPromotionApp(t, store, account.ID, project.ID, "worker")

	stagingPath := "/v1/projects/" + project.Slug + "/environments"
	if raw, status := doReq(t, h, key, http.MethodPost, stagingPath, api.CreateProjectEnvironmentRequest{Slug: "staging"}, map[string]string{"Idempotency-Key": "promotion-route-create-staging"}); status != http.StatusCreated {
		t.Fatalf("create staging environment: status=%d body=%s", status, raw)
	}
	putPromotionConfig(t, h, key, project.Slug, "staging", "promotion-staging-config", `{"region":"eu","release":"candidate"}`)
	putPromotionConfig(t, h, key, project.Slug, "production", "promotion-production-config", `{"region":"us","release":"stable"}`)

	apiSource := createPromotionDeployment(t, store, apiApp.ID, "staging", strings.Repeat("1", 64), "1", "/artifacts/api-v1.ext4")
	workerSource := createPromotionDeployment(t, store, workerApp.ID, "staging", strings.Repeat("2", 64), "2", "/artifacts/worker-v1.ext4")
	apiTarget := createPromotionDeployment(t, store, apiApp.ID, "production", strings.Repeat("3", 64), "3", "/artifacts/api-old.ext4")
	if _, err := store.SetDeploymentSidecarLayer(ctx, state.DeploymentSidecarLayer{
		DeploymentID: apiSource.ID, SidecarName: "logger", StorageKey: "sidecars/logger.ext4",
		Bytes: 23, ContentDigest: "sha256:" + strings.Repeat("a", 64),
	}); err != nil {
		t.Fatalf("seed source sidecar: %v", err)
	}

	return &projectEnvironmentPromotionFixture{
		h: h, store: store, key: key, project: project,
		apiApp: apiApp, workerApp: workerApp,
		apiSource: apiSource, workerSource: workerSource, apiTarget: apiTarget,
	}
}

func createPromotionApp(t *testing.T, store *state.PgStore, accountID, projectID, slug string) state.App {
	t.Helper()
	app, err := store.CreateApp(context.Background(), state.App{
		AccountID: accountID, ProjectID: projectID, Slug: slug,
		Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1,
		Status: state.AppActive, WorkloadName: slug,
	})
	if err != nil {
		t.Fatalf("create %s app: %v", slug, err)
	}
	return app
}

func createPromotionDeployment(t *testing.T, store *state.PgStore, appID, scope, sourceSHA, digestByte, rootfsKey string) state.Deployment {
	t.Helper()
	ctx := context.Background()
	deployment, err := store.CreateDeployment(ctx, state.Deployment{
		AppID: appID, Scope: scope, Kind: state.DeploymentKindImage,
		ImageDigest:  "sha256:" + strings.Repeat(digestByte, 64),
		SourceSHA256: sourceSHA, Status: state.DeployPending,
	})
	if err != nil {
		t.Fatalf("create %s deployment: %v", scope, err)
	}
	if err := store.SetDeploymentRootfs(ctx, deployment.ID, "/artifacts/"+deployment.ID+".ext4", rootfsKey, 4096); err != nil {
		t.Fatalf("set %s deployment artifact: %v", scope, err)
	}
	if err := store.MarkDeploymentLive(ctx, deployment.ID); err != nil {
		t.Fatalf("mark %s deployment live: %v", scope, err)
	}
	deployment, err = store.DeploymentByID(ctx, deployment.ID)
	if err != nil {
		t.Fatalf("reload %s deployment: %v", scope, err)
	}
	return deployment
}

func putPromotionConfig(t *testing.T, h *e2etest.Harness, key, projectSlug, environment, idempotencyKey, values string) {
	t.Helper()
	body := api.UpdateProjectEnvironmentConfigRequest{Values: json.RawMessage(values)}
	path := "/v1/projects/" + projectSlug + "/environments/" + environment + "/config"
	raw, status := doReq(t, h, key, http.MethodPut, path, body, map[string]string{"Idempotency-Key": idempotencyKey})
	if status != http.StatusOK {
		t.Fatalf("PUT %s: status=%d body=%s", path, status, raw)
	}
}

func promotionChangeBySlug(t *testing.T, changes []api.ProjectEnvironmentPromotionChange, slug string) api.ProjectEnvironmentPromotionChange {
	t.Helper()
	for _, change := range changes {
		if change.WorkloadSlug == slug {
			return change
		}
	}
	t.Fatalf("promotion change %q not found: %+v", slug, changes)
	return api.ProjectEnvironmentPromotionChange{}
}

func promotionWorkloadBySlug(t *testing.T, workloads []api.ProjectEnvironmentPromotionWorkloadResponse, slug string) api.ProjectEnvironmentPromotionWorkloadResponse {
	t.Helper()
	for _, workload := range workloads {
		if workload.WorkloadSlug == slug {
			return workload
		}
	}
	t.Fatalf("promotion workload %q not found: %+v", slug, workloads)
	return api.ProjectEnvironmentPromotionWorkloadResponse{}
}

func promotionStatusWorkloadBySlug(t *testing.T, workloads []api.ProjectEnvironmentPromotionStatusWorkloadResponse, slug string) api.ProjectEnvironmentPromotionStatusWorkloadResponse {
	t.Helper()
	for _, workload := range workloads {
		if workload.WorkloadSlug == slug {
			return workload
		}
	}
	t.Fatalf("promotion status workload %q not found: %+v", slug, workloads)
	return api.ProjectEnvironmentPromotionStatusWorkloadResponse{}
}

func assertPromotionProblem(t *testing.T, h *e2etest.Harness, key, method, path string, body any, idempotencyKey string, wantStatus int, wantCode string) {
	t.Helper()
	raw, status := doReq(t, h, key, method, path, body, map[string]string{"Idempotency-Key": idempotencyKey})
	if status != wantStatus {
		t.Fatalf("status=%d want %d: %s", status, wantStatus, raw)
	}
	var problem api.Problem
	if err := json.Unmarshal(raw, &problem); err != nil {
		t.Fatalf("decode problem: %v (body=%s)", err, raw)
	}
	if problem.Code != wantCode {
		t.Fatalf("code=%q want %q (body=%s)", problem.Code, wantCode, raw)
	}
}

func reportPromotionE2EFailure(t *testing.T, stage *string) {
	t.Helper()
	t.Cleanup(func() {
		if t.Failed() {
			fmt.Printf("::error title=promotion E2E failure::failed during %s\n", *stage)
		}
	})
}

// TestE2E_ProjectEnvironmentPromotion_RouteApprovalCopyAndRollback covers the
// full successful operation through real apid HTTP routes. It deliberately
// includes both promotion shapes: api replaces an existing production
// deployment and worker creates its first production deployment.
func TestE2E_ProjectEnvironmentPromotion_RouteApprovalCopyAndRollback(t *testing.T) {
	stage := "fixture setup"
	reportPromotionE2EFailure(t, &stage)
	f := newProjectEnvironmentPromotionFixture(t, "route")
	if f == nil {
		return
	}
	ctx := context.Background()
	stage = "promotion preview"
	previewPath := "/v1/projects/" + f.project.Slug + "/environments/production/promotion-preview?from=staging"
	raw, status := doReq(t, f.h, f.key, http.MethodGet, previewPath, nil)
	if status != http.StatusOK {
		t.Fatalf("promotion preview: status=%d body=%s", status, raw)
	}
	var preview api.ProjectEnvironmentPromotionPreviewResponse
	if err := json.Unmarshal(raw, &preview); err != nil {
		t.Fatalf("decode promotion preview: %v body=%s", err, raw)
	}
	if !preview.ToEnvironmentProtected || !preview.ApprovalRequired || !preview.CanPromote {
		t.Fatalf("promotion eligibility=%+v", preview)
	}
	if len(preview.ConfigDiff.Changes) != 2 {
		t.Fatalf("config diff=%+v, want region and release changes", preview.ConfigDiff)
	}
	apiChange := promotionChangeBySlug(t, preview.Changes, "frontend")
	workerChange := promotionChangeBySlug(t, preview.Changes, "worker")
	if apiChange.Kind != "update" || apiChange.SourceDeploymentID != f.apiSource.ID || apiChange.TargetDeploymentID != f.apiTarget.ID {
		t.Fatalf("api promotion change=%+v", apiChange)
	}
	if workerChange.Kind != "create" || workerChange.SourceDeploymentID != f.workerSource.ID || workerChange.TargetDeploymentID != "" {
		t.Fatalf("worker promotion change=%+v", workerChange)
	}
	if preview.PromotionHash == "" || preview.PromotionToken == "" {
		t.Fatalf("preview omitted promotion identity: %+v", preview)
	}

	stage = "approval and promotion"
	promotePath := "/v1/projects/" + f.project.Slug + "/environments/production/promote"
	promoteRequest := api.PromoteProjectEnvironmentRequest{
		FromEnvironment: "staging", PromotionToken: preview.PromotionToken,
	}
	assertPromotionProblem(t, f.h, f.key, http.MethodPost, promotePath, promoteRequest,
		"promotion-route-no-approval", http.StatusConflict, api.CodeProjectEnvironmentApprovalRequired)

	raw, status = doReq(t, f.h, f.key, http.MethodPost,
		"/v1/projects/"+f.project.Slug+"/environments/production/approvals",
		api.CreateProjectEnvironmentApprovalRequest{PromotionToken: preview.PromotionToken},
		map[string]string{"Idempotency-Key": "promotion-route-approval"})
	if status != http.StatusCreated {
		t.Fatalf("create promotion approval: status=%d body=%s", status, raw)
	}
	var approval api.ProjectEnvironmentApprovalResponse
	if err := json.Unmarshal(raw, &approval); err != nil {
		t.Fatalf("decode promotion approval: %v body=%s", err, raw)
	}
	if approval.ApprovalToken == "" || approval.Environment != "production" || approval.ExpiresAt == "" {
		t.Fatalf("approval response=%+v", approval)
	}
	promoteRequest.ApprovalToken = approval.ApprovalToken
	raw, status = doReq(t, f.h, f.key, http.MethodPost, promotePath, promoteRequest,
		map[string]string{"Idempotency-Key": "promotion-route-1"})
	if status != http.StatusOK {
		t.Fatalf("promote: status=%d body=%s", status, raw)
	}
	firstPromotionBody := append([]byte(nil), raw...)
	var promoted api.ProjectEnvironmentPromotionResponse
	if err := json.Unmarshal(raw, &promoted); err != nil {
		t.Fatalf("decode promotion response: %v body=%s", err, raw)
	}
	if promoted.PromotionID == "" || promoted.PromotionHash != preview.PromotionHash || len(promoted.Workloads) != 2 {
		t.Fatalf("promotion response=%+v", promoted)
	}
	apiResult := promotionWorkloadBySlug(t, promoted.Workloads, "frontend")
	workerResult := promotionWorkloadBySlug(t, promoted.Workloads, "worker")
	if apiResult.Status != "promoted" || apiResult.TargetDeploymentID == "" || apiResult.TargetDeploymentID == f.apiTarget.ID {
		t.Fatalf("api promotion result=%+v", apiResult)
	}
	if workerResult.Status != "promoted" || workerResult.TargetDeploymentID == "" {
		t.Fatalf("worker promotion result=%+v", workerResult)
	}

	apiPromoted, err := f.store.DeploymentByID(ctx, apiResult.TargetDeploymentID)
	if err != nil {
		t.Fatalf("load promoted api deployment: %v", err)
	}
	workerPromoted, err := f.store.DeploymentByID(ctx, workerResult.TargetDeploymentID)
	if err != nil {
		t.Fatalf("load promoted worker deployment: %v", err)
	}
	if apiPromoted.Status != state.DeployLive || workerPromoted.Status != state.DeployLive {
		t.Fatalf("promoted statuses: api=%q worker=%q", apiPromoted.Status, workerPromoted.Status)
	}
	if apiPromoted.Scope != "production" || apiPromoted.SourceSHA256 != f.apiSource.SourceSHA256 || apiPromoted.RootfsKey != f.apiSource.RootfsKey {
		t.Fatalf("promoted api artifact=%+v source=%+v", apiPromoted, f.apiSource)
	}
	if workerPromoted.Scope != "production" || workerPromoted.SourceSHA256 != f.workerSource.SourceSHA256 || workerPromoted.RootfsKey != f.workerSource.RootfsKey {
		t.Fatalf("promoted worker artifact=%+v source=%+v", workerPromoted, f.workerSource)
	}
	oldTarget, err := f.store.DeploymentByID(ctx, f.apiTarget.ID)
	if err != nil {
		t.Fatalf("load old api target: %v", err)
	}
	if oldTarget.Status != state.DeploySuperseded {
		t.Fatalf("old api target status=%q, want superseded", oldTarget.Status)
	}
	layers, err := f.store.ListDeploymentSidecarLayers(ctx, apiPromoted.ID)
	if err != nil {
		t.Fatalf("list promoted sidecars: %v", err)
	}
	if len(layers) != 1 || layers[0].SidecarName != "logger" || layers[0].StorageKey != "sidecars/logger.ext4" || layers[0].ContentDigest != "sha256:"+strings.Repeat("a", 64) {
		t.Fatalf("promoted sidecars=%+v", layers)
	}

	stage = "promotion idempotency replay"
	raw, status = doReq(t, f.h, f.key, http.MethodPost, promotePath, promoteRequest,
		map[string]string{"Idempotency-Key": "promotion-route-1"})
	if status != http.StatusOK || string(raw) != string(firstPromotionBody) {
		t.Fatalf("promotion replay: status=%d body=%s want=%s", status, raw, firstPromotionBody)
	}
	apiDeployments, err := f.store.ListDeploymentsForApp(ctx, f.apiApp.ID, 0, 0)
	if err != nil {
		t.Fatalf("list api deployments after replay: %v", err)
	}
	workerDeployments, err := f.store.ListDeploymentsForApp(ctx, f.workerApp.ID, 0, 0)
	if err != nil {
		t.Fatalf("list worker deployments after replay: %v", err)
	}
	if len(apiDeployments) != 3 || len(workerDeployments) != 2 {
		t.Fatalf("idempotent replay created deployments: api=%d worker=%d", len(apiDeployments), len(workerDeployments))
	}

	stage = "promotion status verification"
	statusPath := "/v1/projects/" + f.project.Slug + "/environments/production/promotions/" + promoted.PromotionID
	raw, status = doReq(t, f.h, f.key, http.MethodGet, statusPath, nil)
	if status != http.StatusOK {
		t.Fatalf("promotion status: status=%d body=%s", status, raw)
	}
	var promotionStatus api.ProjectEnvironmentPromotionStatusResponse
	if err := json.Unmarshal(raw, &promotionStatus); err != nil {
		t.Fatalf("decode promotion status: %v body=%s", err, raw)
	}
	if promotionStatus.Status != "succeeded" || promotionStatus.VerificationStatus != "verified" {
		t.Fatalf("promotion status=%+v", promotionStatus)
	}
	if got := promotionStatusWorkloadBySlug(t, promotionStatus.Workloads, "frontend"); got.VerificationStatus != "verified" {
		t.Fatalf("api verification=%+v", got)
	}
	if got := promotionStatusWorkloadBySlug(t, promotionStatus.Workloads, "worker"); got.VerificationStatus != "verified" {
		t.Fatalf("worker verification=%+v", got)
	}

	stage = "promotion rollback"
	rollbackPath := statusPath + "/rollback"
	raw, status = doReq(t, f.h, f.key, http.MethodPost, rollbackPath, nil,
		map[string]string{"Idempotency-Key": "promotion-route-rollback-1"})
	if status != http.StatusOK {
		t.Fatalf("rollback: status=%d body=%s", status, raw)
	}
	firstRollbackBody := append([]byte(nil), raw...)
	var rollbackStatus api.ProjectEnvironmentPromotionStatusResponse
	if err := json.Unmarshal(raw, &rollbackStatus); err != nil {
		t.Fatalf("decode rollback status: %v body=%s", err, raw)
	}
	if rollbackStatus.RollbackStatus != "rolled_back" {
		t.Fatalf("rollback status=%+v", rollbackStatus)
	}
	if got := promotionStatusWorkloadBySlug(t, rollbackStatus.Workloads, "frontend"); got.RollbackStatus != "restored" || got.RestoredTargetDeploymentID != f.apiTarget.ID {
		t.Fatalf("api rollback=%+v", got)
	}
	if got := promotionStatusWorkloadBySlug(t, rollbackStatus.Workloads, "worker"); got.RollbackStatus != "cleared" {
		t.Fatalf("worker rollback=%+v", got)
	}
	apiLive, err := f.store.LiveDeploymentForScope(ctx, f.apiApp.ID, "production")
	if err != nil {
		t.Fatalf("load restored api deployment: %v", err)
	}
	if apiLive.ID != f.apiTarget.ID {
		t.Fatalf("restored api deployment=%s want=%s", apiLive.ID, f.apiTarget.ID)
	}
	if _, err := f.store.LiveDeploymentForScope(ctx, f.workerApp.ID, "production"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("worker live deployment after rollback: err=%v, want not found", err)
	}
	stage = "rollback idempotency replay"
	raw, status = doReq(t, f.h, f.key, http.MethodPost, rollbackPath, nil,
		map[string]string{"Idempotency-Key": "promotion-route-rollback-1"})
	if status != http.StatusOK || string(raw) != string(firstRollbackBody) {
		t.Fatalf("rollback replay: status=%d body=%s want=%s", status, raw, firstRollbackBody)
	}
}

// TestE2E_ProjectEnvironmentPromotion_StalePreviewRejected protects the
// check between preview and execution. A source release change must reject
// the old token before any target deployment or promotion record is created.
func TestE2E_ProjectEnvironmentPromotion_StalePreviewRejected(t *testing.T) {
	stage := "fixture setup"
	reportPromotionE2EFailure(t, &stage)
	f := newProjectEnvironmentPromotionFixture(t, "stale")
	if f == nil {
		return
	}
	ctx := context.Background()
	stage = "stale preview rejection"
	previewPath := "/v1/projects/" + f.project.Slug + "/environments/production/promotion-preview?from=staging"
	raw, status := doReq(t, f.h, f.key, http.MethodGet, previewPath, nil)
	if status != http.StatusOK {
		t.Fatalf("promotion preview: status=%d body=%s", status, raw)
	}
	var preview api.ProjectEnvironmentPromotionPreviewResponse
	if err := json.Unmarshal(raw, &preview); err != nil {
		t.Fatalf("decode promotion preview: %v body=%s", err, raw)
	}

	// Publish a new source release after the preview. This changes the
	// promotion hash while leaving the old token otherwise well-formed.
	createPromotionDeployment(t, f.store, f.apiApp.ID, "staging", strings.Repeat("4", 64), "4", "/artifacts/api-v2.ext4")
	promotePath := "/v1/projects/" + f.project.Slug + "/environments/production/promote"
	assertPromotionProblem(t, f.h, f.key, http.MethodPost, promotePath, api.PromoteProjectEnvironmentRequest{
		FromEnvironment: "staging", PromotionToken: preview.PromotionToken,
	}, "promotion-stale-preview", http.StatusConflict, api.CodeProjectEnvironmentApprovalInvalid)

	if _, err := f.store.LiveDeploymentForScope(ctx, f.workerApp.ID, "production"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("stale preview created worker target: err=%v", err)
	}
	var promotionCount int
	if err := f.h.Pool.QueryRow(ctx, `select count(*) from project_environment_promotions where project_slug = $1`, f.project.Slug).Scan(&promotionCount); err != nil {
		t.Fatalf("count stale promotions: %v", err)
	}
	if promotionCount != 0 {
		t.Fatalf("stale preview created %d promotion records", promotionCount)
	}
}
