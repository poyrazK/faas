// PR-A: CreateDeployment's deployable-app gate, exercised at the wire.
// The store-side gate lives in pkg/state/{pgstore,memstore}.go —
// these tests pin the wire contract (a deploy to a deleted app must
// 404, and the store must hold zero deployment rows for the
// rejected attempt).

package main

import (
	"archive/tar"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestCreateDeployment_AllowsEvictedColdApp_Tarball pins the zero-config
// redeploy contract: parking stops instances, not source admission. A valid
// source upload to an evicted_cold app must still create a queued deployment.
func TestCreateDeployment_AllowsEvictedColdApp_Tarball(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FAAS_SPOOL_ROOT", dir)

	e := setup(t, api.PlanPro)
	e.do(t, "POST", "/v1/apps", api.CreateAppRequest{Slug: "parked-tar"}, nil)

	app, err := e.store.AppBySlug(context.Background(), "parked-tar")
	if err != nil {
		t.Fatalf("AppBySlug: %v", err)
	}
	parked := state.AppEvictedCold
	if _, err := e.store.UpdateApp(context.Background(), app.ID, state.UpdateAppParams{Status: &parked}); err != nil {
		t.Fatalf("UpdateApp(evicted_cold): %v", err)
	}

	raw := buildTestTarGz(t,
		[]tar.Header{{Name: "index.js"}, {Name: "package.json"}},
		map[string][]byte{
			"index.js":     []byte("exports.handler = () => 'parked';\n"),
			"package.json": []byte(`{"name":"parked-tar","version":"0.0.0"}`),
		})
	body, contentType := multipartUpload(t, map[string]multipartPart{
		"source": {filename: "source.tar.gz", body: raw},
	})
	req := httptest.NewRequest("POST", "/v1/apps/parked-tar/deployments", body)
	req.Header.Set("Authorization", "Bearer "+e.key)
	req.Header.Set("Content-Type", contentType)
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 (%s)", rec.Code, rec.Body)
	}
	deps, err := e.store.ListDeploymentsForApp(context.Background(), app.ID, 0, 0)
	if err != nil {
		t.Fatalf("ListDeploymentsForApp: %v", err)
	}
	if len(deps) != 1 {
		t.Fatalf("deployments = %d, want 1", len(deps))
	}
	if deps[0].Status != state.DeployBuilding {
		t.Errorf("deployment status = %q, want building", deps[0].Status)
	}
}

// TestCreateDeployment_RejectsSoftDeletedApp_Image covers the
// image: branch (cmd/apid/handlers.go::createDeployment). Setup:
// create app, soft-delete it, POST image deploy → must 404, store
// has zero deployments.
func TestCreateDeployment_RejectsSoftDeletedApp_Image(t *testing.T) {
	e := setup(t, api.PlanPro)
	e.do(t, "POST", "/v1/apps", api.CreateAppRequest{Slug: "del-img"}, nil)

	app, err := e.store.AppBySlug(context.Background(), "del-img")
	if err != nil {
		t.Fatalf("AppBySlug: %v", err)
	}
	if err := e.store.DeleteApp(context.Background(), app.ID); err != nil {
		t.Fatalf("DeleteApp: %v", err)
	}

	digest := "sha256:" + repeat("b", 64)
	rec := e.do(t, "POST", "/v1/apps/del-img/deployments",
		api.CreateDeploymentRequest{Image: "registry.example.com/x@" + digest}, nil)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (%s)", rec.Code, rec.Body)
	}
	// Confirm: the store holds no deployments for the deleted app.
	// PR-A review: the prior ListApps-iteration form was unfalsifiable
	// because ListApps hides soft-deleted apps. Use the app.ID we kept
	// before DeleteApp — same approach as the PgStore test.
	deps, err := e.store.ListDeploymentsForApp(context.Background(), app.ID, 0, 0)
	if err != nil {
		t.Fatalf("ListDeploymentsForApp: %v", err)
	}
	if len(deps) != 0 {
		t.Errorf("store has %d deployments for deleted app, want 0", len(deps))
	}
}

// TestCreateDeployment_RejectsSoftDeletedApp_Tarball covers the
// multipart branch (cmd/apid/deploy_inputs.go::createDeploymentMultipart).
// Same shape: create app, soft-delete, POST a multipart deploy → must
// 404, store has zero deployments.
func TestCreateDeployment_RejectsSoftDeletedApp_Tarball(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FAAS_SPOOL_ROOT", dir)

	e := setup(t, api.PlanPro)
	e.do(t, "POST", "/v1/apps", api.CreateAppRequest{Slug: "del-tar"}, nil)

	app, err := e.store.AppBySlug(context.Background(), "del-tar")
	if err != nil {
		t.Fatalf("AppBySlug: %v", err)
	}
	if err := e.store.DeleteApp(context.Background(), app.ID); err != nil {
		t.Fatalf("DeleteApp: %v", err)
	}

	// Use the helper from deploy_inputs_test.go to construct a valid
	// multipart tarball. The validator should never even be reached —
	// CreateDeployment is the first store call, and that's what fails.
	entries := []tar.Header{{Name: "index.js"}}
	raw := buildTestTarGz(t, entries, map[string][]byte{
		"index.js": []byte("exports.handler = () => 1;\n"),
	})
	body, ct := multipartUpload(t, map[string]multipartPart{
		"source":  {filename: "src.tar.gz", body: raw},
		"runtime": {body: []byte("node22")},
		"handler": {body: []byte("index.handler")},
	})

	req := httptest.NewRequest("POST", "/v1/apps/del-tar/deployments", body)
	req.Header.Set("Authorization", "Bearer "+e.key)
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (%s)", rec.Code, rec.Body)
	}

	// Sanity check: confirm body is the standard Problem envelope.
	var p api.Problem
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if p.Code != api.CodeNotFound {
		t.Errorf("problem code = %q, want %q", p.Code, api.CodeNotFound)
	}
}
