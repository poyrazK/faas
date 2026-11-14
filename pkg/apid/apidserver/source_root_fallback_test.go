package apidserver_test

import (
	"archive/tar"
	"bytes"
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

// A raw API deploy of one project service's directory failed with 400
// `source_root "ui" is not present in the source archive` until the caller
// sent source_root=. (2026-11-12 agent feedback). An archive cropped to the
// configured root now builds from its own root when that root holds an app
// marker; an explicit source_root and a marker-less archive still fail.
func TestSourceDeployCroppedArchiveUsesArchiveRoot(t *testing.T) {
	dockerfile := []byte("FROM busybox\nCMD [\"httpd\", \"-f\"]\n")
	for _, tc := range []struct {
		name       string
		files      map[string][]byte
		sourceRoot string
		status     int
		wantRoot   string
		wantDetail string
	}{
		{name: "cropped archive", files: map[string][]byte{"Dockerfile": dockerfile}, status: http.StatusAccepted, wantRoot: ""},
		{name: "repository archive keeps the configured root", files: map[string][]byte{"ui/Dockerfile": dockerfile, "api/Dockerfile": dockerfile}, status: http.StatusAccepted, wantRoot: "ui"},
		{name: "cropped archive without an app marker", files: map[string][]byte{"README.md": []byte("hi\n")}, status: http.StatusBadRequest, wantDetail: "configured root"},
		{name: "explicit source_root is not replaced", files: map[string][]byte{"Dockerfile": dockerfile}, sourceRoot: "ui", status: http.StatusBadRequest, wantDetail: "source_root=."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("FAAS_SPOOL_ROOT", t.TempDir())
			e := apidserver.Setup(t, api.PlanPro)
			if rec := e.Do(t, "POST", "/v1/apps", api.CreateAppRequest{Slug: "retail-ui"}, nil); rec.Code != http.StatusCreated {
				t.Fatalf("create app: %d %s", rec.Code, rec.Body)
			}
			app, err := (*e.Store()).AppBySlug(t.Context(), "retail-ui")
			if err != nil {
				t.Fatal(err)
			}
			root := "ui"
			if _, err := (*e.Store()).UpdateApp(t.Context(), app.ID, state.UpdateAppParams{RootDir: &root}); err != nil {
				t.Fatal(err)
			}
			headers := make([]tar.Header, 0, len(tc.files))
			for name := range tc.files {
				headers = append(headers, tar.Header{Name: name})
			}
			var body bytes.Buffer
			form := multipart.NewWriter(&body)
			source, _ := form.CreateFormFile("source", "src.tar.gz")
			_, _ = source.Write(apidserver.BuildTestTarGz(t, headers, tc.files))
			_ = form.WriteField("dockerfile", "true")
			if tc.sourceRoot != "" {
				_ = form.WriteField("source_root", tc.sourceRoot)
			}
			_ = form.Close()
			req := httptest.NewRequest("POST", "/v1/apps/retail-ui/deployments", &body)
			req.Header.Set("Authorization", "Bearer "+*e.Key())
			req.Header.Set("Content-Type", form.FormDataContentType())
			rec := httptest.NewRecorder()
			(*e.H()).ServeHTTP(rec, req)
			if rec.Code != tc.status {
				t.Fatalf("status = %d, want %d (%s)", rec.Code, tc.status, rec.Body)
			}
			if tc.status != http.StatusAccepted {
				if !strings.Contains(rec.Body.String(), tc.wantDetail) {
					t.Fatalf("problem lacks %q: %s", tc.wantDetail, rec.Body)
				}
				return
			}
			var response api.DeploymentResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			deployment, err := (*e.Store()).LatestDeployment(t.Context(), response.AppID)
			if err != nil {
				t.Fatal(err)
			}
			if deployment.SourceRoot != tc.wantRoot {
				t.Fatalf("deployment source root = %q, want %q", deployment.SourceRoot, tc.wantRoot)
			}
			if after, _ := (*e.Store()).AppBySlug(t.Context(), "retail-ui"); after.RootDir != "ui" {
				t.Fatalf("app root dir changed to %q", after.RootDir)
			}
		})
	}
}
