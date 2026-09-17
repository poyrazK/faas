// deploy_lifecycle_e2e_test.go — focused KVM-free coverage for the
// deploy-to-serve control-plane boundary.
//
// These tests intentionally stop before builderd/imaged/Firecracker. The
// normal-path suite already covers serving and wake behavior; this file pins
// the durable APID + PgStore invariants that decide which deployment is
// allowed to reach that path.
package e2e_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/state"
)

type deployLifecycleFixture struct {
	pool  *pgxpool.Pool
	h     *e2etest.Harness
	key   string
	app   api.AppResponse
	store *state.PgStore
	ctx   context.Context
}

func newDeployLifecycleFixture(t *testing.T, label string) *deployLifecycleFixture {
	t.Helper()
	if os.Getenv("FAAS_SKIP_PG_TESTS") != "" {
		t.Skip("FAAS_SKIP_PG_TESTS set")
	}
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return nil
	}
	if err := dbMigrateUp(t, pool); err != nil {
		t.Fatalf("dbMigrateUp: %v", err)
	}

	ctx := context.Background()
	h := e2etest.Start(t, pool, e2etest.APID)
	key := h.SeedAccount(ctx, api.PlanPro, "deploy-lifecycle-"+label)
	slug := "deploy-lifecycle-" + label
	body, status := doReq(t, h, key, http.MethodPost, "/v1/apps", api.CreateAppRequest{
		Slug: slug, Type: string(state.AppTypeApp),
	})
	if status != http.StatusCreated {
		t.Fatalf("create app: status=%d body=%s", status, body)
	}
	var app api.AppResponse
	if err := json.Unmarshal(body, &app); err != nil {
		t.Fatalf("decode app: %v body=%s", err, body)
	}
	return &deployLifecycleFixture{
		pool: pool, h: h, key: key, app: app,
		store: state.NewPgStore(pool), ctx: ctx,
	}
}

func lifecycleImage(label, fill string) string {
	return "registry.example.com/" + label + "@sha256:" + strings.Repeat(fill, 64)
}

func createLifecycleImageDeployment(t *testing.T, f *deployLifecycleFixture, image, idempotencyKey string) api.DeploymentResponse {
	t.Helper()
	body, status := doReq(t, f.h, f.key, http.MethodPost,
		"/v1/apps/"+f.app.Slug+"/deployments",
		api.CreateDeploymentRequest{Image: image},
		map[string]string{"Idempotency-Key": idempotencyKey})
	if status != http.StatusAccepted {
		t.Fatalf("create deployment: status=%d body=%s", status, body)
	}
	var deployment api.DeploymentResponse
	if err := json.Unmarshal(body, &deployment); err != nil {
		t.Fatalf("decode deployment: %v body=%s", err, body)
	}
	return deployment
}

// TestE2E_DeployLifecycle_ImageAdmissionIsIdempotent pins the customer-facing
// image deploy boundary: a digest-pinned request is admitted as one pending
// row, and a transport retry replays that same operation instead of enqueueing
// a second release.
func TestE2E_DeployLifecycle_ImageAdmissionIsIdempotent(t *testing.T) {
	f := newDeployLifecycleFixture(t, "idempotency")
	if f == nil {
		return
	}

	image := lifecycleImage("idempotency", "a")
	first := createLifecycleImageDeployment(t, f, image, "deploy-lifecycle-idempotency")
	second := createLifecycleImageDeployment(t, f, image, "deploy-lifecycle-idempotency")

	if first.ID == "" || first.ID != second.ID {
		t.Fatalf("idempotent deploy IDs = %q and %q, want the same non-empty ID", first.ID, second.ID)
	}
	if first.Status != string(state.DeployPending) {
		t.Fatalf("new image deployment status = %q, want pending", first.Status)
	}
	if first.Kind != string(state.DeploymentKindImage) || first.ImageDigest != image {
		t.Fatalf("new image deployment shape = kind:%q image:%q", first.Kind, first.ImageDigest)
	}
	if first.BuildID != "" {
		t.Fatalf("image deployment unexpectedly created build %q", first.BuildID)
	}

	var rows int
	if err := f.pool.QueryRow(f.ctx,
		`select count(*) from deployments where app_id = $1`, f.app.ID).Scan(&rows); err != nil {
		t.Fatalf("count deployment rows: %v", err)
	}
	if rows != 1 {
		t.Fatalf("deployment rows after idempotent retry = %d, want 1", rows)
	}
}

