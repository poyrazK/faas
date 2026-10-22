package apidserver_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

func migrationUpload(t *testing.T, handler http.Handler, key, endpoint string, fields map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	part, err := form.CreateFormFile("source", "migration.tar.gz")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(apidserver.ApplyProjectOneWorkloadTarGz(t)); err != nil {
		t.Fatal(err)
	}
	for name, value := range fields {
		if err := form.WriteField(name, value); err != nil {
			t.Fatal(err)
		}
	}
	if err := form.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, endpoint, &body)
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", form.FormDataContentType())
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func migrationJSON(t *testing.T, handler http.Handler, key string, input api.ApplyCachedProjectPlanRequest) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/projects/apply-plan", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func TestMigrationCachedApplySealsConfigurationBeforeDeployment(t *testing.T) {
	t.Cleanup(apidserver.WithTestRecipient(t))
	t.Setenv("FAAS_SCAN_SPOOL_ROOT", t.TempDir())
	t.Setenv("FAAS_SPOOL_ROOT", t.TempDir())
	e, _ := apidserver.NewTestServerWithCapturingNotifier(t, api.PlanPro)
	scan := migrationUpload(t, *e.H(), *e.Key(), "/v1/projects/scan", map[string]string{"project_slug": "migration-api", "no_triggers": "true"})
	if scan.Code != http.StatusOK {
		t.Fatalf("scan: %d %s", scan.Code, scan.Body)
	}
	var plan api.PlanResponse
	if err := json.Unmarshal(scan.Body.Bytes(), &plan); err != nil {
		t.Fatal(err)
	}
	if len(plan.Workloads) != 1 {
		t.Fatalf("workloads: %+v", plan.Workloads)
	}
	name := plan.Workloads[0].Name
	const value = "migration-transient-secret-value"
	response := migrationJSON(t, *e.H(), *e.Key(), api.ApplyCachedProjectPlanRequest{
		PlanToken: plan.PlanToken, Only: []string{name}, InitialSecrets: api.ProjectInitialSecrets{name: {"DATABASE_URL": value}},
	})
	if response.Code != http.StatusOK {
		t.Fatalf("apply: %d %s", response.Code, response.Body)
	}
	if bytes.Contains(response.Body.Bytes(), []byte(value)) {
		t.Fatal("secret value in receipt")
	}
	var applied api.ApplyResponse
	if err := json.Unmarshal(response.Body.Bytes(), &applied); err != nil {
		t.Fatal(err)
	}
	if len(applied.Builds) != 1 || applied.Builds[0].DeploymentID == "" {
		t.Fatalf("missing deployment: %+v", applied.Builds)
	}
	rows, err := (*e.Store()).ListAllAppSecrets(t.Context(), e.Acct().ID, applied.Builds[0].AppID)
	if err != nil || len(rows) != 1 {
		t.Fatalf("secret metadata count=%d err=%v", len(rows), err)
	}
	dep, err := (*e.Store()).DeploymentByID(t.Context(), applied.Builds[0].DeploymentID)
	if err != nil {
		t.Fatal(err)
	}
	if rows[0].Key != "DATABASE_URL" || rows[0].Scope != api.DefaultEnvScope || rows[0].Kid == "" || rows[0].ValueHash == "" || rows[0].CreatedAt.After(dep.CreatedAt) || bytes.Contains(rows[0].Ciphertext, []byte(value)) {
		t.Fatal("configuration was not sealed before deployment")
	}
}

func TestMigrationExistingNamesRemainApplicableForOlderMultipartClients(t *testing.T) {
	t.Cleanup(apidserver.WithTestRecipient(t))
	t.Setenv("FAAS_SCAN_SPOOL_ROOT", t.TempDir())
	t.Setenv("FAAS_SPOOL_ROOT", t.TempDir())
	e, _ := apidserver.NewTestServerWithCapturingNotifier(t, api.PlanPro)
	fields := map[string]string{"project_slug": "older-client", "no_triggers": "true"}
	for attempt := range 2 {
		scan := migrationUpload(t, *e.H(), *e.Key(), "/v1/projects/scan", fields)
		if scan.Code != http.StatusOK {
			t.Fatalf("scan: %d %s", scan.Code, scan.Body)
		}
		var plan api.PlanResponse
		if err := json.Unmarshal(scan.Body.Bytes(), &plan); err != nil {
			t.Fatal(err)
		}
		applied := migrationUpload(t, *e.H(), *e.Key(), "/v1/projects?plan_token="+url.QueryEscape(plan.PlanToken), fields)
		if applied.Code != http.StatusOK {
			t.Fatalf("apply %d: %d %s", attempt, applied.Code, applied.Body)
		}
		if attempt == 1 {
			raw, _ := json.Marshal(map[string]string{plan.Workloads[0].Name: "different-name"})
			changed := map[string]string{"project_slug": "older-client", "no_triggers": "true", "app_slugs": string(raw)}
			stale := migrationUpload(t, *e.H(), *e.Key(), "/v1/projects?plan_token="+url.QueryEscape(plan.PlanToken), changed)
			if stale.Code != http.StatusConflict {
				t.Fatalf("changed names accepted: %d %s", stale.Code, stale.Body)
			}
		}
	}
}

