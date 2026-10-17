package apidserver

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestSourceDeploymentCronReadiness(t *testing.T) {
	for _, route := range []string{"source-ref", "source-tarball", "upload", "legacy-multipart"} {
		for _, mode := range []string{"ready", "failed", "dark", "preview"} {
			t.Run(route+"/"+mode, func(t *testing.T) {
				if route == "upload" && mode == "dark" {
					t.Skip("upload transport has no traffic option")
				}
				e := newSourceRefTestServer(t, api.PlanPro, "x", 7777)
				slug := "x"
				if mode == "preview" {
					project, err := e.store.CreateProject(t.Context(), state.Project{AccountID: e.acctID, Slug: "cron-preview", ScanSource: state.ProjectScanSourceCompose})
					if err != nil {
						t.Fatal(err)
					}
					app, err := e.store.CreateApp(t.Context(), state.App{AccountID: e.acctID, ProjectID: project.ID, Slug: "preview-app", Status: state.AppActive})
					if err != nil {
						t.Fatal(err)
					}
					e.appID = app.ID
					slug = app.Slug
					if _, err := e.store.CreateProjectEnvironment(t.Context(), state.ProjectEnvironment{AccountID: e.acctID, ProjectID: project.ID, Slug: "preview"}); err != nil {
						t.Fatal(err)
					}
				}
				manifest := `triggers:
  - kind: cron
    app: x
    schedule: "0 3 * * *"
    path: /cleanup
`
				archive := buildSourceRefTarGzWithManifest(t, strings.ReplaceAll(manifest, "app: x", "app: "+slug))
				do := func(req *http.Request) *httptest.ResponseRecorder {
					req.Header.Set("Authorization", "Bearer "+e.key)
					w := httptest.NewRecorder()
					e.h.ServeHTTP(w, req)
					return w
				}
				var rec *httptest.ResponseRecorder
				environment := ""
				var traffic *int
				if mode == "dark" {
					zero := 0
					traffic = &zero
				}
				if mode == "preview" {
					environment = "preview"
				}
				switch route {
				case "source-ref":
					e.gh.streamBody = nopReadCloser{bytes.NewReader(archive)}
					rec = e.post(t, "/v1/apps/"+slug+"/deployments/source-ref", api.SourceRefDeployRequest{Repo: "onebox-faas/hello", Ref: "0123456789abcdef0123456789abcdef01234567", Environment: environment, TrafficPercent: traffic})
				case "source-tarball", "legacy-multipart":
					sidecar, _ := json.Marshal(sidecarPayload{Environment: environment, TrafficPercent: traffic})
					parts := map[string]multipartPart{"tarball": {filename: "source.tar.gz", body: archive}, "sidecar": {body: sidecar}}
					path := "/v1/apps/" + slug + "/deployments/source-tarball"
					if route == "legacy-multipart" {
						path = "/v1/apps/" + slug + "/deployments"
						parts = map[string]multipartPart{"source": {filename: "source.tar.gz", body: archive}}
						if environment != "" {
							parts["environment"] = multipartPart{body: []byte(environment)}
						}
						if traffic != nil {
							parts["traffic_percent"] = multipartPart{body: []byte("0")}
						}
					}
					body, ct := multipartUpload(t, parts)
					req := httptest.NewRequest("POST", path, body)
					req.Header.Set("Content-Type", ct)
					rec = do(req)
				case "upload":
					options := api.UploadDeployOptions{Environment: environment}

					body, _ := json.Marshal(startUploadRequest{AppSlug: slug, TotalSize: int64(len(archive)), DeployOptions: &options})
					req := httptest.NewRequest("POST", "/v1/uploads", bytes.NewReader(body))
					req.Header.Set("Content-Type", "application/json")
					start := do(req)
					if start.Code != http.StatusCreated {
						t.Fatalf("start=%d %s", start.Code, start.Body)
					}
					var upload startUploadResponse
					if err := json.Unmarshal(start.Body.Bytes(), &upload); err != nil {
						t.Fatal(err)
					}
					req = httptest.NewRequest("PATCH", "/v1/uploads/"+upload.UploadID, bytes.NewReader(archive))
					req.Header.Set("Upload-Offset", "0")
					req.Header.Set("Content-Type", "application/offset+octet-stream")
					if chunk := do(req); chunk.Code != http.StatusOK {
						t.Fatalf("append=%d %s", chunk.Code, chunk.Body)
					}
					rec = do(httptest.NewRequest("POST", "/v1/uploads/"+upload.UploadID+"/commit", nil))
				}
				if rec.Code != http.StatusAccepted && rec.Code != http.StatusCreated {
					t.Fatalf("deploy=%d %s", rec.Code, rec.Body)
				}
				var dep api.DeploymentResponse
				if err := json.Unmarshal(rec.Body.Bytes(), &dep); err != nil {
					t.Fatal(err)
				}
				crons, err := e.store.ListCronsForApp(t.Context(), e.appID)
				if err != nil || len(crons) != 0 {
					t.Fatalf("early crons=%v err=%v", crons, err)
				}
				if changes, err := e.store.ReconcileDeploymentCrons(t.Context()); err != nil || len(changes) != 0 {
					t.Fatalf("pending changes=%v err=%v", changes, err)
				}
				status := state.DeployLive
				if mode == "failed" {
					status = state.DeployFailed
				}
				if err := e.store.UpdateDeploymentStatus(t.Context(), dep.ID, status, ""); err != nil {
					t.Fatal(err)
				}
				changes, err := e.store.ReconcileDeploymentCrons(t.Context())
				want := 0
				if mode == "ready" {
					want = 1
				}
				if err != nil || len(changes) != want {
					t.Fatalf("changes=%v err=%v want=%d", changes, err, want)
				}
				crons, err = e.store.ListCronsForApp(t.Context(), e.appID)
				if err != nil || len(crons) != want {
					t.Fatalf("ready crons=%v err=%v want=%d", crons, err, want)
				}
			})
		}
	}
}
