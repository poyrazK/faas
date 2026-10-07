package imaged

// adr: 682

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/oci"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

func TestImmutableRuntimeReleaseSurvivesNewPublication(t *testing.T) {
	b := &fakeBuilder{}
	f := newBaseHarness(t, newTwoLayerPuller(t), b)
	s := state.NewMemStore()
	guest := filepath.Join(t.TempDir(), "guest-init")
	if err := os.WriteFile(guest, []byte("exact PID 1"), 0600); err != nil {
		t.Fatal(err)
	}
	f.h.guestInitPath = guest
	ref := "ghcr.io/test/node@sha256:" + strings.Repeat("1", 64)
	first, err := f.h.ensureRuntimeRelease(t.Context(), s, "node22", "amd64", ref)
	if err != nil {
		t.Fatal(err)
	}
	again, err := f.h.ensureRuntimeRelease(t.Context(), s, "node22", "amd64", ref)
	if err != nil || first.ID != again.ID || len(b.calls) != 1 {
		t.Fatal("rebuilt immutable input", again, err, len(b.calls))
	}
	if _, err := f.be.Get(t.Context(), wire.ScanKeyForBaseKey(first.BaseKey())); err != nil {
		t.Fatal("lost scan gate", err)
	}
	second, err := f.h.ensureRuntimeRelease(t.Context(), s, "node22", "amd64", "ghcr.io/test/node@sha256:"+strings.Repeat("2", 64))
	if err != nil || second.ID == first.ID {
		t.Fatal(second, err)
	}
	if err := f.h.verifyRuntimeRelease(t.Context(), first); err != nil {
		t.Fatal("new base invalidated old release", err)
	}
	if err := f.be.Put(t.Context(), first.BaseKey(), strings.NewReader("tampered bytes")); err != nil {
		t.Fatal(err)
	}
	if _, err := f.h.ensureRuntimeRelease(t.Context(), s, "node22", "amd64", ref); err == nil {
		t.Fatal("corrupt immutable generation silently replaced")
	}
	if err := f.be.Delete(t.Context(), second.BaseKey()); err != nil {
		t.Fatal(err)
	}
	if err := f.h.verifyRuntimeRelease(t.Context(), second); err == nil {
		t.Fatal("missing generation", err)
	}
	if _, err := f.h.ensureRuntimeRelease(t.Context(), s, "node22", "amd64", "ghcr.io/test/node:latest"); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatal("mutable source accepted", err)
	}
}

type recordedRuntimeManifestPuller struct {
	*minimalManifestPuller
	ref string
}

func (p *recordedRuntimeManifestPuller) PullManifest(ctx context.Context, ref string) (oci.Manifest, error) {
	p.ref = ref
	return p.minimalManifestPuller.PullManifest(ctx, ref)
}
func TestFunctionArtifactUsesRecordedRuntimeInsteadOfDaemonDefault(t *testing.T) {
	archive := goArtifactFixture(t, "app/handler.js", map[string]any{})
	config := minimalConfigBytes(0)
	digest := digestFor(t, config)
	pull := &recordedRuntimeManifestPuller{minimalManifestPuller: &minimalManifestPuller{manifest: oci.Manifest{Config: oci.Descriptor{Digest: digest}}, layers: map[string][]byte{digest: config}}}
	h := &Handler{oci: pull, deployBaseRefOverride: "ghcr.io/test/new-default@sha256:" + strings.Repeat("2", 64)}
	recorded := "ghcr.io/test/recorded@sha256:" + strings.Repeat("1", 64)
	layers, _, cleanup, err := h.functionBuildArtifactForRef(t.Context(), RuntimeNode22, archive, recorded)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if pull.ref != recorded || len(layers) != 1 {
		t.Fatal("used daemon default", pull.ref, len(layers))
	}
}
