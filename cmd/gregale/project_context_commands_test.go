package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLinkedContextDefaultsAcrossAppScopedReads(t *testing.T) {
	resetJSONOut(t)
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := saveProjectContext(root, localProjectContext{
		Version: projectContextVersion,
		Project: "shop",
		App:     "demo",
	}); err != nil {
		t.Fatal(err)
	}
	documentPath := filepath.Join(root, "openapi.json")
	if err := os.WriteFile(documentPath, []byte(`{"openapi":"3.1.0","paths":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)

	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		switch r.URL.Path {
		case "/v1/apps/demo/openapi":
			_, _ = w.Write([]byte(`{"openapi":"3.1.0","paths":{}}`))
		case "/v1/apps/demo/edge-rules", "/v1/apps/demo/instances":
			_, _ = w.Write([]byte(`[]`))
		case "/v1/apps/demo/invoke", "/v1/apps/demo/invoke/async":
			_, _ = w.Write([]byte(`{"id":"inv-1","status":"completed"}`))
		default:
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "test-token")

	oldOut := osStdout
	osStdout = &bytes.Buffer{}
	t.Cleanup(func() { osStdout = oldOut })
	jsonOutput = true

	tests := []struct {
		name string
		run  func() int
		path string
	}{
		{"inspect", func() int { return cmdInspect([]string{"--upstreams"}) }, "/v1/apps/demo/upstreams"},
		{"deployments", func() int { return cmdDeployments(nil) }, "/v1/apps/demo/deployments"},
		{"analytics", func() int { return cmdAnalytics(nil) }, "/v1/apps/demo/analytics"},
		{"invoke", func() int { return cmdInvoke([]string{"--async"}) }, "/v1/apps/demo/invoke/async"},
		{"slo", func() int { return cmdSLO(nil) }, "/v1/apps/demo/slo"},
		{"ps", func() int { return cmdPS(nil) }, "/v1/apps/demo/instances"},
		{"wake timeline", func() int { return cmdWakeTimeline([]string{"wake-1"}) }, "/v1/apps/demo/wakes/wake-1/timeline"},
		{"cors ls", func() int { return cmdCorsLs(nil) }, "/v1/apps/demo/edge-rules"},
		{"cors show", func() int { return cmdCorsShow(nil) }, "/v1/apps/demo/edge-rules"},
		{"openapi get", func() int { return cmdOpenapiGet(nil) }, "/v1/apps/demo/openapi"},
		{"openapi preview", func() int { return cmdOpenapiPreview(nil) }, "/v1/apps/demo/openapi/diff"},
		{"openapi dry-run", func() int { return cmdOpenapiDryRun([]string{documentPath}) }, "/v1/apps/demo/openapi/dry-run"},
		{"tail", func() int { return cmdTail(nil) }, "/v1/events"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := len(paths)
			if code := tt.run(); code != 0 {
				t.Fatalf("exit = %d, want 0", code)
			}
			if len(paths) == before {
				t.Fatal("command made no request")
			}
			if got := paths[len(paths)-1]; got != tt.path {
				t.Errorf("last request path = %q, want %q", got, tt.path)
			}
		})
	}
}

func TestLinkedContextRequiredCommandExplainsHowToRecover(t *testing.T) {
	resetJSONOut(t)
	root := t.TempDir()
	t.Chdir(root)
	_, readStderr, restore := swapIO(t)
	defer restore()

	if code := cmdAnalytics(nil); code != 1 {
		t.Fatalf("analytics without slug or context = %d, want 1", code)
	}
	if stderr := readStderr(); !strings.Contains(stderr, "gregale link <project-slug>") {
		t.Fatalf("stderr missing recovery hint: %s", stderr)
	}
}
