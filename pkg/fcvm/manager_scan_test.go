// Whitebox tests for pkg/fcvm/manager.go::bringUpScanCheck — the
// issue #299 admission gate that refuses to boot a base ext4 whose
// Grype scan sidecar reports fix-available CRITICAL ≥ 1 (or is
// missing/malformed). Legacy sidecars remain gated on total CRITICAL.
//
// Pattern: whitebox-test-file convention (memory:
// whitebox-test-file-pattern), narrowly scoped to the
// supply-chain-gate seam so a refactor that changes the gate
// semantics fails loudly at unit-test time. Uses an in-memory
// fake StorageBackend (pkg/storage has no in-memory backend so a
// trivial map-backed fake lives here in the test file).
package fcvm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/wire"
)

// memStorage is a trivial pkg/storage.StorageBackend the gate tests
// inject via Manager.WithStorage. LocalStorageBackend (the only
// production-mirrored backend) would require a tmpdir + json-encoding
// sidecars per case, which is exactly the friction this in-memory
// backend exists to skip. When a future PR adds a memory backend to
// pkg/storage, this local copy can be replaced.
type memStorage struct {
	mu     sync.Mutex
	blobs  map[string][]byte
	getErr error // optional — when set, Get returns this error
}

func (m *memStorage) Put(_ context.Context, key string, r io.Reader) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.blobs == nil {
		m.blobs = map[string][]byte{}
	}
	b, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	m.blobs[key] = b
	return nil
}
func (m *memStorage) Get(_ context.Context, key string) (io.ReadCloser, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.getErr != nil {
		return nil, m.getErr
	}
	b, ok := m.blobs[key]
	if !ok {
		return nil, errors.New("fcvm-test: storage miss")
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}
func (m *memStorage) Delete(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.blobs, key)
	return nil
}
func (m *memStorage) Stat(_ context.Context, key string) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.blobs[key]
	if !ok {
		return 0, errors.New("fcvm-test: stat miss")
	}
	return int64(len(b)), nil
}