func TestMigrationLegacyGenericSourceKeepsSavedIdentity(t *testing.T) {
	for _, existing := range []bool{false, true} {
		name := "prepared"
		if existing {
			name = "existing"
		}
		t.Run(name, func(t *testing.T) {
			t.Cleanup(apidserver.WithTestRecipient(t))
			t.Setenv("FAAS_SCAN_SPOOL_ROOT", t.TempDir())
			t.Setenv("FAAS_SPOOL_ROOT", t.TempDir())
			e, _ := apidserver.NewTestServerWithCapturingNotifier(t, api.PlanPro)
			if existing {
				scanned := migrationUpload(t, *e.H(), *e.Key(), "/v1/projects/scan", map[string]string{"project_slug": "legacy-single", "no_triggers": "true"})
				var original api.PlanResponse
				if err := json.Unmarshal(scanned.Body.Bytes(), &original); err != nil {
					t.Fatal(err)
				}
				created := migrationJSON(t, *e.H(), *e.Key(), api.ApplyCachedProjectPlanRequest{PlanToken: original.PlanToken})
				if created.Code != http.StatusOK {
					t.Fatalf("legacy apply: %d %s", created.Code, created.Body)
				}
			}
			rescan := migrationUpload(t, *e.H(), *e.Key(), "/v1/projects/scan", map[string]string{"project_slug": "legacy-single", "no_triggers": "true", "migration": "true", "only": "legacy-single"})
			if rescan.Code != http.StatusOK {
				t.Fatalf("rescan: %d %s", rescan.Code, rescan.Body)
			}
			var plan api.PlanResponse
			if err := json.Unmarshal(rescan.Body.Bytes(), &plan); err != nil {
				t.Fatal(err)
			}
			if !plan.CanApply || len(plan.Workloads) != 1 || plan.Workloads[0].Name != "legacy-single" || plan.Workloads[0].AppSlug != "legacy-single" {
				t.Fatalf("legacy identity lost: %+v", plan)
			}
			if existing && (plan.Workloads[0].Action != "update" || plan.Workloads[0].ExistingAppID == "") {
				t.Fatal("legacy member would be replaced")
			}
		})
	}
}

func TestMigrationRejectsSecretWriteWithoutScopeBeforeMutation(t *testing.T) {
	t.Cleanup(apidserver.WithTestRecipient(t))
	t.Setenv("FAAS_SCAN_SPOOL_ROOT", t.TempDir())
	t.Setenv("FAAS_SPOOL_ROOT", t.TempDir())
	e, _ := apidserver.NewTestServerWithCapturingNotifier(t, api.PlanPro)
	key, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (*e.Store()).CreateAPIKey(t.Context(), e.Acct().ID, hash, "deploy-only", api.ScopesDeployWriteSurface[1:]); err != nil {
		t.Fatal(err)
	}
	fields := map[string]string{"project_slug": "scope-check", "initial_secrets": `{"scope-check":{"DATABASE_URL":"hidden-value"}}`}
	response := migrationUpload(t, *e.H(), key, "/v1/projects", fields)
	if response.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", response.Code, response.Body)
	}
	projects, err := (*e.Store()).ListProjectsForAccount(t.Context(), e.Acct().ID)
	if err != nil || len(projects) != 0 {
		t.Fatalf("unauthorized mutation: %d projects err=%v", len(projects), err)
	}
}

func TestMigrationSourceScanDisablesPushBindingAndTriggers(t *testing.T) {
	e := apidserver.NewSourceRefTestServer(t, api.PlanPro, "existing", 9999)
	(*(*e.Gh()).StreamBody()) = apidserver.NopReadCloser{Reader: bytes.NewReader(*e.Tarball())}
	rec := e.Post(t, "/v1/projects/scan/source-ref", api.ProjectSourceRefScanRequest{Repo: "onebox-faas/hello", Ref: "main", ProjectSlug: "hello", Migration: true})
	if rec.Code != http.StatusOK {
		t.Fatalf("scan status=%d body=%s", rec.Code, rec.Body)
	}
	var plan api.PlanResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &plan); err != nil {
		t.Fatal(err)
	}
	data, err := base64.StdEncoding.DecodeString(plan.PlanToken)
	if err != nil {
		t.Fatal(err)
	}
	var token struct {
		InstallID  int64  `json:"install_id"`
		NoTriggers bool   `json:"no_triggers"`
		Repo       string `json:"repo_full_name"`
	}
	if err := json.Unmarshal(data, &token); err != nil {
		t.Fatal(err)
	}
	if !token.NoTriggers || token.InstallID != 0 || token.Repo != "" || plan.RepoFullName != "" {
		t.Fatal("migration would enable unattended work")
	}
}

