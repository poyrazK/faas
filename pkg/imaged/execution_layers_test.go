package imaged

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
)

// recordingMkfs stands in for mkfs.ext4: it checks the staged tree and writes
// the marker it found as the "image" so the published object can be asserted.
type recordingMkfs struct {
	calls   int
	markers []string
}

func (r *recordingMkfs) Run(_ context.Context, argv []string) error {
	r.calls++
	tree, image := argv[len(argv)-3], argv[len(argv)-2]
	if argv[0] != "mkfs.ext4" || argv[len(argv)-1] != "16M" {
		return os.ErrInvalid
	}
	marker, err := os.ReadFile(filepath.Join(tree, "upper", "etc", "faas", "execution.json"))
	if err != nil {
		return err
	}
	for _, dir := range []string{"work", "merged"} {
		if _, err := os.Stat(filepath.Join(tree, dir)); err != nil {
			return err
		}
	}
	r.markers = append(r.markers, string(marker))
	return os.WriteFile(image, marker, 0o644)
}

func TestEnsureExecutionLayersStagesEveryProfileOnce(t *testing.T) {
	root := t.TempDir()
	be, err := storage.NewLocalStorageBackend(root)
	if err != nil {
		t.Fatalf("NewLocalStorageBackend: %v", err)
	}
	h := New(state.NewMemStore(), &fakeNotifier{}, fakePuller{}, &fakeBuilder{}, "./init", t.TempDir(), silentLogger()).WithStorage(be)
	mkfs := &recordingMkfs{}

	results, err := h.EnsureExecutionLayers(context.Background(), "amd64", t.TempDir(), mkfs)
	if err != nil {
		t.Fatalf("EnsureExecutionLayers: %v", err)
	}
	if len(results) != 2 || mkfs.calls != 2 {
		t.Fatalf("results=%d mkfs calls=%d, want 2/2", len(results), mkfs.calls)
	}
	for _, result := range results {
		rc, err := be.Get(context.Background(), result.StorageKey)
		if err != nil {
			t.Fatalf("layer %s not published: %v", result.StorageKey, err)
		}
		data, _ := io.ReadAll(rc)
		_ = rc.Close()
		var marker struct {
			Kind    string               `json:"kind"`
			Version int                  `json:"version"`
			Profile api.ExecutionProfile `json:"profile"`
		}
		if err := json.Unmarshal(data, &marker); err != nil || marker.Kind != "execution" || marker.Version != 1 {
			t.Fatalf("layer marker = %q (%v)", data, err)
		}
		wantProfile := api.ExecutionProfile("")
		if result.Profile != api.ExecutionProfileStandard {
			wantProfile = result.Profile
		}
		if marker.Profile != wantProfile {
			t.Fatalf("marker profile = %q, want %q", marker.Profile, wantProfile)
		}
		digest, err := readExecutionLayerDigest(context.Background(), be, sched.ExecutionLayerDigestKeyForArch(string(result.Profile), "amd64"))
		if err != nil || digest != result.MarkerDigest || !strings.HasPrefix(digest, "sha256:") {
			t.Fatalf("digest sidecar = %q (%v), want %q", digest, err, result.MarkerDigest)
		}
	}

	again, err := h.EnsureExecutionLayers(context.Background(), "amd64", t.TempDir(), mkfs)
	if err != nil {
		t.Fatalf("second EnsureExecutionLayers: %v", err)
	}
	if mkfs.calls != 2 {
		t.Fatalf("idempotent restage ran mkfs %d more times", mkfs.calls-2)
	}
	for _, result := range again {
		if !result.Skipped {
			t.Fatalf("unchanged layer %s was rebuilt", result.StorageKey)
		}
	}
}

func TestEnsureExecutionLayersRebuildsWhenSidecarStale(t *testing.T) {
	be, err := storage.NewLocalStorageBackend(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalStorageBackend: %v", err)
	}
	h := New(state.NewMemStore(), &fakeNotifier{}, fakePuller{}, &fakeBuilder{}, "./init", t.TempDir(), silentLogger()).WithStorage(be)
	key := sched.ExecutionLayerDigestKeyForArch(string(api.ExecutionProfileStandard), "amd64")
	if err := be.Put(context.Background(), key, strings.NewReader("sha256:stale\n")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	mkfs := &recordingMkfs{}
	if _, err := h.EnsureExecutionLayers(context.Background(), "amd64", t.TempDir(), mkfs); err != nil {
		t.Fatalf("EnsureExecutionLayers: %v", err)
	}
	if mkfs.calls != 2 {
		t.Fatalf("mkfs calls = %d, want 2", mkfs.calls)
	}
}

func TestExecutionMarkerMatchesGuestContract(t *testing.T) {
	standard, err := ExecutionMarker(api.ExecutionProfileStandard)
	if err != nil || string(standard) != "{\"kind\":\"execution\",\"version\":1}\n" {
		t.Fatalf("standard marker = %q (%v)", standard, err)
	}
	data, err := ExecutionMarker(api.ExecutionProfilePythonDataV1)
	if err != nil || string(data) != "{\"kind\":\"execution\",\"version\":1,\"profile\":\"python-data-v1\"}\n" {
		t.Fatalf("profile marker = %q (%v)", data, err)
	}
}
