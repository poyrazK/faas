// adr: 20261124075005 §3a — the agent pool's imaged publishes the platform
// desktop drive once, under a key named by the image's pinned digest, with
// the platform's full-rootfs cap; it refuses an unpinned reference or one
// that resolves to another digest, and publishes nothing when unset.
package imaged

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/oci"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storagekey"
)

func TestEnsureSandboxDesktopDrive(t *testing.T) {
	ctx := context.Background()
	digest := "sha256:" + strings.Repeat("d", 64)
	ref := "registry.example/gregale/sandbox-desktop:1@" + digest
	env := func(v string) func(string) string {
		return func(name string) string {
			if name == SandboxDesktopImageEnv {
				return v
			}
			return ""
		}
	}
	newHandler := func(resolved string) (*Handler, *fakeBuilder) {
		puller := &jobMaterializationPuller{digest: resolved,
			manifest: oci.Manifest{Layers: []oci.Descriptor{{Digest: "sha256:" + strings.Repeat("e", 64)}}},
			config:   oci.ImageConfig{Cmd: []string{"bash"}}, blob: []byte("gzip-tar-placeholder")}
		builder := &fakeBuilder{bytesOut: 123}
		root := t.TempDir()
		return New(state.NewMemStore(), &fakeNotifier{}, puller, builder, "./guest-init", root, silentLogger()).
			WithStorage(mustLocalStorage(t, root)), builder
	}

	h, builder := newHandler(digest)
	if key, _, err := h.EnsureSandboxDesktopDrive(ctx, "amd64", env("")); err != nil || key != "" || len(builder.fullRootfsCalls) != 0 {
		t.Fatalf("unset: key %q err %v builds %d", key, err, len(builder.fullRootfsCalls))
	}
	key, existed, err := h.EnsureSandboxDesktopDrive(ctx, "amd64", env(ref))
	if err != nil || existed || key != storagekey.SandboxDesktopImage(digest) {
		t.Fatalf("first stage: key %q existed %v err %v", key, existed, err)
	}
	if len(builder.fullRootfsCalls) != 1 || builder.fullRootfsCalls[0].Plan != api.PlanScale || builder.fullRootfsCalls[0].StorageKey != key {
		t.Fatalf("builds %+v", builder.fullRootfsCalls)
	}
	rc, err := h.storage.Get(ctx, key)
	if err != nil {
		t.Fatalf("the drive was not published: %v", err)
	}
	_, _ = io.Copy(io.Discard, rc)
	_ = rc.Close()
	if again, existed, err := h.EnsureSandboxDesktopDrive(ctx, "amd64", env(ref)); err != nil || !existed || again != key || len(builder.fullRootfsCalls) != 1 {
		t.Fatalf("second stage rebuilt: existed %v err %v builds %d", existed, err, len(builder.fullRootfsCalls))
	}

	for name, tc := range map[string]struct {
		ref, arch, resolved string
	}{
		"unpinned":      {"registry.example/gregale/sandbox-desktop:1", "amd64", digest},
		"other arch":    {ref, "arm64", digest},
		"moved digest":  {ref, "amd64", "sha256:" + strings.Repeat("f", 64)},
		"short digest":  {"registry.example/gregale/sandbox-desktop@sha256:abc", "amd64", digest},
		"upper hex too": {"registry.example/gregale/sandbox-desktop@sha256:" + strings.Repeat("D", 64), "amd64", digest},
	} {
		h, builder := newHandler(tc.resolved)
		if key, _, err := h.EnsureSandboxDesktopDrive(ctx, tc.arch, env(tc.ref)); err == nil || key != "" {
			t.Errorf("%s: key %q err %v, want a refusal", name, key, err)
		}
		if _, err := h.storage.Get(ctx, storagekey.SandboxDesktopImage(digest)); err == nil {
			t.Errorf("%s: a refused stage published the drive (%d builds)", name, len(builder.fullRootfsCalls))
		}
	}
}