// TestBringUpScanCheck is the table-driven test for the
// admission-gate seam (Critical #4 of PR #385 review). Every case
// constructs a Manager with a memStorage containing either a
// well-formed sidecar, a missing one, a malformed one, or a
// fail-closed placeholder — plus a control case where storage is
// nil (so WithStorage was never wired). The control is the only
// case that returns nil; every other case must surface a typed
// *api.Problem so schedd's wake-error path can render it without
// a translation layer.
func TestBringUpScanCheck(t *testing.T) {
	const baseKey = "base/runner-builder-amd64.ext4"
	const scanKey = "scans/runner-builder-amd64.ext4.scan.json"

	cases := []struct {
		name       string
		setup      func(*memStorage)
		wantErr    bool
		wantCode   string
		wantStatus int
	}{
		{
			name: "missing sidecar returns CodeScanCritical",
			setup: func(s *memStorage) {
				// No Put — Get returns the storage-miss error.
			},
			wantErr:    true,
			wantCode:   api.CodeScanCritical,
			wantStatus: http.StatusServiceUnavailable,
		},
		{
			name: "malformed JSON returns CodeScanCritical",
			setup: func(s *memStorage) {
				_ = s.Put(context.Background(), scanKey, bytes.NewReader([]byte("not-json {{{")))
			},
			wantErr:    true,
			wantCode:   api.CodeScanCritical,
			wantStatus: http.StatusServiceUnavailable,
		},
		{
			name: "missing findings returns CodeScanCritical",
			setup: func(s *memStorage) {
				_ = s.Put(context.Background(), scanKey, bytes.NewReader([]byte(`{"image":"broken"}`)))
			},
			wantErr:    true,
			wantCode:   api.CodeScanCritical,
			wantStatus: http.StatusServiceUnavailable,
		},
		{
			name: "incomplete fix-available findings returns CodeScanCritical",
			setup: func(s *memStorage) {
				blob, _ := json.Marshal(map[string]any{
					"image":                  "ghcr.io/onebox-faas/runner-builder:latest",
					"findings":               map[string]int{"CRITICAL": 7},
					"fix_available_findings": map[string]int{},
				})
				_ = s.Put(context.Background(), scanKey, bytes.NewReader(blob))
			},
			wantErr:    true,
			wantCode:   api.CodeScanCritical,
			wantStatus: http.StatusServiceUnavailable,
		},
		{
			name: "fail-closed placeholder (CRITICAL=9999) returns CodeScanCritical",
			setup: func(s *memStorage) {
				blob, _ := json.Marshal(map[string]any{
					"image": "ghcr.io/onebox-faas/runner-builder:latest",
					"findings": map[string]int{
						"CRITICAL": 9999, "HIGH": 9999, "MEDIUM": 9999,
						"LOW": 9999, "UNKNOWN": 0,
					},
				})
				_ = s.Put(context.Background(), scanKey, bytes.NewReader(blob))
			},
			wantErr:    true,
			wantCode:   api.CodeScanCritical,
			wantStatus: http.StatusServiceUnavailable,
		},
		{
			name: "CRITICAL=1 returns CodeScanCritical",
			setup: func(s *memStorage) {
				blob, _ := json.Marshal(map[string]any{
					"image":    "ghcr.io/onebox-faas/runner-builder:latest",
					"findings": map[string]int{"CRITICAL": 1, "HIGH": 3, "MEDIUM": 5, "LOW": 2, "UNKNOWN": 0},
				})
				_ = s.Put(context.Background(), scanKey, bytes.NewReader(blob))
			},
			wantErr:    true,
			wantCode:   api.CodeScanCritical,
			wantStatus: http.StatusServiceUnavailable,
		},
		{
			name: "unfixed CRITICAL findings are admitted",
			setup: func(s *memStorage) {
				blob, _ := json.Marshal(map[string]any{
					"image":                  "ghcr.io/onebox-faas/runner-builder:latest",
					"findings":               map[string]int{"CRITICAL": 7, "HIGH": 3, "MEDIUM": 5, "LOW": 2, "UNKNOWN": 0},
					"fix_available_findings": map[string]int{"CRITICAL": 0, "HIGH": 1, "MEDIUM": 0, "LOW": 0, "UNKNOWN": 0},
				})
				_ = s.Put(context.Background(), scanKey, bytes.NewReader(blob))
			},
			wantErr: false,
		},
		{
			name: "fix-available CRITICAL=1 returns CodeScanCritical",
			setup: func(s *memStorage) {
				blob, _ := json.Marshal(map[string]any{
					"image":                  "ghcr.io/onebox-faas/runner-builder:latest",
					"findings":               map[string]int{"CRITICAL": 7, "HIGH": 3, "MEDIUM": 5, "LOW": 2, "UNKNOWN": 0},
					"fix_available_findings": map[string]int{"CRITICAL": 1, "HIGH": 1, "MEDIUM": 0, "LOW": 0, "UNKNOWN": 0},
				})
				_ = s.Put(context.Background(), scanKey, bytes.NewReader(blob))
			},
			wantErr:    true,
			wantCode:   api.CodeScanCritical,
			wantStatus: http.StatusServiceUnavailable,
		},
		{
			name: "all-zero findings is admitted",
			setup: func(s *memStorage) {
				blob, _ := json.Marshal(map[string]any{
					"image":    "ghcr.io/onebox-faas/runner-builder:latest",
					"findings": map[string]int{"CRITICAL": 0, "HIGH": 0, "MEDIUM": 0, "LOW": 0, "UNKNOWN": 0},
				})
				_ = s.Put(context.Background(), scanKey, bytes.NewReader(blob))
			},
			wantErr: false,
		},
		{
			name: "high-severity only is admitted (CRITICAL==0)",
			setup: func(s *memStorage) {
				blob, _ := json.Marshal(map[string]any{
					"image":    "ghcr.io/onebox-faas/runner-builder:latest",
					"findings": map[string]int{"CRITICAL": 0, "HIGH": 12, "MEDIUM": 30, "LOW": 100, "UNKNOWN": 4},
				})
				_ = s.Put(context.Background(), scanKey, bytes.NewReader(blob))
			},
			wantErr: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &memStorage{}
			if tc.setup != nil {
				tc.setup(s)
			}
			m := &Manager{}
			m.WithStorage(s)
			err := m.bringUpScanCheck(context.Background(), baseKey)
			if (err != nil) != tc.wantErr {
				t.Fatalf("bringUpScanCheck err = %v, wantErr = %v", err, tc.wantErr)
			}
			if !tc.wantErr {
				return
			}
			var prob *api.Problem
			if !errors.As(err, &prob) {
				t.Fatalf("expected *api.Problem, got %T (%v)", err, err)
			}
			if prob.Code != tc.wantCode {
				t.Errorf("Code = %q, want %q", prob.Code, tc.wantCode)
			}
			if prob.Status != tc.wantStatus {
				t.Errorf("Status = %d, want %d", prob.Status, tc.wantStatus)
			}
		})
	}
}

