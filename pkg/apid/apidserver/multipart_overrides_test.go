package apidserver_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

// adr: 053
// A source upload's `overrides` field used to be dropped as an unknown
// multipart field, so the deployment ran without the caller's port or
// healthcheck. Both multipart deploy routes now refuse it before creating a
// deployment.
func TestMultipartDeployRejectsOverrides(t *testing.T) {
	for _, tc := range []struct{ name, path, sourceField string }{
		{name: "deployments", path: "/deployments", sourceField: "source"},
		{name: "source tarball", path: "/deployments/source-tarball", sourceField: "tarball"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("FAAS_SPOOL_ROOT", t.TempDir())
			e := apidserver.Setup(t, api.PlanPro)
			if rec := e.Do(t, "POST", "/v1/apps", api.CreateAppRequest{Slug: "port-pin"}, nil); rec.Code != http.StatusCreated {
				t.Fatalf("create app: %d %s", rec.Code, rec.Body)
			}
			var body bytes.Buffer
			mw := multipart.NewWriter(&body)
			src, err := mw.CreateFormFile(tc.sourceField, "src.tar.gz")
			if err != nil {
				t.Fatal(err)
			}
			_, _ = src.Write(nodeSourceTarGz(t))
			if err := mw.WriteField("overrides", `{"port":5432}`); err != nil {
				t.Fatal(err)
			}
			if err := mw.Close(); err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest("POST", "/v1/apps/port-pin"+tc.path, &body)
			req.Header.Set("Authorization", "Bearer "+*e.Key())
			req.Header.Set("Content-Type", mw.FormDataContentType())
			rec := httptest.NewRecorder()
			(*e.H()).ServeHTTP(rec, req)
			if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "hosting.port") {
				t.Fatalf("status = %d body = %s, want 400 naming gregale.yaml's hosting.port", rec.Code, rec.Body)
			}
			app, err := (*e.Store()).AppBySlug(t.Context(), "port-pin")
			if err != nil {
				t.Fatal(err)
			}
			if dep, err := (*e.Store()).LatestDeployment(t.Context(), app.ID); err == nil {
				t.Fatalf("rejected upload created deployment %s", dep.ID)
			}
		})
	}
}

func nodeSourceTarGz(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, content := range map[string]string{
		"package.json": `{"name":"port-pin","scripts":{"start":"node index.js"}}`,
		"index.js":     "require('http').createServer((q, s) => s.end('ok')).listen(5432);\n",
	} {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(content))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
