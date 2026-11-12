package local_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/guest/runners/local"
	"github.com/onebox-faas/faas/pkg/fnadapter"
)

// `gregale dev --local` runs a function's init hook in each worker before
// it serves, as a deployed function does (ADR-20261110190000 slice 3).
func TestLocalFunctionRunsInitBeforeServing(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	dir := t.TempDir()
	handler := `let ready = "no";
export default {
  async init() { await new Promise((r) => setTimeout(r, 50)); ready = "yes"; },
  async fetch() { return new Response("init=" + ready); },
};
`
	if err := os.WriteFile(filepath.Join(dir, "handler.js"), []byte(handler), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"type":"module"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	adapter := filepath.Join(dir, "adapter.cjs")
	if err := os.WriteFile(adapter, []byte(fnadapter.NodeFor("handler.js")), 0o600); err != nil {
		t.Fatal(err)
	}
	fn := local.Function{Runtime: "node22", Executable: node, Args: []string{adapter}, Env: os.Environ(), HandlerPath: adapter}
	t.Cleanup(func() { local.Reload(fn) })
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := local.Prewarm(ctx, fn); err != nil {
		t.Fatalf("prewarm: %v", err)
	}
	srv := httptest.NewServer(local.Handler(fn))
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || !strings.Contains(string(body), "init=yes") {
		t.Fatalf("response = %d %q, want init to have finished before serving", resp.StatusCode, body)
	}
}