// TestBringUpScanCheck_NilStorageIsNoop confirms the control case —
// a Manager that never called WithStorage must allow bringUp to
// proceed without a scan check (the existing unit tests in
// manager_test.go take this path; the gate is purely additive).
func TestBringUpScanCheck_NilStorageIsNoop(t *testing.T) {
	m := &Manager{}
	if err := m.bringUpScanCheck(context.Background(), "base/anything.ext4"); err != nil {
		t.Errorf("nil storage must skip scan check, got %v", err)
	}
}

// TestBringUpScanCheck_GetErrorSurfacesAsProblem: when the storage
// backend returns ANY error from Get (not just "not found"), the
// gate emits the same CodeScanCritical. Defence-in-depth — a
// permissions error, a transient network blip to a remote backend,
// etc. all funnel through the same Problem shape so schedd's
// wake-error path doesn't branch on the storage layer's failure
// taxonomy.
func TestBringUpScanCheck_GetErrorSurfacesAsProblem(t *testing.T) {
	s := &memStorage{getErr: errors.New("permission denied")}
	m := &Manager{}
	m.WithStorage(s)
	err := m.bringUpScanCheck(context.Background(), "base/anything.ext4")
	if err == nil {
		t.Fatal("expected error from getErr")
	}
	var prob *api.Problem
	if !errors.As(err, &prob) {
		t.Fatalf("expected *api.Problem, got %T", err)
	}
	if prob.Code != api.CodeScanCritical {
		t.Errorf("Code = %q, want %q", prob.Code, api.CodeScanCritical)
	}
}

// TestScanKeyForBaseKeyFormat pins the storage key shape that
// bridges imaged's sidecar write (pkg/imaged/base_stage.go) and
// vmmd's read (Manager.bringUpScanCheck). The constant lives in
// pkg/wire so both sides import the same function — but a future
// refactor that swaps an explicit key string for an alg, or that
// drops the .scan.json suffix, would silently break the bridge.
// Pin it here.
func TestScanKeyForBaseKeyFormat(t *testing.T) {
	cases := []struct {
		baseKey, want string
	}{
		{
			baseKey: "base/runner-node22-amd64.ext4",
			want:    "scans/runner-node22-amd64.ext4.scan.json",
		},
		{
			baseKey: "base/runner-builder-amd64.ext4",
			want:    "scans/runner-builder-amd64.ext4.scan.json",
		},
		{
			// Production only ever produces keys with the
			// canonical "base/" prefix (per-arch partition in
			// sched.BaseScanKey / wire.BaseScanKeyForArch).
			// A malformed key without that prefix must still
			// funnel through the gate's get-miss path —
			// confirm the fallback route gets a deterministic
			// key under scans/ rather than panicking. The
			// gate surfaces any malformed key as
			// *api.Problem{Code: scan_critical}.
			baseKey: "no-prefix.ext4",
			want:    "scans/no-prefix.ext4.scan.json",
		},
		{
			// Empty string is the boundary case; the helper
			// returns the root scans/.scan.json placeholder
			// so callers never see a non-deterministic key.
			baseKey: "",
			want:    "scans/.scan.json",
		},
	}
	for _, tc := range cases {
		if got := wire.ScanKeyForBaseKey(tc.baseKey); got != tc.want {
			t.Errorf("ScanKeyForBaseKey(%q) = %q, want %q", tc.baseKey, got, tc.want)
		}
	}
}
