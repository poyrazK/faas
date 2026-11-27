package builderd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/frameworkprofile"
	"github.com/onebox-faas/faas/pkg/ociarchive"
	"github.com/onebox-faas/faas/pkg/state"
)

// uploadedImage returns the prebuilt image apid accepted for this deployment
// (ADR-20261127120000), or nil for an ordinary source build.
func uploadedImage(dep state.Deployment) *frameworkprofile.UploadedImage {
	profile, ok := persistedProfile(dep)
	if !ok || profile.UploadedImage == nil || dep.Kind != state.DeploymentKindDockerfile {
		return nil
	}
	return profile.UploadedImage
}

// sourceCapBytes is the plan's cap on what this build's source spool holds.
func sourceCapBytes(lim api.Limits, dep state.Deployment) int64 {
	if uploadedImage(dep) != nil {
		return lim.ImageArchiveMaxBytes()
	}
	return int64(lim.SourceTarballMaxMB) * 1024 * 1024
}

// completeUploadedImage finishes a build whose source is an already built
// image: it rechecks the archive apid inspected, places it where a builder
// VM leaves its export, and completes the build as the cache-hit path does,
// with no VM. imaged then builds the app layer exactly as for a Dockerfile
// build, verifying every layer's diff ID.
func (b *Builderd) completeUploadedImage(ctx context.Context, build state.Build, dep state.Deployment, app state.App, acct state.Account, srcHash string, uploaded *frameworkprofile.UploadedImage, buildStart time.Time) (BuildResult, error) {
	if app.RequireSigned || app.SecurityPolicy.RequiresSignedImage() {
		msg := "the app requires signed images, and an uploaded local image carries no signature"
		b.markFailed(ctx, build, state.FailureUserError, msg, buildStart)
		return BuildResult{}, errors.New("builderd: " + msg)
	}
	if b.cfg.ImageExportDir == "" {
		b.markFailed(ctx, build, state.FailureInfra, "uploaded images are not configured on this builder", buildStart)
		return BuildResult{}, errors.New("builderd: image export dir not configured")
	}
	if b.stopIfBuildCancelled(ctx, build) {
		return BuildResult{}, nil
	}
	b.ops.ObserveBuildQueueWait(time.Since(build.EnqueuedAt))
	buildStart = time.Now()
	b.emitBuildLog(ctx, build.ID, "build started (uploaded image)\n")
	b.openImageBuildStage(ctx, dep.ID, build, buildStart)
	b.emitBuildLog(ctx, build.ID, fmt.Sprintf("[build] uploaded image %s — skipping vm spawn\n", uploaded.ManifestDigest))
	// Copy first, then inspect the copy: what imaged reads is exactly what
	// was checked, in a file builderd owns.
	layerPath, size, err := placeUploadedImage(b.cfg.ImageExportDir, build.ID, dep.SourcePath)
	if err != nil {
		b.markFailed(ctx, build, state.FailureInfra, "stage uploaded image: "+err.Error(), buildStart)
		return BuildResult{}, err
	}
	if err := checkUploadedImage(layerPath, uploaded.ManifestDigest); err != nil {
		_ = os.Remove(layerPath)
		b.markFailed(ctx, build, state.FailureUserError, err.Error(), buildStart)
		return BuildResult{}, err
	}
	return b.completeBuild(ctx, build, dep, app, acct, srcHash, "", BuildResult{BuildID: build.ID, LayerPath: layerPath, LayerBytes: size}, buildStart)
}

// checkUploadedImage re-inspects the placed export and confirms it is the
// image apid accepted.
func checkUploadedImage(path, wantDigest string) error {
	f, err := os.Open(path) //nolint:forbidigo // source path validated against the spool root by the caller.
	if err != nil {
		return fmt.Errorf("builderd: open uploaded image: %w", err)
	}
	defer func() { _ = f.Close() }()
	summary, err := ociarchive.Inspect(f)
	if err != nil {
		return fmt.Errorf("builderd: uploaded image: %w", err)
	}
	if summary.ManifestDigest != wantDigest {
		return fmt.Errorf("builderd: uploaded image is %s, not the accepted %s", summary.ManifestDigest, wantDigest)
	}
	return nil
}

// placeUploadedImage writes the archive to <root>/<build-id>/build/out/image.tar,
// the canonical buildexport shape. It copies rather than hard-linking the
// apid-owned spool file, so the export has builderd's owner and a 0644 mode
// like a VM build's. The export sweeper owns it from here.
func placeUploadedImage(root, buildID, source string) (string, int64, error) {
	dir := filepath.Join(root, buildID, "build", "out")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", 0, err
	}
	dest := filepath.Join(dir, "image.tar")
	if err := copyFile(source, dest); err != nil { // a retried claim replaces its earlier copy
		return "", 0, err
	}
	info, err := os.Stat(dest)
	if err != nil {
		return "", 0, err
	}
	return dest, info.Size(), nil
}

func copyFile(source, dest string) error {
	in, err := os.Open(source) //nolint:forbidigo // validated spool path.
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	tmp := dest + ".partial"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644) //nolint:forbidigo // builderd-owned export path.
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dest)
}
