package main

import (
	"archive/tar"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// ADR-987: binary is a function runtime. Its deployments build from the
// source's Dockerfile; every other function runtime still refuses one.

const binaryDockerfile = "FROM scratch\nCOPY handler /app/handler\nENTRYPOINT [\"/app/handler\"]\n"

func deployBinaryFunction(t *testing.T, e testEnv, slug, runtime string, files map[string][]byte) *httptest.ResponseRecorder {
	t.Helper()
	entries := make([]tar.Header, 0, len(files))
	for name := range files {
		entries = append(entries, tar.Header{Name: name})
	}
	raw := buildTestTarGz(t, entries, files)
	body, ct := multipartUpload(t, map[string]multipartPart{
		"source":  {filename: "src.tar.gz", body: raw},
		"runtime": {body: []byte(runtime)},
		"handler": {body: []byte("handler")},
	})
	req := httptest.NewRequest("POST", "/v1/apps/"+slug+"/deployments", body)
	req.Header.Set("Authorization", "Bearer "+e.key)
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec
}

func TestCreateApp_FunctionBinaryRuntime(t *testing.T) {
	e := setup(t, api.PlanPro)
	rec := e.do(t, "POST", "/v1/apps",
		api.CreateAppRequest{Slug: "fn-binary-rt", Type: "function", Runtime: "binary"}, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("function with runtime=binary: %d %s", rec.Code, rec.Body)
	}
	var out api.AppResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Runtime != "binary" || out.Type != "function" {
		t.Fatalf("round-trip = type %q runtime %q", out.Type, out.Runtime)
	}
}

func TestCreateDeploymentMultipart_BinaryFunctionDockerfileRule(t *testing.T) {
	t.Setenv("FAAS_SPOOL_ROOT", t.TempDir())
	e := setup(t, api.PlanPro)
	e.do(t, "POST", "/v1/apps", api.CreateAppRequest{Slug: "fn-bin", Type: "function", Runtime: "binary"}, nil)

	withDockerfile := deployBinaryFunction(t, e, "fn-bin", "binary", map[string][]byte{
		"Dockerfile": []byte(binaryDockerfile),
		"handler":    []byte("\x7fELF-static"),
	})
	if withDockerfile.Code != http.StatusAccepted {
		t.Fatalf("binary deploy with a Dockerfile: %d %s", withDockerfile.Code, withDockerfile.Body)
	}

	withoutDockerfile := deployBinaryFunction(t, e, "fn-bin", "binary", map[string][]byte{
		"handler": []byte("\x7fELF-static"),
	})
	if withoutDockerfile.Code != http.StatusBadRequest || !strings.Contains(withoutDockerfile.Body.String(), "Dockerfile required") {
		t.Fatalf("binary deploy without a Dockerfile: %d %s", withoutDockerfile.Code, withoutDockerfile.Body)
	}

	e.do(t, "POST", "/v1/apps", api.CreateAppRequest{Slug: "fn-node-df", Type: "function", Runtime: "node22"}, nil)
	nodeWithDockerfile := deployBinaryFunction(t, e, "fn-node-df", "node22", map[string][]byte{
		"Dockerfile": []byte(binaryDockerfile),
		"handler.js": []byte("exports.handler = () => 1;\n"),
	})
	if nodeWithDockerfile.Code != http.StatusBadRequest || !strings.Contains(nodeWithDockerfile.Body.String(), "Dockerfile functions unsupported") {
		t.Fatalf("node22 deploy with a Dockerfile: %d %s", nodeWithDockerfile.Code, nodeWithDockerfile.Body)
	}
}