// TestE2E_DeployLifecycle_FailedReplacementPreservesLivePredecessor covers
// the no-outage release invariant. A pending replacement must not displace a
// live deployment, and a failed replacement must return all traffic to that
// predecessor while keeping the failed row visible for diagnosis.
func TestE2E_DeployLifecycle_FailedReplacementPreservesLivePredecessor(t *testing.T) {
	f := newDeployLifecycleFixture(t, "replacement")
	if f == nil {
		return
	}

	prior, err := f.store.CreateDeployment(f.ctx, state.Deployment{
		AppID: f.app.ID, Kind: state.DeploymentKindImage,
		ImageDigest: lifecycleImage("replacement-prior", "b"),
	})
	if err != nil {
		t.Fatalf("seed predecessor: %v", err)
	}
	if err := f.store.MarkDeploymentLive(f.ctx, prior.ID); err != nil {
		t.Fatalf("promote predecessor: %v", err)
	}

	// The API must reject an attempt to cancel the only live release. Rollback
	// is the separate control-plane operation for live traffic.
	assertProblem(t, f.h, f.key, http.MethodPost,
		fmt.Sprintf("/v1/apps/%s/deployments/%s/cancel", f.app.Slug, prior.ID),
		nil, http.StatusConflict, api.CodeDeploymentCancelLiveForbidden)

	candidate := createLifecycleImageDeployment(t, f,
		lifecycleImage("replacement-candidate", "c"), "deploy-lifecycle-replacement")
	if candidate.Status != string(state.DeployPending) {
		t.Fatalf("replacement status = %q, want pending", candidate.Status)
	}

	priorBeforeFailure, err := f.store.DeploymentByID(f.ctx, prior.ID)
	if err != nil {
		t.Fatalf("read predecessor before failure: %v", err)
	}
	if priorBeforeFailure.Status != state.DeployLive || priorBeforeFailure.TrafficPercent != 100 {
		t.Fatalf("predecessor while replacement pending = status:%q traffic:%d, want live/100",
			priorBeforeFailure.Status, priorBeforeFailure.TrafficPercent)
	}

	failed, err := f.store.SetDeploymentFailed(f.ctx, candidate.ID, api.CodeImageManifestInvalid, "synthetic manifest failure")
	if err != nil {
		t.Fatalf("fail replacement: %v", err)
	}
	if failed.Status != state.DeployFailed || failed.TrafficPercent != 0 || failed.ErrorCode != api.CodeImageManifestInvalid {
		t.Fatalf("failed replacement = status:%q traffic:%d code:%q", failed.Status, failed.TrafficPercent, failed.ErrorCode)
	}

	priorAfterFailure, err := f.store.LiveDeployment(f.ctx, f.app.ID)
	if err != nil {
		t.Fatalf("read live fallback: %v", err)
	}
	if priorAfterFailure.ID != prior.ID || priorAfterFailure.TrafficPercent != 100 {
		t.Fatalf("live fallback = id:%q traffic:%d, want predecessor %q at 100",
			priorAfterFailure.ID, priorAfterFailure.TrafficPercent, prior.ID)
	}
	var liveRows int
	if err := f.pool.QueryRow(f.ctx,
		`select count(*) from deployments where app_id = $1 and status = 'live'`, f.app.ID).Scan(&liveRows); err != nil {
		t.Fatalf("count live deployments: %v", err)
	}
	if liveRows != 1 {
		t.Fatalf("live deployment rows after failed replacement = %d, want 1", liveRows)
	}
}

// TestE2E_DeployLifecycle_CancelCascadesQueuedBuild pins the cancellation
// boundary through HTTP and the durable cleanup contract: cancelling an
// admitted deployment also cancels its queued build in the same transaction.
func TestE2E_DeployLifecycle_CancelCascadesQueuedBuild(t *testing.T) {
	f := newDeployLifecycleFixture(t, "cancel")
	if f == nil {
		return
	}

	deployment := createLifecycleImageDeployment(t, f,
		lifecycleImage("cancel", "d"), "deploy-lifecycle-cancel-create")
	build, err := f.store.CreateBuild(f.ctx, deployment.ID, state.DeploymentKindTarball, 1024, "/tmp/deploy-lifecycle.log")
	if err != nil {
		t.Fatalf("seed queued build: %v", err)
	}

	path := fmt.Sprintf("/v1/apps/%s/deployments/%s/cancel", f.app.Slug, deployment.ID)
	body, status := doReq(t, f.h, f.key, http.MethodPost, path,
		map[string]string{"reason": string(state.CancelReasonUser)})
	if status != http.StatusOK {
		t.Fatalf("cancel deployment: status=%d body=%s", status, body)
	}
	var cancelResponse struct {
		ID              string   `json:"id"`
		Status          string   `json:"status"`
		CancelledBuilds []string `json:"cancelled_builds"`
	}
	if err := json.Unmarshal(body, &cancelResponse); err != nil {
		t.Fatalf("decode cancel response: %v body=%s", err, body)
	}
	if cancelResponse.ID != deployment.ID || cancelResponse.Status != string(state.DeployCancelled) {
		t.Fatalf("cancel response = id:%q status:%q", cancelResponse.ID, cancelResponse.Status)
	}
	if len(cancelResponse.CancelledBuilds) != 1 || cancelResponse.CancelledBuilds[0] != build.ID {
		t.Fatalf("cancelled build IDs = %v, want [%s]", cancelResponse.CancelledBuilds, build.ID)
	}

	gotDeployment, err := f.store.DeploymentByID(f.ctx, deployment.ID)
	if err != nil {
		t.Fatalf("read cancelled deployment: %v", err)
	}
	if gotDeployment.Status != state.DeployCancelled || gotDeployment.CancelledAt == nil || gotDeployment.CancelReason != string(state.CancelReasonUser) {
		t.Fatalf("cancelled deployment = status:%q at:%v reason:%q", gotDeployment.Status, gotDeployment.CancelledAt, gotDeployment.CancelReason)
	}
	gotBuild, err := f.store.BuildByID(f.ctx, build.ID)
	if err != nil {
		t.Fatalf("read cascaded build: %v", err)
	}
	if gotBuild.Status != state.BuildCancelled || gotBuild.CancelledAt == nil || !gotBuild.CancelledByDeploymentCascade {
		t.Fatalf("cascaded build = status:%q at:%v cascade:%t", gotBuild.Status, gotBuild.CancelledAt, gotBuild.CancelledByDeploymentCascade)
	}
}
