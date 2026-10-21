package apidserver_test

// adr: 20261021120000

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// removalArchive declares only the back service of the two-service fixture.
func removalArchive(t *testing.T) []byte {
	t.Helper()
	var data bytes.Buffer
	gz := gzip.NewWriter(&data)
	tarball := tar.NewWriter(gz)
	for name, body := range map[string]string{
		"back/Dockerfile":    "FROM alpine\n",
		"docker-compose.yml": "services:\n  back:\n    build: ./back\n    ports: ['8080:8080']\n",
	} {
		if err := tarball.WriteHeader(&tar.Header{Name: "repo/" + name, Mode: 0600, Size: int64(len(body))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tarball.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tarball.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}

func removalRequest(t *testing.T, handler http.Handler, key, path, token string, headers map[string]string, source []byte) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for field, value := range map[string]string{"project_slug": "demo", "no_triggers": "true", "environment": "production"} {
		if err := writer.WriteField(field, value); err != nil {
			t.Fatal(err)
		}
	}
	part, err := writer.CreateFormFile("source", "source.tar.gz")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(source); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, path, &body)
	if token != "" {
		query := r.URL.Query()
		query.Set("plan_token", token)
		r.URL.RawQuery = query.Encode()
	}
	r.Header.Set("Authorization", "Bearer "+key)
	r.Header.Set("Content-Type", writer.FormDataContentType())
	r.Header.Set(api.ProjectSafeReleaseHeader, "atomic")
	for name, value := range headers {
		r.Header.Set(name, value)
	}
	if strings.Contains(path, "safe-deploy") {
		r.Header.Set(api.DeploymentAttemptHeader, strings.Repeat("a", 32))
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, r)
	return response
}

func TestAtomicServiceRemovalAPI(t *testing.T) {
	for _, scenario := range []string{"apply", "disabled", "with_additions"} {
		t.Run(scenario, func(t *testing.T) {
			s, store, account, key := apidserver.ProjectSecretsTestServer(t, api.ScopesAdminOnly)
			initial := apidserver.ProjectSecretsRequest(t, s, key, "/v1/projects", "", nil)
			if initial.Code != http.StatusOK {
				t.Fatal(initial.Body.String())
			}
			var applied api.ApplyResponse
			if err := json.Unmarshal(initial.Body.Bytes(), &applied); err != nil {
				t.Fatal(err)
			}
			var original []state.ProjectReleaseMember
			var retiring string
			for _, build := range applied.Builds {
				app, err := store.MemStore.AppByID(t.Context(), build.AppID)
				if err != nil {
					t.Fatal(err)
				}
				app.Manifest.RevisionPinTTLSeconds = 1800
				if _, err := store.MemStore.UpdateApp(t.Context(), app.ID, state.UpdateAppParams{Manifest: &app.Manifest}); err != nil {
					t.Fatal(err)
				}
				dep, err := store.MemStore.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Scope: "production", Kind: state.DeploymentKindTarball})
				if err != nil {
					t.Fatal(err)
				}
				if err := store.MemStore.MarkDeploymentLive(t.Context(), dep.ID); err != nil {
					t.Fatal(err)
				}
				original = append(original, state.ProjectReleaseMember{AppID: app.ID, DeploymentID: dep.ID})
				if app.WorkloadName == "worker" {
					retiring = app.ID
				}
			}
			if _, err := store.MemStore.PublishProjectReleaseSet(t.Context(), account.ID, applied.ProjectID, "production", 1800, original); err != nil {
				t.Fatal(err)
			}
			if _, err := store.MemStore.UpdateProjectEnvironmentProtection(t.Context(), account.ID, applied.ProjectID, "production", false); err != nil {
				t.Fatal(err)
			}
			s.WithAtomicServiceRemovalsEnabled(scenario != "disabled")
			s.WithAtomicServiceAdditionsEnabled(true)
			headers := map[string]string{api.ProjectRemoveServicesHeader: "required"}
			if scenario == "with_additions" {
				headers[api.ProjectAddServicesHeader] = "required"
			}
			source := removalArchive(t)
			handler := s.Handler()
			preview := removalRequest(t, handler, key, "/v1/projects/scan", "", headers, source)
			switch scenario {
			case "disabled":
				if preview.Code != http.StatusServiceUnavailable {
					t.Fatalf("unqualified removal: %d %s", preview.Code, preview.Body)
				}
				return
			case "with_additions":
				if preview.Code != http.StatusBadRequest && preview.Code != http.StatusUnprocessableEntity {
					t.Fatalf("removal combined with additions: %d %s", preview.Code, preview.Body)
				}
				return
			}
			if preview.Code != http.StatusOK {
				t.Fatalf("scan=%d %s", preview.Code, preview.Body)
			}
			var plan api.PlanResponse
			if err := json.Unmarshal(preview.Body.Bytes(), &plan); err != nil {
				t.Fatal(err)
			}
			if plan.SafeRelease == nil || !plan.SafeRelease.RemoveServices || len(plan.SafeRelease.Removals) != 1 || plan.SafeRelease.Removals[0].AppID != retiring {
				t.Fatalf("bad removal plan: %+v", plan.SafeRelease)
			}
			result := removalRequest(t, handler, key, "/v1/projects/safe-deploy", plan.PlanToken, headers, source)
			if result.Code != http.StatusOK {
				t.Fatalf("apply=%d %s", result.Code, result.Body)
			}
			if err := json.Unmarshal(result.Body.Bytes(), &applied); err != nil {
				t.Fatal(err)
			}
			if len(applied.Builds) != 1 || applied.Builds[0].AppID == retiring || applied.SafeRelease == nil || len(applied.SafeRelease.Removals) != 1 {
				t.Fatalf("retired service was rebuilt or not recorded: %+v", applied)
			}
			if _, err := store.MemStore.AppByID(t.Context(), retiring); err != nil {
				t.Fatalf("retirement deleted the app before publication: %v", err)
			}
		})
	}
}
