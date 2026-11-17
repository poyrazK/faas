package rootfs

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/storage"
)

// ErrManifestPatchUnsupported reports that a published app layer cannot have
// its runtime manifest replaced in place. Callers build the layer instead.
var ErrManifestPatchUnsupported = errors.New("rootfs: app manifest patch unsupported")

// ManifestPatch names a published app layer and the manifests it holds and
// should hold.
type ManifestPatch struct {
	// Source is the published layer; it is only read.
	Source PublishedLayer
	// Old is the manifest Source was built with. The patch refuses a layer
	// whose embedded manifest is not exactly Old's bytes.
	Old api.AppManifest
	// New replaces it.
	New api.AppManifest
}

// appManifestCandidates are where a built app layer keeps AppManifestPath:
// under /upper in a two-drive layer, at the root in a full rootfs.
var appManifestCandidates = []string{"/upper" + api.AppManifestPath, api.AppManifestPath}

// RepublishWithManifest publishes under key a copy of p.Source whose
// embedded app manifest is p.New.
//
// A deployment that changes only runtime settings (environment, ports,
// health checks, hooks...) used to convert its unchanged image again: the
// manifest is the one file of an app layer that depends on them, and the
// layer reuse key covered the whole manifest (Kroki feedback 2026-11-17).
// Like PatchBaseGuestInit this copies the image, swaps the file with debugfs
// (no mount, no root) at a pinned time, proves the old file was exactly
// p.Old and the new one is exactly p.New, runs a read-only e2fsck, then
// publishes and signs the result like a build. It returns the published
// identity and the change in content bytes.
func (b *Builder) RepublishWithManifest(ctx context.Context, be storage.StorageBackend, key string, p ManifestPatch) (ArtifactIdentity, int64, error) {
	run, ok := b.run.(outputRunner)
	if !ok {
		return ArtifactIdentity{}, 0, fmt.Errorf("%w: runner %T cannot capture output", ErrManifestPatchUnsupported, b.run)
	}
	if be == nil || key == "" || p.Source.Path == "" || p.Source.Digest == "" || p.Source.Bytes <= 0 {
		return ArtifactIdentity{}, 0, errors.New("rootfs: manifest patch needs storage, a key and a recorded source identity")
	}
	if err := p.New.Validate(); err != nil {
		return ArtifactIdentity{}, 0, err
	}
	var oldBytes, newBytes bytes.Buffer
	if err := api.WriteManifest(&oldBytes, p.Old); err != nil {
		return ArtifactIdentity{}, 0, err
	}
	if err := api.WriteManifest(&newBytes, p.New); err != nil {
		return ArtifactIdentity{}, 0, err
	}

	tmpPath, err := createBaseTemp("faas-layer-manifest-*.ext4")
	if err != nil {
		return ArtifactIdentity{}, 0, err
	}
	defer func() { _ = os.Remove(tmpPath) }()
	if !debugfsSafePath(tmpPath) {
		return ArtifactIdentity{}, 0, fmt.Errorf("%w: scratch path %q", ErrManifestPatchUnsupported, tmpPath)
	}
	if err := copyBaseImage(p.Source.Path, tmpPath); err != nil {
		return ArtifactIdentity{}, 0, err
	}
	if err := verifyFileIdentity(ctx, tmpPath, ArtifactIdentity{Digest: p.Source.Digest, Bytes: p.Source.Bytes}); err != nil {
		return ArtifactIdentity{}, 0, err
	}

	manifestPath, current, err := findAppManifest(ctx, run, tmpPath)
	if err != nil {
		return ArtifactIdentity{}, 0, err
	}
	got, err := debugfsDump(ctx, run, tmpPath, manifestPath)
	if err != nil {
		return ArtifactIdentity{}, 0, err
	}
	if !bytes.Equal(got, oldBytes.Bytes()) {
		return ArtifactIdentity{}, 0, fmt.Errorf("%w: embedded manifest differs from the recorded one", ErrManifestPatchUnsupported)
	}
	if err := writeAppManifest(ctx, run, tmpPath, manifestPath, current, newBytes.Bytes()); err != nil {
		return ArtifactIdentity{}, 0, err
	}
	after, err := debugfsStat(ctx, run, tmpPath, manifestPath)
	if err != nil {
		return ArtifactIdentity{}, 0, err
	}
	if after.Type != "regular" || after.Mode != current.Mode || after.User != current.User || after.Group != current.Group {
		return ArtifactIdentity{}, 0, fmt.Errorf("rootfs: patched manifest is %s mode %s owner %s:%s, want regular %s %s:%s",
			after.Type, after.Mode, after.User, after.Group, current.Mode, current.User, current.Group)
	}
	if got, err := debugfsDump(ctx, run, tmpPath, manifestPath); err != nil {
		return ArtifactIdentity{}, 0, err
	} else if !bytes.Equal(got, newBytes.Bytes()) {
		return ArtifactIdentity{}, 0, errors.New("rootfs: patched manifest does not hold the new bytes")
	}
	if _, err := run.Output(ctx, []string{"e2fsck", "-fn", tmpPath}); err != nil {
		return ArtifactIdentity{}, 0, fmt.Errorf("rootfs: patched app layer failed e2fsck: %w", err)
	}

	//nolint:forbidigo // tmpPath is this function's own scratch file.
	f, err := os.Open(tmpPath)
	if err != nil {
		return ArtifactIdentity{}, 0, fmt.Errorf("rootfs: open patched app layer: %w", err)
	}
	defer func() { _ = f.Close() }()
	identity, err := publishArtifactIdentity(ctx, be, key, f)
	if err != nil {
		return ArtifactIdentity{}, 0, fmt.Errorf("rootfs: publish patched app layer %q: %w", key, err)
	}
	if err := b.signPublished(ctx, key, identity); err != nil {
		return ArtifactIdentity{}, 0, fmt.Errorf("rootfs: sign %q: %w", key, err)
	}
	return identity, int64(newBytes.Len() - oldBytes.Len()), nil
}

