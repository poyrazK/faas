package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/environmentsync"
	"github.com/onebox-faas/faas/pkg/state"
)

func seedPublicSecretReferences(t *testing.T, store *state.MemStore, acct state.Account) (state.Project, state.App) {
	t.Helper()
	project, err := store.CreateProject(t.Context(), state.Project{AccountID: acct.ID, Slug: "shop", RepoFullName: "example/shop", ProductionBranch: "main", InstallID: 42})
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(t.Context(), state.App{AccountID: acct.ID, ProjectID: project.ID, Slug: "shop-api", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	for _, scope := range []string{"production", "default"} {
		if err := store.UpsertAppSecretInScope(t.Context(), acct.ID, app.ID, scope, "DATABASE", []byte("sealed-"+scope)); err != nil {
			t.Fatal(err)
		}
	}
	return project, app
}

func ownPublicSecretReference(t *testing.T, store *state.MemStore, acct state.Account, project state.Project, app state.App) state.EnvironmentGitSource {
	t.Helper()
	if err := store.PutAppEnvironmentSecretReference(t.Context(), acct.ID, app.ID, "production", "DATABASE_URL", "secret:DATABASE"); err != nil {
		t.Fatal(err)
	}
	source, err := store.CreateEnvironmentGitSource(t.Context(), acct.ID, project.ID, "production", state.EnvironmentGitSourceSpec{RepositoryID: 123, InstallationID: 42, Repository: "example/shop", Ref: "refs/heads/main", ManifestPath: "environment.yaml", Mode: "enforce", ApprovalPolicy: "manual"})
	if err != nil {
		t.Fatal(err)
	}
	desired, err := environmentsync.Compile(api.EnvironmentDefinition{APIVersion: environmentsync.APIVersion, Project: project.Slug, Environment: "production", Workloads: map[string]api.EnvironmentWorkload{"api": {App: app.Slug, SecretRefs: map[string]string{"DATABASE_URL": "secret:DATABASE"}}}})
	if err != nil {
		t.Fatal(err)
	}
	source, _, err = store.ApproveEnvironmentDesiredRevision(t.Context(), state.ApproveEnvironmentRevision{AccountID: acct.ID, SourceID: source.ID, ExpectedGeneration: source.Generation, CommitSHA: strings.Repeat("a", 40), Desired: desired, ApprovedBy: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	preview, err := store.PreviewEnvironmentGitOpsAdoption(t.Context(), acct.ID, source.ID)
	if err != nil || !preview.CanApply() {
		t.Fatalf("adoption: %+v %v", preview, err)
	}
	if err := store.AdoptEnvironmentGitOps(t.Context(), acct.ID, source.ID, preview.Hash); err != nil {
		t.Fatal(err)
	}
	return source
}

func TestSecretReferenceHTTPNamesOnlyAndSourcePreservation(t *testing.T) {
	e := setup(t, api.PlanPro)
	_, app := seedPublicSecretReferences(t, e.store, e.acct)
	path := "/v1/apps/shop-api/secret-references"
	set := e.do(t, http.MethodPut, path+"/DATABASE_URL?environment=production", api.PutAppSecretReferenceRequest{Reference: "secret:DATABASE"}, nil)
	if set.Code != http.StatusOK {
		t.Fatalf("set: %d %s", set.Code, set.Body.String())
	}
	list := e.do(t, http.MethodGet, path+"?environment=production", nil, nil)
	var response api.AppSecretReferenceListResponse
	if err := json.Unmarshal(list.Body.Bytes(), &response); err != nil || list.Code != http.StatusOK || response.EnvironmentID == "" || response.References["DATABASE_URL"] != "secret:DATABASE" || response.Count != 1 {
		t.Fatalf("list: %d %+v %v", list.Code, response, err)
	}
	if strings.Contains(list.Body.String(), "sealed-") || strings.Contains(list.Body.String(), "ciphertext") {
		t.Fatal("reference read exposed sealed values")
	}
	for i := 0; i < 2; i++ {
		removed := e.do(t, http.MethodDelete, path+"/DATABASE_URL?environment=production", nil, nil)
		if removed.Code != http.StatusNoContent {
			t.Fatalf("delete: %d %s", removed.Code, removed.Body.String())
		}
	}
	list = e.do(t, http.MethodGet, path+"?environment=production", nil, nil)
	response = api.AppSecretReferenceListResponse{}
	if err := json.Unmarshal(list.Body.Bytes(), &response); err != nil || len(response.References) != 0 || len(response.SuppressedKeys) != 1 || response.SuppressedKeys[0] != "DATABASE_URL" || response.Count != 0 {
		t.Fatalf("removed key absent from public suppression list: %+v %v", response, err)
	}
	set = e.do(t, http.MethodPut, path+"/DATABASE_URL?environment=production", api.PutAppSecretReferenceRequest{Reference: "secret:DATABASE"}, nil)
	list = e.do(t, http.MethodGet, path+"?environment=production", nil, nil)
	response = api.AppSecretReferenceListResponse{}
	if err := json.Unmarshal(list.Body.Bytes(), &response); err != nil || set.Code != http.StatusOK || len(response.SuppressedKeys) != 0 || response.References["DATABASE_URL"] != "secret:DATABASE" {
		t.Fatalf("public reenable: %+v %v", response, err)
	}
	secret, err := e.store.GetAppSecretInScope(t.Context(), e.acct.ID, app.ID, "production", "DATABASE")
	if err != nil || !bytes.Equal(secret.Ciphertext, []byte("sealed-production")) {
		t.Fatalf("delete changed source: %+v %v", secret, err)
	}
}

func TestSecretReferenceHTTPValidationQuotaAndAuthority(t *testing.T) {
	e := setup(t, api.PlanFree)
	project, app := seedPublicSecretReferences(t, e.store, e.acct)
	path := "/v1/apps/shop-api/secret-references"
	for _, tc := range []struct {
		suffix, reference, code string
		status                  int
	}{
		{"/URL", "secret:DATABASE", "validation_failed", 400},
		{"/URL?environment=default", "secret:DATABASE", "validation_failed", 400},
		{"/URL?environment=__all__", "secret:DATABASE", "validation_failed", 400},
		{"/URL?environment=missing", "secret:DATABASE", api.CodeNotFound, 404},
		{"/URL?environment=production", "DATABASE", "validation_failed", 400},
		{"/URL?environment=production", "secret:ABSENT", "secret_not_found", 400},
		{"/bad-key?environment=production", "secret:DATABASE", "env_var_invalid_key", 400},
	} {
		t.Run(tc.suffix+tc.reference, func(t *testing.T) {
			rec := e.do(t, http.MethodPut, path+tc.suffix, api.PutAppSecretReferenceRequest{Reference: tc.reference}, nil)
			if rec.Code != tc.status || !strings.Contains(rec.Body.String(), `"code":"`+tc.code+`"`) {
				t.Fatalf("validation: %d %s", rec.Code, rec.Body.String())
			}
		})
	}
	if err := e.store.UpsertAppEnvInScope(t.Context(), e.acct.ID, app.ID, "production", "PLAIN", "value"); err != nil {
		t.Fatal(err)
	}
	if rec := e.do(t, http.MethodPut, path+"/PLAIN?environment=production", api.PutAppSecretReferenceRequest{Reference: "secret:DATABASE"}, nil); rec.Code != 409 {
		t.Fatalf("shadow: %d %s", rec.Code, rec.Body.String())
	}
	limits := api.MustLimitsFor(e.acct.Plan)
	for i := 1; i < limits.EnvVarsMax; i++ {
		if err := e.store.UpsertAppEnvInScope(t.Context(), e.acct.ID, app.ID, "default", fmt.Sprintf("KEY_%d", i), "value"); err != nil {
			t.Fatal(err)
		}
	}
	if rec := e.do(t, http.MethodPut, path+"/URL?environment=production", api.PutAppSecretReferenceRequest{Reference: "secret:DATABASE"}, nil); rec.Code != 403 || !strings.Contains(rec.Body.String(), `"observed":`) {
		t.Fatalf("shared quota: %d %s", rec.Code, rec.Body.String())
	}
	// A separate app avoids quota hiding the ownership result.
	owned, err := e.store.CreateApp(t.Context(), state.App{AccountID: e.acct.ID, ProjectID: project.ID, Slug: "owned-api", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.store.UpsertAppSecretInScope(t.Context(), e.acct.ID, owned.ID, "production", "DATABASE", []byte("sealed")); err != nil {
		t.Fatal(err)
	}
	source := ownPublicSecretReference(t, e.store, e.acct, project, owned)
	ownedPath := "/v1/apps/owned-api/secret-references/DATABASE_URL?environment=production"
	for _, method := range []string{http.MethodPut, http.MethodDelete} {
		rec := e.do(t, method, ownedPath, api.PutAppSecretReferenceRequest{Reference: "secret:ABSENT"}, nil)
		if rec.Code != 409 || !strings.Contains(rec.Body.String(), "environment_field_git_managed") {
			t.Fatalf("owned %s: %d %s", method, rec.Code, rec.Body.String())
		}
	}
	if err := e.store.SetEnvironmentGitOpsOverride(t.Context(), e.acct.ID, source.ID, state.EnvironmentGitOpsOverrideRequest{Resource: "workload/api", Path: "secret_refs/DATABASE_URL", Reason: "database incident", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if rec := e.do(t, http.MethodPut, ownedPath, api.PutAppSecretReferenceRequest{Reference: "secret:DATABASE"}, nil); rec.Code != 200 {
		t.Fatalf("override: %d %s", rec.Code, rec.Body.String())
	}
}

type replaceSecretReferenceCatalogStore struct {
	state.Store
	state.AppEnvironmentSecretReferenceControlStore
	project state.Project
}

func (s replaceSecretReferenceCatalogStore) ReadAppEnvironmentSecretReferences(ctx context.Context, account, app, scope string) (state.AppEnvironmentSecretReferenceSnapshot, error) {
	snapshot, err := s.AppEnvironmentSecretReferenceControlStore.ReadAppEnvironmentSecretReferences(ctx, account, app, scope)
	if err != nil {
		return snapshot, err
	}
	if err := s.Store.DeleteProjectEnvironment(ctx, account, s.project.ID, scope); err != nil {
		return snapshot, err
	}
	_, err = s.Store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: account, ProjectID: s.project.ID, Slug: scope})
	return snapshot, err
}

func TestSecretReferenceHTTPRejectsReplacementDuringWrite(t *testing.T) {
	for _, method := range []string{http.MethodPut, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			e := setup(t, api.PlanPro)
			project, _ := seedPublicSecretReferences(t, e.store, e.acct)
			if _, err := e.store.CreateProjectEnvironment(t.Context(), state.ProjectEnvironment{AccountID: e.acct.ID, ProjectID: project.ID, Slug: "staging"}); err != nil {
				t.Fatal(err)
			}
			e.s.store = replaceSecretReferenceCatalogStore{e.store, e.store, project}
			rec := e.do(t, method, "/v1/apps/shop-api/secret-references/URL?environment=staging", api.PutAppSecretReferenceRequest{Reference: "secret:DATABASE"}, nil)
			if rec.Code != 409 || !strings.Contains(rec.Body.String(), "secret_reference_conflict") {
				t.Fatalf("replacement: %d %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestSecretReferenceHTTPPermissionsAndMFA(t *testing.T) {
	e := setupWithScopes(t, []string{api.ScopeAppsRead})
	_, _ = seedPublicSecretReferences(t, e.store, e.acct)
	path := "/v1/apps/shop-api/secret-references?environment=production"
	if rec := e.do(t, http.MethodGet, path, nil, nil); rec.Code != 200 {
		t.Fatalf("read scope: %d %s", rec.Code, rec.Body.String())
	}
	for _, method := range []string{http.MethodPut, http.MethodDelete} {
		if rec := e.do(t, method, "/v1/apps/shop-api/secret-references/URL?environment=production", api.PutAppSecretReferenceRequest{Reference: "secret:DATABASE"}, nil); rec.Code != 403 {
			t.Fatalf("read-only %s: %d", method, rec.Code)
		}
	}
	h, acct, mgr, sid := setupMW(t, api.PlanPro, true)
	cookie := reissueWithMFAFlag(t, mgr, sid, acct.ID, true)
	for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete} {
		requestPath := "/v1/apps/shop-api/secret-references/URL?environment=production"
		if method == http.MethodGet {
			requestPath = path
		}
		rec := cookieDo(t, h, cookie, method, requestPath, api.PutAppSecretReferenceRequest{Reference: "secret:DATABASE"})
		if rec.Code != 403 || !strings.Contains(rec.Body.String(), api.CodeMFARequired) {
			t.Fatalf("pending MFA %s: %d %s", method, rec.Code, rec.Body.String())
		}
	}
}
