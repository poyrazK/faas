package apidserver_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func environmentProjectArchive(t *testing.T, compose string) []byte {
	t.Helper()
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: "compose.yaml", Mode: 0644, Size: int64(len(compose))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte(compose)); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func applyEnvironmentProject(t *testing.T, h http.Handler, key, env, compose string) api.ApplyResponse {
	t.Helper()
	var b bytes.Buffer
	mw := multipart.NewWriter(&b)
	for k, v := range map[string]string{"project_slug": "environment-shop", "environment": env} {
		if err := mw.WriteField(k, v); err != nil {
			t.Fatal(err)
		}
	}
	p, err := mw.CreateFormFile("source", "source.tar.gz")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Write(environmentProjectArchive(t, compose)); err != nil {
		t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", "/v1/projects", &b)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+key)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("apply env=%s: %d %s", env, rec.Code, rec.Body.String())
	}
	var out api.ApplyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	for _, b := range out.Builds {
		if b.Error != "" {
			t.Fatalf("build env=%s: %+v", env, b)
		}
	}
	return out
}

func TestProjectStageCheckpointDoesNotSkipProduction(t *testing.T) {
	spool := t.TempDir()
	t.Setenv("FAAS_SCAN_SPOOL_ROOT", spool)
	t.Setenv("FAAS_SPOOL_ROOT", spool)
	e, _ := apidserver.NewTestServerWithCapturingNotifier(t, api.PlanPro)
	first := "services:\n  web:\n    image: nginx:1.26\n    ports: [\"8080:80\"]\n"
	second := "services:\n  web:\n    image: nginx:1.27\n    ports: [\"8080:80\"]\n"
	baseline := applyEnvironmentProject(t, *e.H(), *e.Key(), "", first)
	if len(baseline.Builds) != 1 {
		t.Fatalf("initial builds=%+v", baseline.Builds)
	}
	store := *e.Store()
	before, err := store.AppByID(t.Context(), baseline.Builds[0].AppID)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(t.Context(), baseline.Builds[0].DeploymentID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateProjectEnvironment(t.Context(), state.ProjectEnvironment{AccountID: e.Acct().ID, ProjectID: baseline.ProjectID, Slug: "staging"}); err != nil {
		t.Fatal(err)
	}
	staged := applyEnvironmentProject(t, *e.H(), *e.Key(), "staging", second)
	if len(staged.Builds) != 1 {
		t.Fatalf("staging builds=%+v", staged.Builds)
	}
	after, err := store.AppByID(t.Context(), before.ID)
	if err != nil || !reflect.DeepEqual(before.Manifest, after.Manifest) {
		t.Fatalf("staging changed production's manifest: err=%v", err)
	}
	stageDeployment, err := store.DeploymentByID(t.Context(), staged.Builds[0].DeploymentID)
	if err != nil {
		t.Fatal(err)
	}
	stageApp, err := state.AppForDeployment(t.Context(), store, stageDeployment)
	if err != nil || stageDeployment.Scope != "staging" || stageApp.Manifest.ProjectImage != "docker.io/library/nginx:1.27" {
		t.Fatalf("staging deployment lost its own image/settings: image=%s scope=%s err=%v", stageApp.Manifest.ProjectImage, stageDeployment.Scope, err)
	}
	production := applyEnvironmentProject(t, *e.H(), *e.Key(), "", second)
	if len(production.Builds) != 1 {
		t.Errorf("production deploy after staging queued %d builds; want 1 for nginx:1.27; baseline production deployment remains %s", len(production.Builds), baseline.Builds[0].DeploymentID)
	}
}

func TestProjectStageOmissionKeepsProductionApp(t *testing.T) {
	spool := t.TempDir()
	t.Setenv("FAAS_SCAN_SPOOL_ROOT", spool)
	t.Setenv("FAAS_SPOOL_ROOT", spool)
	e, _ := apidserver.NewTestServerWithCapturingNotifier(t, api.PlanPro)
	first := "services:\n  web:\n    image: nginx:1.27\n    ports: [\"8080:80\"]\n  legacy:\n    image: nginx:1.27\n    ports: [\"8081:80\"]\n"
	second := "services:\n  web:\n    image: nginx:1.27\n    ports: [\"8080:80\"]\n"
	baseline := applyEnvironmentProject(t, *e.H(), *e.Key(), "", first)
	store := *e.Store()
	var legacy string
	for _, b := range baseline.Builds {
		if err := store.MarkDeploymentLive(t.Context(), b.DeploymentID); err != nil {
			t.Fatal(err)
		}
		app, err := store.AppByID(t.Context(), b.AppID)
		if err != nil {
			t.Fatal(err)
		}
		if app.WorkloadName == "legacy" {
			legacy = app.ID
		}
	}
	if legacy == "" {
		t.Fatal("no legacy app")
	}
	if _, err := store.CreateProjectEnvironment(t.Context(), state.ProjectEnvironment{AccountID: e.Acct().ID, ProjectID: baseline.ProjectID, Slug: "staging"}); err != nil {
		t.Fatal(err)
	}
	req := scanAuditRegressionRequest(t, *e.Key(), environmentProjectArchive(t, second), map[string]string{"project_slug": "environment-shop", "environment": "staging"})
	rec := httptest.NewRecorder()
	(*e.H()).ServeHTTP(rec, req)
	var plan api.PlanResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &plan); err != nil || rec.Code != http.StatusOK || len(plan.Removed) != 0 {
		t.Fatalf("stage preview advertised global removals: status=%d removed=%v err=%v", rec.Code, plan.Removed, err)
	}
	applyEnvironmentProject(t, *e.H(), *e.Key(), "staging", second)
	app, err := store.AppByID(t.Context(), legacy)
	if err != nil || app.Status == state.AppDeleted {
		t.Errorf("staging removed production app globally: status=%q err=%v", app.Status, err)
	}
}

