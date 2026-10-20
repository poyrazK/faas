package apidserver

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

type additionNotifier struct {
	noopNotifier
	notify func(context.Context, string, string) error
}

func (f additionNotifier) Notify(ctx context.Context, channel, payload string) error {
	return f.notify(ctx, channel, payload)
}

func additionArchive(t *testing.T) []byte {
	t.Helper()
	var data bytes.Buffer
	gz := gzip.NewWriter(&data)
	tarball := tar.NewWriter(gz)
	for name, body := range map[string]string{
		"back/Dockerfile": "FROM alpine\n", "worker/Dockerfile": "FROM alpine\n", "metrics/Dockerfile": "FROM alpine\n",
		"docker-compose.yml": "services:\n  back:\n    build: ./back\n    ports: ['8080:8080']\n  worker:\n    build: ./worker\n    ports: ['8081:8080']\n  metrics:\n    build: ./metrics\n    ports: ['8082:8080']\n",
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

func additionRequest(t *testing.T, s *server, key, path, token string, enabled bool, source []byte) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for key, value := range map[string]string{"project_slug": "demo", "no_triggers": "true", "environment": "production"} {
		if err := writer.WriteField(key, value); err != nil {
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
	if enabled {
		r.Header.Set(api.ProjectAddServicesHeader, "required")
	}
	if strings.Contains(path, "safe-deploy") {
		r.Header.Set(api.DeploymentAttemptHeader, strings.Repeat("a", 32))
	}
	response := httptest.NewRecorder()
	s.handler().ServeHTTP(response, r)
	return response
}

func TestAtomicServiceAdditionAPIPlansBeforeReserving(t *testing.T) {
	for _, scenario := range []string{"apply", "disabled", "missing_opt_in", "stale_plan"} {
		t.Run(scenario, func(t *testing.T) {
			s, store, account, key := projectSecretsTestServer(t, api.ScopesAdminOnly)
			initial := projectSecretsRequest(t, s, key, "/v1/projects", "", nil)
			if initial.Code != http.StatusOK {
				t.Fatal(initial.Body.String())
			}
			var applied api.ApplyResponse
			if err := json.Unmarshal(initial.Body.Bytes(), &applied); err != nil {
				t.Fatal(err)
			}
			var original []state.ProjectReleaseMember
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
			}
			baseline, err := store.MemStore.PublishProjectReleaseSet(t.Context(), account.ID, applied.ProjectID, "production", 1800, original)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := store.MemStore.UpdateProjectEnvironmentProtection(t.Context(), account.ID, applied.ProjectID, "production", false); err != nil {
				t.Fatal(err)
			}
			s.WithAtomicServiceAdditionsEnabled(scenario != "disabled")
			source := additionArchive(t)
			preview := additionRequest(t, s, key, "/v1/projects/scan", "", scenario != "missing_opt_in", source)
			apps, err := store.MemStore.AppsForProject(t.Context(), account.ID, applied.ProjectID)
			if err != nil {
				t.Fatal(err)
			}
			if len(apps) != 2 {
				t.Fatal("scan created a service reservation")
			}
			if scenario == "disabled" || scenario == "missing_opt_in" {
				if preview.Code == http.StatusOK {
					t.Fatal("unqualified membership change passed")
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
			if plan.SafeRelease == nil || !plan.SafeRelease.AddServices || len(plan.SafeRelease.Additions) != 1 || plan.SafeRelease.Additions[0].AppID != "" {
				t.Fatalf("bad addition plan: %+v", plan.SafeRelease)
			}
			if scenario == "stale_plan" {
				data, err := base64.StdEncoding.DecodeString(plan.PlanToken)
				if err != nil {
					t.Fatal(err)
				}
				var token planTokenWire
				if err := json.Unmarshal(data, &token); err != nil {
					t.Fatal(err)
				}
				token.SafeReleaseID = baseline.ID
				data, err = json.Marshal(token)
				if err != nil {
					t.Fatal(err)
				}
				plan.PlanToken = base64.StdEncoding.EncodeToString(data)
			}
			admitted := 0
			var placementNotifications []string
			s.notif = additionNotifier{notify: func(_ context.Context, channel, payload string) error {
				if channel == db.NotifyAppChanged {
					var notice struct {
						Kind  string `json:"kind"`
						AppID string `json:"app_id"`
					}
					if err := json.Unmarshal([]byte(payload), &notice); err != nil {
						t.Fatal(err)
					}
					if notice.Kind == "created" {
						placementNotifications = append(placementNotifications, notice.AppID)
					}
				}
				return nil
			}}
			store.onBuild = func(dep state.Deployment) {
				admitted++
				if !dep.TrafficPercentExplicit || dep.TrafficPercent != 0 {
					t.Fatal("new service build received traffic")
				}
				active, err := store.MemStore.ActiveProjectReleaseSet(t.Context(), account.ID, applied.ProjectID, "production")
				if err != nil || active.ID != baseline.ID {
					t.Fatal("build admission switched production")
				}
			}
			result := additionRequest(t, s, key, "/v1/projects/safe-deploy", plan.PlanToken, true, source)
			if scenario == "stale_plan" {
				if result.Code == http.StatusOK || admitted != 0 {
					t.Fatal("changed membership plan admitted builds")
				}
				return
			}
			if result.Code != http.StatusOK {
				t.Fatalf("apply=%d %s", result.Code, result.Body)
			}
			if err := json.Unmarshal(result.Body.Bytes(), &applied); err != nil {
				t.Fatal(err)
			}
			if admitted != 3 || len(applied.Builds) != 3 || applied.SafeRelease.Additions[0].AppID == "" {
				t.Fatalf("incomplete addition receipt: %+v", applied)
			}
			if len(placementNotifications) != 1 || placementNotifications[0] != applied.SafeRelease.Additions[0].AppID {
				t.Fatalf("new service was not announced for placement: %v", placementNotifications)
			}
			if api.ProjectReleaseAdditionsPolicyID(plan.SafeRelease.Additions) != api.ProjectReleaseAdditionsPolicyID(applied.SafeRelease.Additions) {
				t.Fatal("reservation changed approved settings")
			}
		})
	}
}
