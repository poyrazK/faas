package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/appstandards"
	"github.com/onebox-faas/faas/pkg/state"
)

type standardProjectBoundaryStore interface {
	standardOnboardingBoundaryStore
	state.ApplicationStandardImmediateMaterializationStore
	state.ApplicationStandardStore
	state.ApplicationStandardResourceStore
	state.ApplicationStandardReviewStore
	state.ProjectReconcileStore
}

func standardProjectSource(t *testing.T, names ...string) []byte {
	t.Helper()
	var body bytes.Buffer
	gz := gzip.NewWriter(&body)
	tw := tar.NewWriter(gz)
	for _, name := range names {
		for _, file := range []struct{ path, body string }{
			{"Dockerfile", "FROM alpine\nCMD [\"sleep\", \"infinity\"]\n"},
			{"index.js", "exports.handler = () => '" + name + "';\n"},
		} {
			path := "repo/apps/" + name + "/" + file.path
			if err := tw.WriteHeader(&tar.Header{Name: path, Size: int64(len(file.body)), Mode: 0o644, ModTime: time.Unix(0, 0), Typeflag: tar.TypeReg}); err != nil {
				t.Fatal(err)
			}
			if _, err := tw.Write([]byte(file.body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return body.Bytes()
}

func standardProjectApply(t *testing.T, e standardOnboardingBoundaryEnv, source []byte) api.ApplyResponse {
	t.Helper()
	r := scanAuditRegressionRequest(t, e.key, source, map[string]string{"project_slug": "standard-services"})
	r.URL.Path = "/v1/projects"
	w := httptest.NewRecorder()
	e.handler.ServeHTTP(w, r)
	return standardOnboardingBoundaryResponse[api.ApplyResponse](t, w, http.StatusOK)
}

func standardProjectSetup(t *testing.T, store standardOnboardingBoundaryStore) standardOnboardingBoundaryEnv {
	t.Helper()
	e := newStandardOnboardingBoundary(t, store)
	t.Setenv("FAAS_SPOOL_ROOT", t.TempDir())
	t.Setenv("FAAS_STORAGE_BACKEND", "local")
	if err := store.MarkAccountEmailVerified(t.Context(), e.owner.Account.ID); err != nil {
		t.Fatal(err)
	}
	return e
}

func standardProjectInstalled(t *testing.T, e standardOnboardingBoundaryEnv, id string, revision int64) state.App {
	t.Helper()
	row, err := e.store.GetApplicationStandardEnrollment(t.Context(), e.owner.PersonalOrg.ID, id)
	if err != nil || row.State != "persisted" || row.DesiredRevision != revision || row.PersistedRevision != revision || row.ObservedRevision != 0 || len(row.Adoptions) != 1 || row.Adoptions[0].AssignmentID != e.assignment || row.Adoptions[0].Version != 1 {
		t.Fatalf("project did not retain its pinned standard: %+v %v", row, err)
	}
	app, err := e.store.AppByID(t.Context(), id)
	if err != nil || app.ProjectID == "" || app.OrgID != e.owner.PersonalOrg.ID || !app.RequireSigned || app.SecurityPolicy != api.AppSecurityPolicyWarn || len(app.EgressAllowlist) != 1 || app.EgressAllowlist[0].String() != "8.8.8.0/24" || !reflect.DeepEqual(app.EgressPorts, []int{8443}) {
		t.Fatalf("project missed installed controls: %+v %v", app, err)
	}
	drains, err := e.store.ListAppLogDrainsForApp(t.Context(), id)
	if err != nil || len(drains) != 1 || !drains[0].Enabled || drains[0].TargetURL != e.destination.TargetURL {
		t.Fatalf("project missed company logging: %+v %v", drains, err)
	}
	der, err := base64.StdEncoding.DecodeString(e.publisher.PublicKeyDER)
	if err != nil {
		t.Fatal(err)
	}
	signers, err := e.store.ListAppTrustedSignersForApp(t.Context(), id)
	if err != nil || len(signers) != 1 || !bytes.Equal(signers[0].CosignPublicKey, der) {
		t.Fatalf("project missed approved publisher: %+v %v", signers, err)
	}
	return app
}

func standardProjectBuilds(t *testing.T, e standardOnboardingBoundaryEnv, response api.ApplyResponse, count int) {
	t.Helper()
	if len(response.Builds) != count {
		t.Fatalf("project build count=%d, want=%d: %+v", len(response.Builds), count, response)
	}
	for _, result := range response.Builds {
		if result.Error != "" || result.AppID == "" || result.DeploymentID == "" || result.BuildID == "" {
			t.Fatalf("first project deploy required manual repair: %+v", result)
		}
		deployment, err := e.store.DeploymentByID(t.Context(), result.DeploymentID)
		if err != nil || deployment.AppID != result.AppID || deployment.Status != state.DeployBuilding || deployment.DeployedByUserID != e.owner.Account.ID {
			t.Fatalf("project deployment boundary: %+v %v", deployment, err)
		}
		build, err := e.store.BuildByID(t.Context(), result.BuildID)
		if err != nil || build.DeploymentID != deployment.ID || build.Status != state.BuildQueued {
			t.Fatalf("project did not publish its build queue: %+v %v", build, err)
		}
	}
}

func TestMemProjectApplicationStandardsOnboarding(t *testing.T) {
	standardProjectOnboarding(t, state.NewMemStore())
}

// Real HTTP, source extraction, reconciliation, projection and durable enqueue.
// Builder execution, image verification, provider delivery and native residency
// have separate acceptance gates; accepting these builds is not observation.
func standardProjectOnboarding(t *testing.T, store standardProjectBoundaryStore) {
	t.Helper()
	e := standardProjectSetup(t, store)
	unrelated := standardOnboardingBoundaryResponse[api.AppResponse](t, standardOnboardingBoundaryRequest(t, e, http.MethodPost, "/v1/apps", api.CreateAppRequest{Slug: "unrelated-pending"}), http.StatusCreated)
	source := standardProjectSource(t, "standards-api", "standards-worker")
	response := standardProjectApply(t, e, source)
	if len(response.Apps) != 2 {
		t.Fatalf("project applications: %+v", response)
	}
	standardProjectBuilds(t, e, response, 2)
	ids := map[string]string{}
	for _, summary := range response.Apps {
		app := standardProjectInstalled(t, e, summary.ID, 1)
		ids[app.WorkloadName] = app.ID
	}
	standardOnboardingPending(t, e, unrelated)
	t.Run("candidate publication retains admitted version", func(t *testing.T) {
		path := "/v1/orgs/" + e.owner.PersonalOrg.Slug + "/application-standards/company-baseline"
		prior := standardOnboardingBoundaryResponse[api.ApplicationStandardVersion](t, standardOnboardingBoundaryRequest(t, e, http.MethodGet, path+"?version=1", nil), http.StatusOK)
		prior.Definition[appstandards.SecurityPolicy] = appstandards.Rule{Mode: appstandards.Mandatory, Value: json.RawMessage(`"off"`)}
		definition, err := json.Marshal(prior.Definition)
		if err != nil {
			t.Fatal(err)
		}
		candidate := standardOnboardingBoundaryResponse[api.ApplicationStandardVersion](t, standardOnboardingBoundaryRequest(t, e, http.MethodPost, path+"/versions", api.CreateApplicationStandardVersionRequest{ExpectedVersion: 1, Definition: definition}), http.StatusCreated)
		if candidate.Version != 2 {
			t.Fatalf("candidate: %+v", candidate)
		}
		for _, id := range ids {
			standardProjectInstalled(t, e, id, 1)
		}
	})
	t.Run("unchanged reapply", func(t *testing.T) {
		got := standardProjectApply(t, e, source)
		standardProjectBuilds(t, e, got, 0)
		for _, summary := range got.Apps {
			standardProjectInstalled(t, e, summary.ID, 1)
		}
	})
	t.Run("environment clone retains application ownership", func(t *testing.T) {
		result := standardOnboardingBoundaryResponse[api.ProjectEnvironmentResponse](t, standardOnboardingBoundaryRequest(t, e, http.MethodPost, "/v1/projects/standard-services/environments", api.CreateProjectEnvironmentRequest{Slug: "staging", FromEnvironment: "production"}), http.StatusCreated)
		if result.Slug != "staging" || result.ClonedFrom != "production" {
			t.Fatalf("environment clone: %+v", result)
		}
		apps, err := e.store.AppsForProject(t.Context(), e.owner.Account.ID, response.ProjectID)
		if err != nil || len(apps) != 2 {
			t.Fatalf("environment clone changed application inventory: %+v %v", apps, err)
		}
		for _, app := range apps {
			standardProjectInstalled(t, e, app.ID, 1)
		}
	})
	t.Run("restoration installs retained pins before build", func(t *testing.T) {
		removed := standardProjectApply(t, e, standardProjectSource(t, "standards-api"))
		standardProjectBuilds(t, e, removed, 0)
		worker, err := e.store.AppByID(t.Context(), ids["standards-worker"])
		if err != nil || worker.Status != state.AppDeleted {
			t.Fatalf("remove workload: %+v %v", worker, err)
		}
		restored := standardProjectApply(t, e, source)
		standardProjectBuilds(t, e, restored, 1)
		if restored.Builds[0].AppID != worker.ID {
			t.Fatal("restored service changed identity")
		}
		standardProjectInstalled(t, e, worker.ID, 2)
		standardProjectInstalled(t, e, ids["standards-api"], 1)
	})
	standardOnboardingPending(t, e, unrelated)
}

type interruptedProjectStandardStore struct {
	standardProjectBoundaryStore
	interrupted bool
}

func (s *interruptedProjectStandardStore) MaterializeApplicationStandardEnrollment(ctx context.Context, c state.ApplicationStandardEnrollmentClaim) (state.ApplicationStandardEnrollment, error) {
	if s.interrupted {
		return state.ApplicationStandardEnrollment{}, context.DeadlineExceeded
	}
	return s.standardProjectBoundaryStore.MaterializeApplicationStandardEnrollment(ctx, c)
}

func TestMemProjectApplicationStandardsInterruptedInstallation(t *testing.T) {
	standardProjectInterruptedInstallation(t, state.NewMemStore())
}

func standardProjectInterruptedInstallation(t *testing.T, base standardProjectBoundaryStore) {
	t.Helper()
	store := &interruptedProjectStandardStore{standardProjectBoundaryStore: base, interrupted: true}
	e := standardProjectSetup(t, store)
	source := standardProjectSource(t, "standards-retry")
	failed := standardProjectApply(t, e, source)
	if len(failed.Builds) != 1 || failed.Builds[0].DeploymentID != "" || failed.Builds[0].BuildID != "" || !strings.Contains(failed.Builds[0].Error, "Application standards are still being installed") {
		t.Fatalf("interrupted installation was admitted or concealed: %+v", failed)
	}
	if len(failed.Apps) != 1 {
		t.Fatalf("failed apply did not retain service intent: %+v", failed)
	}
	id := failed.Apps[0].ID
	standardOnboardingPending(t, e, api.AppResponse{ID: id, Slug: failed.Apps[0].Slug})
	// Recovery requires no lease expiry sleep and cannot reuse the old grant.
	claim, err := base.ClaimApplicationStandardEnrollmentForApp(t.Context(), state.ApplicationStandardEnrollmentClaimRequest{OrgID: e.owner.PersonalOrg.ID, AppID: id, DesiredRevision: 1, Owner: "background-after-interruption"})
	if err != nil {
		t.Fatalf("request failure stranded worker lease: %v", err)
	}
	if err := base.ReleaseApplicationStandardEnrollmentWorker(t.Context(), claim); err != nil {
		t.Fatal(err)
	}
	store.interrupted = false
	retry := standardProjectApply(t, e, source)
	standardProjectBuilds(t, e, retry, 1)
	if retry.Builds[0].AppID != id {
		t.Fatal("retry duplicated the service")
	}
	standardProjectInstalled(t, e, id, 1)
	deployments, err := e.store.ListDeploymentsForApp(t.Context(), id, 10, 0)
	if err != nil || len(deployments) != 1 {
		t.Fatalf("failed installation leaked deployment rows: %+v %v", deployments, err)
	}
}

func TestMemProjectApplicationStandardsBlockedInstallation(t *testing.T) {
	standardProjectBlockedInstallation(t, state.NewMemStore())
}

func standardProjectBlockedInstallation(t *testing.T, store standardProjectBoundaryStore) {
	t.Helper()
	e := standardProjectSetup(t, store)
	// The mandatory destination was reviewed for the creating account's old
	// entitlement. Installation must recheck the current plan, not that review.
	if err := store.UpdateAccountPlan(t.Context(), e.owner.Account.ID, api.PlanFree); err != nil {
		t.Fatal(err)
	}
	source := standardProjectSource(t, "standards-blocked")
	got := standardProjectApply(t, e, source)
	if len(got.Builds) != 1 || got.Builds[0].DeploymentID != "" || got.Builds[0].BuildID != "" || !strings.Contains(got.Builds[0].Error, "Application standards blocked this build") {
		t.Fatalf("blocked projection was admitted or concealed: %+v", got)
	}
	id := got.Builds[0].AppID
	row, err := store.GetApplicationStandardEnrollment(t.Context(), e.owner.PersonalOrg.ID, id)
	if err != nil || row.State != "blocked" || row.ErrorCode == "" || row.DesiredRevision != 1 || row.PersistedRevision != 0 || row.ObservedRevision != 0 {
		t.Fatalf("durable blocker: %+v %v", row, err)
	}
	app, err := store.AppByID(t.Context(), id)
	if err != nil || app.RequireSigned {
		t.Fatalf("blocked projection partially installed controls: %+v %v", app, err)
	}
	deployments, err := store.ListDeploymentsForApp(t.Context(), id, 10, 0)
	if err != nil || len(deployments) != 0 {
		t.Fatalf("blocked projection created deployments: %+v %v", deployments, err)
	}
	if _, err := store.ClaimApplicationStandardEnrollmentForApp(t.Context(), state.ApplicationStandardEnrollmentClaimRequest{OrgID: e.owner.PersonalOrg.ID, AppID: id, DesiredRevision: 1, Owner: "blocked-interactive-retry"}); err == nil {
		t.Fatal("interactive retry bypassed blocked retry policy")
	}
	retry := standardProjectApply(t, e, source)
	if len(retry.Builds) != 1 || !strings.Contains(retry.Builds[0].Error, "Application standards blocked this build") {
		t.Fatalf("blocked reapply lost durable status: %+v", retry)
	}
}

type detachedProjectStandardStore struct {
	standardProjectBoundaryStore
	detached bool
}

func (s *detachedProjectStandardStore) GetApplicationStandardEnrollment(ctx context.Context, orgID, appID string) (state.ApplicationStandardEnrollment, error) {
	if !s.detached {
		app, err := s.standardProjectBoundaryStore.AppByID(ctx, appID)
		if err != nil {
			return state.ApplicationStandardEnrollment{}, err
		}
		if app.ProjectID != "" {
			if err := s.standardProjectBoundaryStore.DeleteProject(ctx, app.ProjectID); err != nil {
				return state.ApplicationStandardEnrollment{}, err
			}
			s.detached = true
		}
	}
	return s.standardProjectBoundaryStore.GetApplicationStandardEnrollment(ctx, orgID, appID)
}

func TestMemProjectApplicationStandardsScopeChange(t *testing.T) {
	standardProjectScopeChange(t, state.NewMemStore())
}

func standardProjectScopeChange(t *testing.T, base standardProjectBoundaryStore) {
	t.Helper()
	store := &detachedProjectStandardStore{standardProjectBoundaryStore: base}
	e := standardProjectSetup(t, store)
	// DeleteProject really detaches and reenrolls the durable app between
	// reconciliation and the request's first standard read. An org-wide
	// standard still applies, but that does not authorize the old project
	// request to enqueue source for the now-independent application.
	r := scanAuditRegressionRequest(t, e.key, standardProjectSource(t, "standards-detached"), map[string]string{"project_slug": "standard-services"})
	r.URL.Path = "/v1/projects"
	w := httptest.NewRecorder()
	e.handler.ServeHTTP(w, r)
	problem := standardOnboardingBoundaryResponse[api.Problem](t, w, http.StatusConflict)
	if !store.detached || problem.Code != "project_apply_stale" {
		t.Fatalf("deleted project did not return a current-scope conflict: %+v", problem)
	}
	app, err := base.AppBySlug(t.Context(), "standards-detached")
	if err != nil || app.ProjectID != "" {
		t.Fatalf("real project detach did not persist: %+v %v", app, err)
	}
	id := app.ID
	row, err := base.GetApplicationStandardEnrollment(t.Context(), e.owner.PersonalOrg.ID, id)
	if err != nil || row.State != "pending" || row.DesiredRevision != 2 || row.PersistedRevision != 0 || row.ObservedRevision != 0 {
		t.Fatalf("request projected a different scope's intent: %+v %v", row, err)
	}
	deployments, err := base.ListDeploymentsForApp(t.Context(), id, 10, 0)
	if err != nil || len(deployments) != 0 {
		t.Fatalf("scope change created deployments: %+v %v", deployments, err)
	}
}