func TestProjectStageAdditionDoesNotReplaceExistingProductionConfiguration(t *testing.T) {
	spool := t.TempDir()
	t.Setenv("FAAS_SCAN_SPOOL_ROOT", spool)
	t.Setenv("FAAS_SPOOL_ROOT", spool)
	e, _ := apidserver.NewTestServerWithCapturingNotifier(t, api.PlanPro)
	first := "services:\n  web:\n    image: nginx:1.26\n    ports: [\"8080:80\"]\n"
	second := "services:\n  web:\n    image: nginx:1.27\n    ports: [\"8080:80\"]\n    mem_limit: 512m\n  backend:\n    image: nginx:1.27\n    ports: [\"8081:80\"]\n"
	baseline := applyEnvironmentProject(t, *e.H(), *e.Key(), "", first)
	store := *e.Store()
	if _, err := store.CreateProjectEnvironment(t.Context(), state.ProjectEnvironment{AccountID: e.Acct().ID, ProjectID: baseline.ProjectID, Slug: "staging"}); err != nil {
		t.Fatal(err)
	}
	before, err := store.AppByID(t.Context(), baseline.Builds[0].AppID)
	if err != nil {
		t.Fatal(err)
	}
	staged := applyEnvironmentProject(t, *e.H(), *e.Key(), "staging", second)
	if len(staged.Builds) != 2 {
		t.Fatalf("stage did not deploy both selected services: %+v", staged.Builds)
	}
	after, err := store.AppByID(t.Context(), before.ID)
	if err != nil || before.RAMMB != after.RAMMB || !reflect.DeepEqual(before.Manifest, after.Manifest) {
		t.Fatalf("stage addition changed production settings: err=%v", err)
	}
	for _, build := range staged.Builds {
		deployment, err := store.DeploymentByID(t.Context(), build.DeploymentID)
		if err != nil {
			t.Fatal(err)
		}
		app, err := state.AppForDeployment(t.Context(), store, deployment)
		if err != nil || app.Manifest.ProjectImage != "docker.io/library/nginx:1.27" {
			t.Fatalf("stage runtime settings missing: err=%v", err)
		}
		if app.ID == before.ID && app.RAMMB != 512 {
			t.Fatalf("stage lost its resource override: RAM=%d", app.RAMMB)
		}
	}
}

func TestProjectStageOmissionsDoNotRefundSharedAppQuota(t *testing.T) {
	spool := t.TempDir()
	t.Setenv("FAAS_SCAN_SPOOL_ROOT", spool)
	t.Setenv("FAAS_SPOOL_ROOT", spool)
	e, _ := apidserver.NewTestServerWithCapturingNotifier(t, api.PlanFree)
	first := "services:\n  web:\n    image: nginx:1.27\n    ports: [\"8080:80\"]\n"
	second := "services:\n  backend:\n    image: nginx:1.27\n    ports: [\"8080:80\"]\n"
	baseline := applyEnvironmentProject(t, *e.H(), *e.Key(), "", first)
	if _, err := (*e.Store()).CreateProjectEnvironment(t.Context(), state.ProjectEnvironment{AccountID: e.Acct().ID, ProjectID: baseline.ProjectID, Slug: "staging"}); err != nil {
		t.Fatal(err)
	}
	req := scanAuditRegressionRequest(t, *e.Key(), environmentProjectArchive(t, second), map[string]string{"project_slug": "environment-shop", "environment": "staging"})
	rec := httptest.NewRecorder()
	(*e.H()).ServeHTTP(rec, req)
	var plan api.PlanResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &plan); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("scan: status=%d err=%v", rec.Code, err)
	}
	if plan.CanApply || len(plan.Removed) != 0 {
		t.Fatal("stage omission incorrectly refunded capacity for an existing production app")
	}
}

