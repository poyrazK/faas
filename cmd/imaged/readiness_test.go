// Tests for cmd/imaged/readiness.go (issue #571 PR-A2).

package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/wire"
)

func TestBuildReadinessProbe_HappyPath(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "cache"), 0o755); err != nil {
		t.Fatal(err)
	}
	p := BuildReadinessProbe(root)
	defer p.Drain("", nil)
	if p == nil {
		t.Fatal("BuildReadinessProbe returned nil")
	}
	// First check fires synchronously inside the goroutine.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if r, _ := p.All(); r {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if r, reason := p.All(); !r {
		t.Errorf("happy path: All() = false (reason=%q), want true", reason)
	}
}

func TestBuildReadinessProbe_MissingRoot(t *testing.T) {
	// Storage root's parent doesn't exist either — sentinel
	// creation fails. /readyz must surface the failing
	// reason. This is the "deploy misconfigured" path.
	bogus := "/nonexistent-parent-12345/root"
	p := BuildReadinessProbe(bogus)
	defer p.Drain("", nil)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if r, reason := p.All(); !r && reason != "" {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	r, reason := p.All()
	if r {
		t.Errorf("missing root: All() = true, want false")
	}
	if reason == "" {
		t.Errorf("reason = \"\", want contains an OS-error string")
	}
}

func TestBuildReadinessProbe_NonWritablePath(t *testing.T) {
	// Storage root is a read-only directory — the open(O_CREATE)
	// check fails with EACCES.
	root := t.TempDir()
	if err := os.Chmod(root, 0o555); err != nil {
		t.Fatal(err)
	}
	// Reset perms at test teardown so t.TempDir can clean up.
	t.Cleanup(func() { _ = os.Chmod(root, 0o755) })

	p := BuildReadinessProbe(root)
	defer p.Drain("", nil)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if r, reason := p.All(); !r && (strings.Contains(reason, "permission") || strings.Contains(reason, "denied")) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	r, reason := p.All()
	if r {
		t.Errorf("read-only root: All() = true, want false")
	}
	if !strings.Contains(strings.ToLower(reason), "permission") && !strings.Contains(strings.ToLower(reason), "denied") {
		t.Errorf("reason = %q, want contains permission/denied", reason)
	}
}

func TestBuildReadinessProbe_EndToEndViaControlMuxLite(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "cache"), 0o755); err != nil {
		t.Fatal(err)
	}
	p := BuildReadinessProbe(root)
	defer p.Drain("", nil)
	time.Sleep(50 * time.Millisecond)
	mux := http.NewServeMux()
	wire.ControlMuxLite(mux, p.ReadyFunc(), p.ReasonFunc())
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("/readyz code = %d, want 200 (body=%q)", rr.Code, rr.Body.String())
	}
}

func TestBuildReadinessProbe_DrainFlipsFalse(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "cache"), 0o755); err != nil {
		t.Fatal(err)
	}
	p := BuildReadinessProbe(root)
	p.Drain("", nil)
	r, reason := p.All()
	if r {
		t.Errorf("after Drain: All() = true, want false")
	}
	if !strings.Contains(reason, "draining") {
		t.Errorf("reason = %q, want contains \"draining\"", reason)
	}
}

func TestOCIReadinessChecksActualCacheInsteadOfLocalBackendRoot(t *testing.T) {
	cache := t.TempDir()
	p := buildImageReadinessProbe("oci", filepath.Join(cache, "unused-local-root"), cache)
	defer p.Drain("", nil)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if ready, _ := p.All(); ready {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	_, reason := p.All()
	t.Fatalf("actual writable cache never ready: %s", reason)
}
