package apidserver_test

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

// Source deploys dropped readiness_probe before the deployment row, and
// gregale.yaml could not declare one (2026-11-12 agent feedback; #3557 built
// the manifest key but never reached main). hosting.readiness_probe now
// supplies it, and a request field wins for that one deployment.
func TestSourceDeployReadinessProbe(t *testing.T) {
	for _, tc := range []struct {
		name     string
		manifest string
		field    string
		want     string
		status   int
	}{
		{name: "from gregale.yaml", manifest: "hosting:\n  readiness_probe:\n    path: /ready\n    period_s: 5\n", want: "/ready", status: http.StatusAccepted},
		{name: "request field wins", manifest: "hosting:\n  readiness_probe:\n    path: /ready\n", field: `{"path":"/from-flag"}`, want: "/from-flag", status: http.StatusAccepted},
		{name: "request field alone", field: `{"path":"/only-flag"}`, want: "/only-flag", status: http.StatusAccepted},
		{name: "none", status: http.StatusAccepted},
		{name: "invalid manifest path", manifest: "hosting:\n  readiness_probe:\n    path: ready\n", status: http.StatusUnprocessableEntity},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("FAAS_SPOOL_ROOT", t.TempDir())
			e := apidserver.Setup(t, api.PlanHobby)
			if rec := e.Do(t, "POST", "/v1/apps", api.CreateAppRequest{Slug: "ready-app"}, nil); rec.Code != http.StatusCreated {
				t.Fatalf("create app: %d %s", rec.Code, rec.Body)
			}
			headers := []tar.Header{{Name: "index.js"}}
			files := map[string][]byte{"index.js": []byte("require('http').createServer((q, s) => s.end('ok')).listen(8080)\n")}
			if tc.manifest != "" {
				headers = append(headers, tar.Header{Name: "gregale.yaml"})
				files["gregale.yaml"] = []byte(tc.manifest)
			}
			var body bytes.Buffer
			form := multipart.NewWriter(&body)
			source, _ := form.CreateFormFile("source", "src.tar.gz")
			_, _ = source.Write(apidserver.BuildTestTarGz(t, headers, files))
			if tc.field != "" {
				_ = form.WriteField("readiness_probe", tc.field)
			}
			_ = form.Close()
			req := httptest.NewRequest("POST", "/v1/apps/ready-app/deployments", &body)
			req.Header.Set("Authorization", "Bearer "+*e.Key())
			req.Header.Set("Content-Type", form.FormDataContentType())
			rec := httptest.NewRecorder()
			(*e.H()).ServeHTTP(rec, req)
			if rec.Code != tc.status {
				t.Fatalf("status = %d, want %d (%s)", rec.Code, tc.status, rec.Body)
			}
			if tc.status != http.StatusAccepted {
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
			if tc.want == "" {
				if len(deployment.OverrideReadinessProbe) != 0 {
					t.Fatalf("unexpected readiness probe %s", deployment.OverrideReadinessProbe)
				}
				return
			}
			var probe api.DeploymentReadinessProbe
			if err := json.Unmarshal(deployment.OverrideReadinessProbe, &probe); err != nil || probe.Path != tc.want {
				t.Fatalf("readiness probe = %s (%v), want path %s", deployment.OverrideReadinessProbe, err, tc.want)
			}
		})
	}
}
