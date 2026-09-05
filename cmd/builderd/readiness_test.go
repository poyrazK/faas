// Tests for cmd/builderd/readiness.go (issue #571 PR-A2).

package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/wire"
)

// fakePGPool satisfies pgPool via a closure so we don't need
// the real pgxpool in the test binary.
type fakePGPool struct {
	pingFn func(ctx context.Context) error
}

func (f *fakePGPool) Ping(ctx context.Context) error {
	return f.pingFn(ctx)
}

func TestBuildReadinessProbe_HappyPath(t *testing.T) {
	root := t.TempDir()
	buildsDir := filepath.Join(root, "builds")
	if err := os.MkdirAll(buildsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	pool := &fakePGPool{pingFn: func(ctx context.Context) error { return nil }}
	// No vmmd dial seam — pass a stub dial so we don't reach
	// for grpc; happy-path check: empty target yields "vmmd
	// target empty" which is acceptable for the happy-path test
	// because the OTHER two signals are ready.
	dial := func(ctx context.Context, target string) error {
		if target == "" {
			return errors.New("vmmd target empty")
		}
		return nil
	}
	p := BuildReadinessProbe(context.Background(), pool, root, "", dial)
	defer p.Drain("", nil)
	if p == nil {
		t.Fatal("BuildReadinessProbe returned nil")
	}
	// Wait for the PG ping + buildsDir to flip ready; the vmmd
	// signal intentionally stays not-ready with target empty.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		r, reason := p.All()
		if !r && strings.Contains(reason, "vmmd") {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Errorf("happy path: All() never flipped to \"vmmd not ready\" (got ready=%v)", func() bool { r, _ := p.All(); return r }())
}

func TestBuildReadinessProbe_BuildsDirWritable(t *testing.T) {
	root := t.TempDir()
	buildsDir := filepath.Join(root, "builds")
	if err := os.MkdirAll(buildsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	pool := &fakePGPool{pingFn: func(ctx context.Context) error { return nil }}
	dial := func(ctx context.Context, target string) error { return nil }
	p := BuildReadinessProbe(context.Background(), pool, root, "unix:///run/faas/vmmd.sock", dial)
	defer p.Drain("", nil)
	// Probe must flip ready (PG + buildsDir + vmmd dial all OK).
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if r, _ := p.All(); r {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	if r, reason := p.All(); !r {
		t.Errorf("All() = false (reason=%q), want true", reason)
	}
}

func TestBuildReadinessProbe_PGPingFails(t *testing.T) {
	root := t.TempDir()
	buildsDir := filepath.Join(root, "builds")
	if err := os.MkdirAll(buildsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	pool := &fakePGPool{pingFn: func(ctx context.Context) error {
		return errors.New("connection refused")
	}}
	dial := func(ctx context.Context, target string) error { return nil }
	p := BuildReadinessProbe(context.Background(), pool, root, "unix:///run/faas/vmmd.sock", dial)
	defer p.Drain("", nil)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if r, reason := p.All(); !r && (strings.Contains(reason, "pg") || strings.Contains(reason, "connection")) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	r, reason := p.All()
	if r {
		t.Errorf("PG ping failing: All() = true, want false")
	}
	if !strings.Contains(strings.ToLower(reason), "pg") && !strings.Contains(strings.ToLower(reason), "connection") {
		t.Errorf("reason = %q, want contains pg/connection", reason)
	}
}

func TestBuildReadinessProbe_VmmdDialFails(t *testing.T) {
	root := t.TempDir()
	buildsDir := filepath.Join(root, "builds")
	if err := os.MkdirAll(buildsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	pool := &fakePGPool{pingFn: func(ctx context.Context) error { return nil }}
	dial := func(ctx context.Context, target string) error {
		return errors.New("connection refused")
	}
	p := BuildReadinessProbe(context.Background(), pool, root, "unix:///run/faas/vmmd.sock", dial)
	defer p.Drain("", nil)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if r, reason := p.All(); !r && strings.Contains(reason, "vmmd dial") {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	r, reason := p.All()
	if r {
		t.Errorf("vmmd dial failing: All() = true, want false")
	}
	if !strings.Contains(reason, "vmmd dial") {
		t.Errorf("reason = %q, want contains \"vmmd dial\"", reason)
	}
}

func TestBuildReadinessProbe_NilPool(t *testing.T) {
	root := t.TempDir()
	buildsDir := filepath.Join(root, "builds")
	if err := os.MkdirAll(buildsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	dial := func(ctx context.Context, target string) error { return nil }
	p := BuildReadinessProbe(context.Background(), nil, root, "unix:///run/faas/vmmd.sock", dial)
	defer p.Drain("", nil)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if r, reason := p.All(); !r && strings.Contains(reason, "pg pool nil") {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	r, reason := p.All()
	if r {
		t.Errorf("nil pool: All() = true, want false")
	}
	if !strings.Contains(reason, "pg pool nil") {
		t.Errorf("reason = %q, want contains \"pg pool nil\"", reason)
	}
}

func TestBuildReadinessProbe_EndToEndViaControlMuxLite(t *testing.T) {
	root := t.TempDir()
	buildsDir := filepath.Join(root, "builds")
	if err := os.MkdirAll(buildsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	pool := &fakePGPool{pingFn: func(ctx context.Context) error { return nil }}
	dial := func(ctx context.Context, target string) error { return nil }
	p := BuildReadinessProbe(context.Background(), pool, root, "unix:///run/faas/vmmd.sock", dial)
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
	buildsDir := filepath.Join(root, "builds")
	if err := os.MkdirAll(buildsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	pool := &fakePGPool{pingFn: func(ctx context.Context) error { return nil }}
	dial := func(ctx context.Context, target string) error { return nil }
	p := BuildReadinessProbe(context.Background(), pool, root, "unix:///run/faas/vmmd.sock", dial)
	p.Drain("", nil)
	r, reason := p.All()
	if r {
		t.Errorf("after Drain: All() = true, want false")
	}
	// At least one signal should report draining.
	if !strings.Contains(reason, "draining") {
		t.Errorf("reason = %q, want contains \"draining\"", reason)
	}
}

func TestCheckDirWritable(t *testing.T) {
	dir := t.TempDir()
	if err := checkDirWritable(dir); err != nil {
		t.Errorf("checkDirWritable(%q) = %v, want nil", dir, err)
	}
}

func TestCheckDirWritable_NotWritable(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	if err := checkDirWritable(dir); err == nil {
		t.Errorf("checkDirWritable(%q read-only) = nil, want error", dir)
	}
}

func TestReadinessUsesConfiguredDriveWithoutAddingBuilds(t *testing.T) {
	drive := t.TempDir()
	pool := &fakePGPool{pingFn: func(context.Context) error { return nil }}
	p := buildReadinessProbeForDrive(context.Background(), pool, drive, "unused", func(context.Context, string) error { return nil })
	defer p.Drain("", nil)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if ready, _ := p.All(); ready {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	_, reason := p.All()
	t.Fatalf("configured drive never ready: %s", reason)
}