func verifyFileIdentity(ctx context.Context, p string, want ArtifactIdentity) error {
	//nolint:forbidigo // p is a scratch copy this package made.
	f, err := os.Open(p)
	if err != nil {
		return fmt.Errorf("rootfs: open layer copy: %w", err)
	}
	defer func() { _ = f.Close() }()
	got, err := ReadArtifactIdentity(ctx, f)
	if err != nil {
		return fmt.Errorf("rootfs: read layer copy: %w", err)
	}
	if got != want {
		return fmt.Errorf("%w: source layer is %s (%d bytes), recorded %s (%d bytes)", ErrManifestPatchUnsupported, got.Digest, got.Bytes, want.Digest, want.Bytes)
	}
	return nil
}

func findAppManifest(ctx context.Context, run outputRunner, image string) (string, debugfsInode, error) {
	for _, p := range appManifestCandidates {
		ino, err := debugfsStat(ctx, run, image, p)
		if err != nil {
			continue
		}
		if ino.Type != "regular" || ino.User == "" || ino.Group == "" || len(ino.Mode) != 4 {
			return "", debugfsInode{}, fmt.Errorf("%w: %s is a %s", ErrManifestPatchUnsupported, p, ino.Type)
		}
		return p, ino, nil
	}
	return "", debugfsInode{}, fmt.Errorf("%w: no app manifest in the layer", ErrManifestPatchUnsupported)
}

func debugfsDump(ctx context.Context, run outputRunner, image, p string) ([]byte, error) {
	out := image + ".dump"
	_ = os.Remove(out)
	defer func() { _ = os.Remove(out) }()
	if _, err := run.Output(ctx, []string{"debugfs", "-R", "dump " + p + " " + out, image}); err != nil {
		return nil, fmt.Errorf("rootfs: debugfs dump %s: %w", p, err)
	}
	//nolint:forbidigo // out is this function's own scratch file.
	b, err := os.ReadFile(out)
	if err != nil {
		return nil, fmt.Errorf("rootfs: read dumped %s: %w", p, err)
	}
	return b, nil
}

// writeAppManifest replaces manifestPath with body, keeping its owner and
// mode. debugfs reports command failures on stderr but exits 0, so the
// caller verifies the result.
func writeAppManifest(ctx context.Context, run outputRunner, image, manifestPath string, current debugfsInode, body []byte) error {
	scratch := image + ".app.json"
	if err := os.WriteFile(scratch, body, 0o600); err != nil {
		return fmt.Errorf("rootfs: write manifest scratch: %w", err)
	}
	defer func() { _ = os.Remove(scratch) }()
	dir, name := path.Split(manifestPath)
	script := strings.Join([]string{
		"cd " + path.Clean(dir),
		"rm " + name,
		"write " + scratch + " " + name,
		"sif " + name + " mode 0100" + strings.TrimPrefix(current.Mode, "0"),
		"sif " + name + " uid " + current.User,
		"sif " + name + " gid " + current.Group,
		"",
	}, "\n")
	scriptPath := image + ".debugfs"
	if err := os.WriteFile(scriptPath, []byte(script), 0o600); err != nil {
		return fmt.Errorf("rootfs: write debugfs script: %w", err)
	}
	defer func() { _ = os.Remove(scriptPath) }()
	if _, err := run.Output(ctx, []string{
		"env", "E2FSPROGS_FAKE_TIME=" + basePatchFakeTime,
		"debugfs", "-w", "-f", scriptPath, image,
	}); err != nil {
		return fmt.Errorf("rootfs: debugfs replace app manifest: %w", err)
	}
	return nil
}
