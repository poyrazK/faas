package journeys

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/mcpmanagement"
)

// Exercise the complete remote template protocol and archive preparation with
// the real trusted CLI. Native requests are confined to this test's key fake;
// no builder, application or external platform is contacted.
func TestHarnessMCPHostedToolSourceWithCLI(t *testing.T) {
	cli := os.Getenv("GREGALE_JOURNEY_TEST_CLI")
	if cli == "" {
		t.Skip("set GREGALE_JOURNEY_TEST_CLI to a matching local CLI")
	}
	const key = "fp_test_0123456789abcdef0123456789abcdef"
	native := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/keys":
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "publisher-key", "plaintext": key, "scopes": []string{"agent", "apps:read", "deploy:write"}})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/account":
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "owner", "key_scopes": []string{"agent", "apps:read", "deploy:write"}})
		case r.Method == http.MethodDelete && r.URL.Path == "/v1/keys/publisher-key":
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("starter preparation requested unexpected native route: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	t.Cleanup(native.Close)
	bridge := httptest.NewUnstartedServer(nil)
	uploads := filepath.Join(t.TempDir(), "uploads")
	handler, err := mcpmanagement.NewHTTPHandler(mcpmanagement.HTTPOptions{
		APIBase: native.URL, ResourceURL: "https://" + bridge.Listener.Addr().String() + "/mcp", Hosted: true, AgentKeys: true, AllowWrites: true, AllowToolPublishing: true,
		RedirectURIs: []string{"http://localhost/callback"},
		Toolsets:     []string{mcpmanagement.ToolsetBuildTools}, UploadDir: uploads, CLIRunner: mcpmanagement.HostedCLIRunner(cli, native.URL, uploads),
	})
	if err != nil {
		t.Fatal(err)
	}
	bridge.Config.Handler = handler
	bridge.Start()
	t.Cleanup(bridge.Close)
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	t.Cleanup(cancel)
	j := &mcpToolJourney{root: t, ctx: ctx, tg: &target{cli: cli, home: t.TempDir(), api: native.URL, mcpURL: bridge.URL + "/mcp", token: key, runID: "hs3p9a", mcpHostedTools: true}}
	j.scaffold(t)
	body, err := archiveJourneyToolSource(j.path)
	if err != nil {
		t.Fatal(err)
	}
	gz, err := gzip.NewReader(bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = gz.Close() }()
	archive := tar.NewReader(gz)
	found := map[string]bool{}
	for {
		entry, err := archive.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		content, err := io.ReadAll(archive)
		if err != nil {
			t.Fatal(err)
		}
		original, err := os.ReadFile(filepath.Join(j.path, entry.Name))
		if err != nil || !bytes.Equal(original, content) {
			t.Fatalf("archive changed starter file %s", entry.Name)
		}
		found[entry.Name] = true
	}
	for _, name := range []string{"server.js", "package.json", "gregale-mcp.json", "gregale-mcp.tests.json", "journey-tools.mjs"} {
		if !found[name] {
			t.Fatalf("hosted source omitted %s", name)
		}
	}
}
