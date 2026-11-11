package internal

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/guest/runners/internal/workerpool"
	"github.com/onebox-faas/faas/pkg/fnadapter"
	"github.com/onebox-faas/faas/pkg/functionlifetime"
)

func TestFunctionHookNeedsReservedPathAndMatchingMarker(t *testing.T) {
	for _, tc := range []struct{ path, marker, want string }{
		{functionlifetime.FunctionAfterRestorePath, "after_restore", "after_restore"},
		{functionlifetime.FunctionBeforeSnapshotPath, "before_checkpoint", "before_snapshot"},
		{functionlifetime.FunctionAfterRestorePath, "before_checkpoint", ""},
		{functionlifetime.FunctionAfterRestorePath, "", ""},
		{"/internal/after-restore", "after_restore", ""},
	} {
		if got := functionlifetime.FunctionHook(tc.path, tc.marker); got != tc.want {
			t.Errorf("FunctionHook(%q, %q) = %q, want %q", tc.path, tc.marker, got, tc.want)
		}
	}
}

// ADR-20261110190000 §3–4: guest-init's call on the reserved path runs the
// function's hook in its workers; a caller without guest-init's loopback
// marker reaches the handler as an ordinary request instead; a throwing hook
// answers 503 so guest-init rejects the restored instance.
func TestServeRunsFunctionHooksForGuestInit(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"type":"module"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	handler := `const state = { after: 0, calls: 0 };
export default {
  afterRestore() { if (process.env.HOOK_FAIL === "1") throw new Error("boom"); state.after++; },
  async fetch() { state.calls++; return Response.json(state); },
};`
	if err := os.WriteFile(filepath.Join(dir, "handler.js"), []byte(handler), 0o644); err != nil {
		t.Fatal(err)
	}
	adapter := filepath.Join(dir, "adapter.mjs")
	if err := os.WriteFile(adapter, []byte(fnadapter.Node), 0o644); err != nil {
		t.Fatal(err)
	}
	serve := func(t *testing.T, env ...string) *httptest.Server {
		spec := workerpool.Spec{Executable: node, Args: []string{adapter, t.Name()}, Env: append(os.Environ(), env...), HandlerPath: adapter}
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			Serve(w, r, ServeConfig{Runtime: "node24", Spec: spec})
		}))
		t.Cleanup(srv.Close)
		return srv
	}
	get := func(t *testing.T, srv *httptest.Server) string {
		resp, err := http.Get(srv.URL + "/")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		body, _ := io.ReadAll(resp.Body)
		return string(body)
	}
	hook := func(t *testing.T, srv *httptest.Server, marked bool) int {
		req, _ := http.NewRequest(http.MethodPost, srv.URL+functionlifetime.FunctionAfterRestorePath, nil)
		if marked {
			req.Header.Set("X-Faas-After-Restore", "1")
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		return resp.StatusCode
	}

	t.Run("runs", func(t *testing.T) {
		srv := serve(t)
		if got := get(t, srv); !strings.Contains(got, `"calls":1`) {
			t.Fatalf("first request = %s", got)
		}
		if status := hook(t, srv, true); status != http.StatusNoContent {
			t.Fatalf("hook status = %d, want 204", status)
		}
		if got := get(t, srv); !strings.Contains(got, `"after":1`) || !strings.Contains(got, `"calls":2`) {
			t.Fatalf("after hook = %s", got)
		}
		// Without guest-init's marker the reserved path is an ordinary request.
		if status := hook(t, srv, false); status != http.StatusOK {
			t.Fatalf("unmarked call status = %d, want the handler's 200", status)
		}
		if got := get(t, srv); !strings.Contains(got, `"after":1`) {
			t.Fatalf("unmarked call ran the hook: %s", got)
		}
	})
	t.Run("fails", func(t *testing.T) {
		srv := serve(t, "HOOK_FAIL=1")
		_ = get(t, srv)
		if status := hook(t, srv, true); status != http.StatusServiceUnavailable {
			t.Fatalf("failing hook status = %d, want 503", status)
		}
	})
}
