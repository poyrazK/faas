package imaged

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/rootfs"
	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/wire"
)

// patchingBuilder is a callCountingBuilder that can also patch guest-init,
// like *rootfs.Builder.
type patchingBuilder struct {
	callCountingBuilder
	patches  []rootfs.BasePatchInput
	patchErr error
}

func (b *patchingBuilder) PatchBaseGuestInit(ctx context.Context, in rootfs.BasePatchInput) (rootfs.BaseBuildResult, error) {
	b.patches = append(b.patches, in)
	if b.patchErr != nil {
		return rootfs.BaseBuildResult{}, b.patchErr
	}
	if err := in.Storage.Put(ctx, in.StorageKey, strings.NewReader("patched ext4 "+in.GuestInitSHA256)); err != nil {
		return rootfs.BaseBuildResult{}, err
	}
	return rootfs.BaseBuildResult{ImageKey: in.StorageKey, SizeBytes: 1}, nil
}

type patchHarness struct {
	h         *Handler
	b         *patchingBuilder
	be        storage.StorageBackend
	mp        *minimalManifestPuller
	guestInit string
	scans     int
}

const (
	patchRef     = "ghcr.io/onebox-faas/runner-node22@sha256:4444444444444444444444444444444444444444444444444444444444444444"
	patchBaseKey = "base/runner-node22-amd64.ext4"
	patchDigKey  = "base/runner-node22-amd64.ext4.digest"
)

// newPatchHarness stages patchBaseKey once with guest-init v1.
func newPatchHarness(t *testing.T) *patchHarness {
	t.Helper()
	ph := &patchHarness{b: &patchingBuilder{}, mp: newTwoLayerPuller(t)}
	ph.guestInit = filepath.Join(t.TempDir(), "faas-guest-init")
	ph.writeGuestInit(t, "guest-init-v1")
	hs := newBaseHarness(t, ph.mp, ph.b)
	ph.h, ph.be = hs.h, hs.be
	ph.h.guestInitPath = ph.guestInit
	ph.h.grypeRun = func(context.Context, string) (*ScanResult, error) {
		ph.scans++
		return &ScanResult{}, nil
	}
	if _, err := ph.stage(); err != nil {
		t.Fatalf("initial stage: %v", err)
	}
	if ph.b.calls != 1 || len(ph.b.patches) != 0 {
		t.Fatalf("initial stage = %d builds %d patches, want 1 build", ph.b.calls, len(ph.b.patches))
	}
	return ph
}