func TestMigrationInvalidPlanDoesNotMutate(t *testing.T) {
	e, _ := apidserver.NewTestServerWithCapturingNotifier(t, api.PlanPro)
	response := migrationJSON(t, *e.H(), *e.Key(), api.ApplyCachedProjectPlanRequest{PlanToken: "unavailable"})
	if response.Code != http.StatusGone {
		t.Fatalf("status=%d body=%s", response.Code, response.Body)
	}
	projects, err := (*e.Store()).ListProjectsForAccount(context.Background(), e.Acct().ID)
	if err != nil || len(projects) != 0 {
		t.Fatal("expired plan mutated projects")
	}
}

func TestMigrationOtherAccountCannotApplyReviewedSource(t *testing.T) {
	t.Setenv("FAAS_SCAN_SPOOL_ROOT", t.TempDir())
	t.Setenv("FAAS_SPOOL_ROOT", t.TempDir())
	owner, _ := apidserver.NewTestServerWithCapturingNotifier(t, api.PlanPro)
	scan := migrationUpload(t, *owner.H(), *owner.Key(), "/v1/projects/scan", map[string]string{"project_slug": "owned-source"})
	if scan.Code != http.StatusOK {
		t.Fatalf("scan: %d %s", scan.Code, scan.Body)
	}
	var plan api.PlanResponse
	if err := json.Unmarshal(scan.Body.Bytes(), &plan); err != nil {
		t.Fatal(err)
	}
	other, _ := apidserver.NewTestServerWithCapturingNotifier(t, api.PlanPro)
	response := migrationJSON(t, *other.H(), *other.Key(), api.ApplyCachedProjectPlanRequest{PlanToken: plan.PlanToken})
	if response.Code != http.StatusGone {
		t.Fatalf("status=%d body=%s", response.Code, response.Body)
	}
	projects, err := (*other.Store()).ListProjectsForAccount(t.Context(), other.Acct().ID)
	if err != nil || len(projects) != 0 {
		t.Fatal("another account's source mutated projects")
	}
}

func TestMigrationConfigurationFailureCanResumeTheSameApps(t *testing.T) {
	t.Cleanup(apidserver.WithTestRecipient(t))
	t.Setenv("FAAS_SCAN_SPOOL_ROOT", t.TempDir())
	t.Setenv("FAAS_SPOOL_ROOT", t.TempDir())
	e, _ := apidserver.NewTestServerWithCapturingNotifier(t, api.PlanPro)
	scan := migrationUpload(t, *e.H(), *e.Key(), "/v1/projects/scan", map[string]string{"project_slug": "retry-api", "no_triggers": "true"})
	if scan.Code != http.StatusOK {
		t.Fatalf("scan: %d %s", scan.Code, scan.Body)
	}
	var plan api.PlanResponse
	if err := json.Unmarshal(scan.Body.Bytes(), &plan); err != nil {
		t.Fatal(err)
	}
	name := plan.Workloads[0].Name
	request := api.ApplyCachedProjectPlanRequest{PlanToken: plan.PlanToken, Only: []string{name}, InitialSecrets: api.ProjectInitialSecrets{name: {"DATABASE_URL": "request-only"}}}
	identities, identity := *apidserver.MfaIdentities, *apidserver.MfaIdentity
	t.Cleanup(func() { *apidserver.MfaIdentities, *apidserver.MfaIdentity = identities, identity })
	*apidserver.MfaIdentities, *apidserver.MfaIdentity = nil, nil
	failed := migrationJSON(t, *e.H(), *e.Key(), request)
	var problem api.Problem
	if err := json.Unmarshal(failed.Body.Bytes(), &problem); err != nil {
		t.Fatal(err)
	}
	if failed.Code != http.StatusServiceUnavailable || problem.Code != "project_configuration_failed" {
		t.Fatalf("configuration failure: %d %s", failed.Code, failed.Body)
	}
	projects, err := (*e.Store()).ListProjectsForAccount(t.Context(), e.Acct().ID)
	if err != nil || len(projects) != 1 {
		t.Fatal("saved project is unavailable for correction")
	}
	*apidserver.MfaIdentities, *apidserver.MfaIdentity = identities, identity
	resumed := migrationJSON(t, *e.H(), *e.Key(), request)
	if resumed.Code != http.StatusOK {
		t.Fatalf("resume: %d %s", resumed.Code, resumed.Body)
	}
	var applied api.ApplyResponse
	if err := json.Unmarshal(resumed.Body.Bytes(), &applied); err != nil {
		t.Fatal(err)
	}
	if len(applied.Apps) != 1 || len(applied.Builds) != 1 || applied.Builds[0].DeploymentID == "" || applied.ProjectID != projects[0].ID {
		t.Fatal("configuration correction did not deploy the saved workload")
	}
}
