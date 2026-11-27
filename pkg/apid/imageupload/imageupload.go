// Package imageupload holds apid's rules for prebuilt image uploads
// (ADR-20261127120000): `gregale deploy --local-image` sends a single-image
// OCI archive through the resumable upload session. It skips the source-tree
// checks and becomes a Dockerfile deployment whose build is already done;
// builderd hands the archive to imaged without a builder VM. apidserver keeps
// the wiring (the release flag and the handlers).
package imageupload

import (
	"errors"
	"fmt"
	"os"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidsource"
	"github.com/onebox-faas/faas/pkg/ociarchive"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/statefuldenylist"
)

// SizeCap validates the upload's format and returns the plan cap that
// applies to it, refusing a total size above it.
func SizeCap(app state.App, limits api.Limits, opts *api.UploadDeployOptions, total int64) (int64, *api.Problem) {
	if opts == nil || !opts.IsImageArchive() {
		if opts != nil {
			if prob := opts.ValidateSourceFormat(); prob != nil {
				return 0, prob
			}
		}
		maxBytes := int64(limits.SourceTarballMaxMB) * 1024 * 1024
		if total > maxBytes {
			return 0, api.ErrSourceTooLarge(limits, total)
		}
		return maxBytes, nil
	}
	if prob := AppProblem(app, *opts); prob != nil {
		return 0, prob
	}
	maxBytes := limits.ImageArchiveMaxBytes()
	if total > maxBytes {
		return 0, api.ErrImageArchiveTooLarge(limits, total)
	}
	return maxBytes, nil
}

// SpoolBudgetBytes is an account's budget for bytes in open upload sessions
// when it opens one more. Source uploads keep their budget of multiplier
// source tarballs. Opening an image archive may instead fill up to one app
// layer cap when that is larger, so image uploads do not multiply the
// control plane's spool use and source uploads gain nothing.
func SpoolBudgetBytes(limits api.Limits, imageArchive bool, multiplier int64) int64 {
	source := int64(limits.SourceTarballMaxMB) << 20 * multiplier
	if !imageArchive {
		return source
	}
	return max(source, limits.ImageArchiveMaxBytes())
}

// AppProblem refuses options and apps a prebuilt image cannot serve. It runs
// at session start and again at commit, because the app's policy can change
// while the bytes upload.
func AppProblem(app state.App, opts api.UploadDeployOptions) *api.Problem {
	if prob := opts.ValidateSourceFormat(); prob != nil {
		return prob
	}
	if app.Type == state.AppTypeFunction {
		return api.ErrImageArchiveInvalid("functions build from their runtime; deploy a local image to an app")
	}
	if app.RequireSigned || app.SecurityPolicy.RequiresSignedImage() {
		return api.ErrImageArchiveSignedImageRequired(app.Slug)
	}
	if hint, denied := statefuldenylist.Match(opts.ImageName); denied {
		return api.ErrStatelessOnlyViolation("image", fmt.Sprintf("%s runs a datastore, which loses its data when the app parks; %s", opts.ImageName, hint))
	}
	return nil
}

// Params is the Enqueue input for a committed image archive.
func Params(summary *ociarchive.Summary, name string) *apidsource.UploadedImage {
	if summary == nil {
		return nil
	}
	return &apidsource.UploadedImage{ManifestDigest: summary.ManifestDigest, Name: name, Port: summary.ExposedPort}
}

// Inspect verifies the committed archive's shape, digests and platform.
// imaged verifies every layer's diff ID when it builds the layer.
func Inspect(partPath string) (ociarchive.Summary, *api.Problem) {
	f, err := os.Open(partPath) //nolint:forbidigo // server-created upload spool path from the session row.
	if err != nil {
		return ociarchive.Summary{}, api.ErrImageArchiveInvalid("the uploaded archive is unreadable")
	}
	defer func() { _ = f.Close() }()
	summary, err := ociarchive.Inspect(f)
	if err != nil {
		if errors.Is(err, ociarchive.ErrInvalid) {
			return ociarchive.Summary{}, api.ErrImageArchiveInvalid(err.Error())
		}
		return ociarchive.Summary{}, api.ErrImageArchiveInvalid("the uploaded archive is unreadable")
	}
	return summary, nil
}