func (ph *patchHarness) writeGuestInit(t *testing.T, body string) {
	t.Helper()
	if err := os.WriteFile(ph.guestInit, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

func (ph *patchHarness) stage() (BaseStageResult, error) {
	return ph.h.EnsureBaseExt4(context.Background(), patchRef, patchBaseKey, patchDigKey, "", "", "")
}

func (ph *patchHarness) read(t *testing.T, key string) string {
	t.Helper()
	rc, err := ph.be.Get(context.Background(), key)
	if err != nil {
		t.Fatalf("get %s: %v", key, err)
	}
	defer func() { _ = rc.Close() }()
	b, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestEnsureBaseExt4_PatchesGuestInitWhenOnlyGuestInitChanged(t *testing.T) {
	ph := newPatchHarness(t)
	scansBefore := ph.scans
	ph.writeGuestInit(t, "guest-init-v2")
	want, err := guestInitBinaryDigest(ph.guestInit)
	if err != nil {
		t.Fatal(err)
	}

	res, err := ph.stage()
	if err != nil {
		t.Fatalf("EnsureBaseExt4: %v", err)
	}
	if res.Skipped || res.StorageKey != patchBaseKey || res.ConfigDigest == "" {
		t.Fatalf("result = %+v, want a published (not skipped) %s", res, patchBaseKey)
	}
	if ph.b.calls != 1 {
		t.Fatalf("BuildBase calls = %d, want the patch to replace the rebuild", ph.b.calls)
	}
	if len(ph.b.patches) != 1 {
		t.Fatalf("patches = %d, want 1", len(ph.b.patches))
	}
	p := ph.b.patches[0]
	if p.GuestInitPath != ph.guestInit || p.GuestInitSHA256 != want || p.StorageKey != patchBaseKey || p.SourceImage == "" {
		t.Fatalf("patch input = %+v", p)
	}
	if got := ph.read(t, patchBaseKey); got != "patched ext4 "+want {
		t.Fatalf("published base = %q, want the patched artifact", got)
	}
	if sidecar := ph.read(t, patchDigKey); sidecar != baseDigestSidecarValueWithSource(res.ConfigDigest, want, patchRef) {
		t.Fatalf("digest sidecar = %q, want the new guest-init recorded", sidecar)
	}
	content := ph.read(t, baseContentKey(patchBaseKey))
	if !strings.Contains(content, `"size":`+strconv.Itoa(len("patched ext4 "+want))) {
		t.Fatalf("content sidecar %s does not describe the patched bytes", content)
	}
	if ph.scans != scansBefore+1 {
		t.Fatalf("grype scans = %d, want the patched artifact rescanned once", ph.scans-scansBefore)
	}
	if _, err := ph.be.Get(context.Background(), wire.ScanKeyForBaseKey(patchBaseKey)); err != nil {
		t.Fatalf("scan sidecar missing after patch: %v", err)
	}

	// The next start finds every sidecar current and skips.
	again, err := ph.stage()
	if err != nil {
		t.Fatal(err)
	}
	if !again.Skipped || ph.b.calls != 1 || len(ph.b.patches) != 1 {
		t.Fatalf("restart = skipped:%v builds:%d patches:%d, want a skip", again.Skipped, ph.b.calls, len(ph.b.patches))
	}
}

func TestEnsureBaseExt4_RebuildsWhenGuestInitPatchFails(t *testing.T) {
	ph := newPatchHarness(t)
	ph.b.patchErr = errors.New("debugfs: no space")
	ph.writeGuestInit(t, "guest-init-v2")
	res, err := ph.stage()
	if err != nil {
		t.Fatalf("EnsureBaseExt4: %v", err)
	}
	if res.Skipped || len(ph.b.patches) != 1 || ph.b.calls != 2 {
		t.Fatalf("result skipped:%v patches:%d builds:%d, want a refused patch then a rebuild",
			res.Skipped, len(ph.b.patches), ph.b.calls)
	}
	if got := ph.read(t, patchBaseKey); got != "fake ext4" {
		t.Fatalf("published base = %q, want the rebuilt artifact", got)
	}
}

func TestEnsureBaseExt4_DoesNotPatchWhenTheBaseItselfChanged(t *testing.T) {
	for name, mutate := range map[string]func(t *testing.T, ph *patchHarness){
		"OCI config digest changed": func(t *testing.T, ph *patchHarness) {
			ph.mp.manifest.Config.Digest = "sha256:5555555555555555555555555555555555555555555555555555555555555555"
			ph.writeGuestInit(t, "guest-init-v2")
		},
		"base layout changed": func(t *testing.T, ph *patchHarness) {
			sidecar := ph.read(t, patchDigKey)
			stale := strings.Replace(sidecar, baseLayoutVersion, "faas-base-layout-v2", 1)
			if err := ph.be.Put(context.Background(), patchDigKey, strings.NewReader(stale)); err != nil {
				t.Fatal(err)
			}
			ph.writeGuestInit(t, "guest-init-v2")
		},
		"sidecar names another source ref": func(t *testing.T, ph *patchHarness) {
			sidecar := ph.read(t, patchDigKey)
			other := strings.Replace(sidecar, patchRef, "ghcr.io/onebox-faas/runner-node24@sha256:"+strings.Repeat("6", 64), 1)
			if err := ph.be.Put(context.Background(), patchDigKey, strings.NewReader(other)); err != nil {
				t.Fatal(err)
			}
			ph.writeGuestInit(t, "guest-init-v2")
		},
		"sidecar predates guest-init tracking": func(t *testing.T, ph *patchHarness) {
			sidecar := ph.read(t, patchDigKey)
			configDigest := strings.SplitN(sidecar, "\n", 2)[0]
			if err := ph.be.Put(context.Background(), patchDigKey, strings.NewReader(baseDigestSidecarValue(configDigest))); err != nil {
				t.Fatal(err)
			}
			ph.writeGuestInit(t, "guest-init-v2")
		},
	} {
		t.Run(name, func(t *testing.T) {
			ph := newPatchHarness(t)
			mutate(t, ph)
			res, err := ph.stage()
			if err != nil {
				t.Fatalf("EnsureBaseExt4: %v", err)
			}
			if res.Skipped || len(ph.b.patches) != 0 || ph.b.calls != 2 {
				t.Fatalf("result skipped:%v patches:%d builds:%d, want a full rebuild",
					res.Skipped, len(ph.b.patches), ph.b.calls)
			}
		})
	}
}

func TestEnsureBaseExt4_DoesNotPatchWithoutALocalArtifact(t *testing.T) {
	ph := newPatchHarness(t)
	resolver := ph.be.(storage.LocalPathResolver)
	path, _, err := resolver.LocalPath(patchBaseKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	ph.writeGuestInit(t, "guest-init-v2")
	if _, err := ph.stage(); err != nil {
		t.Fatalf("EnsureBaseExt4: %v", err)
	}
	if len(ph.b.patches) != 0 || ph.b.calls != 2 {
		t.Fatalf("patches:%d builds:%d, want a rebuild when the staged artifact is gone", len(ph.b.patches), ph.b.calls)
	}
}

func TestParseBaseDigestSidecarFields(t *testing.T) {
	const cfg = "sha256:3333333333333333333333333333333333333333333333333333333333333333"
	gi := strings.Repeat("a", 64)
	for _, tc := range []struct {
		name              string
		sidecar           string
		wantInit, wantSrc string
		wantOK            bool
	}{
		{name: "full", sidecar: baseDigestSidecarValueWithSource(cfg, gi, patchRef), wantInit: gi, wantSrc: patchRef, wantOK: true},
		{name: "no source", sidecar: baseDigestSidecarValueWithGuestInit(cfg, gi), wantInit: gi, wantOK: true},
		{name: "no guest-init", sidecar: baseDigestSidecarValueWithSource(cfg, "", patchRef), wantSrc: patchRef, wantOK: true},
		{name: "legacy", sidecar: baseDigestSidecarValue(cfg), wantOK: true},
		{name: "old layout", sidecar: cfg + "\nfaas-base-layout-v2\nguest-init-sha256=" + gi},
		{name: "malformed guest-init", sidecar: baseDigestSidecarValue(cfg) + "\nguest-init-sha256=xyz"},
		{name: "trailing junk", sidecar: baseDigestSidecarValueWithSource(cfg, gi, patchRef) + "\nextra"},
		{name: "bad digest", sidecar: "sha256:zz\n" + baseLayoutVersion},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gotCfg, gotInit, gotSrc, ok := parseBaseDigestSidecarFields(tc.sidecar)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if !ok {
				return
			}
			if gotCfg != cfg || gotInit != tc.wantInit || gotSrc != tc.wantSrc {
				t.Fatalf("got (%q, %q, %q), want (%q, %q, %q)", gotCfg, gotInit, gotSrc, cfg, tc.wantInit, tc.wantSrc)
			}
		})
	}
}