func TestProjectProductionOmissionKeepsStageOnlyServiceAndQuota(t *testing.T) {
	spool := t.TempDir()
	t.Setenv("FAAS_SCAN_SPOOL_ROOT", spool)
	t.Setenv("FAAS_SPOOL_ROOT", spool)
	e, _ := apidserver.NewTestServerWithCapturingNotifier(t, api.PlanHobby)
	first := "services:\n  web:\n    image: nginx:1.26\n    ports: [\"8080:80\"]\n"
	baseline := applyEnvironmentProject(t, *e.H(), *e.Key(), "", first)
	store := *e.Store()
	if _, err := store.CreateProjectEnvironment(t.Context(), state.ProjectEnvironment{AccountID: e.Acct().ID, ProjectID: baseline.ProjectID, Slug: "staging"}); err != nil {
		t.Fatal(err)
	}
	staged := applyEnvironmentProject(t, *e.H(), *e.Key(), "staging", "services:\n  experimental:\n    image: nginx:1.27\n    ports: [\"8080:80\"]\n")
	if len(staged.Builds) != 1 {
		t.Fatalf("stage builds: %+v", staged.Builds)
	}
	stage := staged.Builds[0]
	if err := store.MarkDeploymentLive(t.Context(), stage.DeploymentID); err != nil {
		t.Fatal(err)
	}
	rescan := func(compose string) api.PlanResponse {
		t.Helper()
		req := scanAuditRegressionRequest(t, *e.Key(), environmentProjectArchive(t, compose), map[string]string{"project_slug": "environment-shop"})
		rec := httptest.NewRecorder()
		(*e.H()).ServeHTTP(rec, req)
		var plan api.PlanResponse
		if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &plan) != nil {
			t.Fatalf("scan: %d %s", rec.Code, rec.Body)
		}
		return plan
	}
	plan := rescan(first)
	if len(plan.Removed) != 0 || !strings.Contains(strings.Join(plan.Warnings, " "), "preserves omitted services") {
		t.Fatalf("stage-only service was not preserved in plan: %+v", plan)
	}
	applyEnvironmentProject(t, *e.H(), *e.Key(), "", first)
	app, err := store.AppByID(t.Context(), stage.AppID)
	if err != nil || app.Status == state.AppDeleted {
		t.Fatalf("production removed staging service: %+v %v", app, err)
	}
	deployment, err := store.DeploymentByID(t.Context(), stage.DeploymentID)
	if err != nil || deployment.Status != state.DeployLive {
		t.Fatalf("production changed staging deployment: %+v %v", deployment, err)
	}
	for i := range 3 {
		if _, err := store.CreateApp(t.Context(), state.App{AccountID: e.Acct().ID, Slug: "capacity-" + string(rune('a'+i)), RAMMB: 256}); err != nil {
			t.Fatal(err)
		}
	}
	plan = rescan(first + "  backend:\n    image: nginx:1.27\n    ports: [\"8081:80\"]\n")
	if plan.CanApply || len(plan.Removed) != 0 {
		t.Fatalf("preserved stage-only member refunded app quota: %+v", plan)
	}
}

func TestProjectProductionApplyPinsUpdatedSavedSettings(t *testing.T) {
	spool := t.TempDir()
	t.Setenv("FAAS_SCAN_SPOOL_ROOT", spool)
	t.Setenv("FAAS_SPOOL_ROOT", spool)
	e, _ := apidserver.NewTestServerWithCapturingNotifier(t, api.PlanPro)
	first := "services:\n  web:\n    image: nginx:1.26\n    ports: [\"8080:80\"]\n"
	baseline := applyEnvironmentProject(t, *e.H(), *e.Key(), "", first)
	store := *e.Store()
	app, err := store.AppByID(t.Context(), baseline.Builds[0].AppID)
	if err != nil {
		t.Fatal(err)
	}
	settings, err := state.WorkloadSettingsFromApp(app)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.PutProjectEnvironmentWorkloadSpec(t.Context(), e.Acct().ID, baseline.ProjectID, "production", app.ID, 0, settings); err != nil {
		t.Fatal(err)
	}
	second := "services:\n  web:\n    image: nginx:1.27\n    ports: [\"8080:8080\"]\n    mem_limit: 512m\n"
	applied := applyEnvironmentProject(t, *e.H(), *e.Key(), "", second)
	if len(applied.Builds) != 1 {
		t.Fatalf("builds=%+v", applied.Builds)
	}
	dep, err := store.DeploymentByID(t.Context(), applied.Builds[0].DeploymentID)
	if err != nil {
		t.Fatal(err)
	}
	pinned, err := state.AppForDeployment(t.Context(), store, dep)
	if err != nil || pinned.RAMMB != 512 || pinned.Manifest.ProjectImagePort != 8080 || pinned.Manifest.ProjectImage != "docker.io/library/nginx:1.27" {
		t.Fatalf("deployment pinned stale production settings: %+v %v", pinned, err)
	}
	current, err := store.AppByID(t.Context(), app.ID)
	if err != nil || current.RAMMB != 512 {
		t.Fatalf("source checkpoint reverted RAM: %d %v", current.RAMMB, err)
	}
}
