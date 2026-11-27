package apidserver

// Prebuilt image uploads (ADR-20261127120000): `gregale deploy
// --local-image` sends a single-image OCI archive through the resumable
// upload session. It skips the source-tree checks and becomes a Dockerfile
// deployment whose build is already done; builderd hands the archive to
// imaged without a builder VM.

import (
	"errors"
	"fmt"
	"net/http"
	"os"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidsource"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
	"github.com/onebox-faas/faas/pkg/ociarchive"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/statefuldenylist"
)

// uploadSessionSizeCap validates the upload's format and returns the plan
// cap that applies to it, refusing a total size above it.
func (s *server) uploadSessionSizeCap(app state.App, limits api.Limits, opts *api.UploadDeployOptions, total int64) (int64, *api.Problem) {
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
	if prob := imageArchiveAppProblem(app, *opts); prob != nil {
		return 0, prob
	}
	maxBytes := limits.ImageArchiveMaxBytes()
	if total > maxBytes {
		return 0, api.ErrImageArchiveTooLarge(limits, total)
	}
	return maxBytes, nil
}

// imageArchiveAppProblem refuses options and apps a prebuilt image cannot
// serve. It runs at session start and again at commit, because the app's
// policy can change while the bytes upload.
func imageArchiveAppProblem(app state.App, opts api.UploadDeployOptions) *api.Problem {
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

// uploadedSourceTree is what a committed source tarball declares.
type uploadedSourceTree struct {
	manifest    *gregalemanifest.Manifest
	manifestApp state.App
	build       sourceDockerfileBuild
	hasBuild    bool
}

// resolveUploadedSourceTree runs a committed source tarball's checks: shape,
// source root, secrets, stateful shape, function Dockerfile, gregale manifest
// and Dockerfile build. opts.SourceRoot is resolved in place.
func (s *server) resolveUploadedSourceTree(partPath string, acct state.Account, app state.App, opts *api.UploadDeployOptions, explicitSourceRoot bool) (uploadedSourceTree, *api.Problem) {
	if prob := validateTarballShape(partPath); prob != nil {
		return uploadedSourceTree{}, prob
	}
	// The source_root scan reads every entry body, three passes in all, so it
	// runs only after the shape check has refused archives whose declared sizes
	// exceed the budget. Before, a small compression bomb with source_root set
	// was inflated in full on each commit retry; a failed commit leaves the
	// session open.
	if opts.SourceRoot != "" {
		resolved, present, rootErr := resolveArchiveSourceRoot(partPath, opts.SourceRoot, explicitSourceRoot)
		if rootErr != nil {
			return uploadedSourceTree{}, api.NewProblem(http.StatusBadRequest, api.CodeSourceInvalid, "Bad source", rootErr.Error())
		}
		if !present {
			return uploadedSourceTree{}, sourceRootMissingProblem(opts.SourceRoot, explicitSourceRoot)
		}
		opts.SourceRoot = resolved
	}
	if prob := s.scanSourceTarballSecrets(partPath, api.MustLimitsFor(acct.Plan)); prob != nil {
		return uploadedSourceTree{}, prob
	}
	if prob := scanForStatefulShapeAtRoot(partPath, opts.Dockerfile, opts.SourceRoot); prob != nil {
		return uploadedSourceTree{}, prob
	}
	if app.Type == state.AppTypeFunction {
		hasDockerfile, err := archiveHasRootDockerfileAtRoot(partPath, opts.SourceRoot)
		if err != nil {
			return uploadedSourceTree{}, api.ErrSourceInvalid(err.Error())
		}
		if problem := api.FunctionDockerfileProblem(app.Runtime, opts.Dockerfile || hasDockerfile); problem != nil {
			return uploadedSourceTree{}, problem
		}
	}
	tree := uploadedSourceTree{manifestApp: app}
	tree.manifestApp.RootDir = opts.SourceRoot
	var prob *api.Problem
	if tree.manifest, prob = loadSourceRefManifest(partPath, tree.manifestApp, acct.Plan, s.agentStepsAllowed(acct)); prob != nil {
		return uploadedSourceTree{}, prob
	}
	if tree.build, tree.hasBuild, prob = resolveSourceDockerfileBuild(partPath, tree.manifestApp.RootDir, app, tree.manifest); prob != nil {
		return uploadedSourceTree{}, prob
	}
	return tree, nil
}

// uploadedImageParams is the Enqueue input for a committed image archive.
func uploadedImageParams(summary *ociarchive.Summary, name string) *apidsource.UploadedImage {
	if summary == nil {
		return nil
	}
	return &apidsource.UploadedImage{ManifestDigest: summary.ManifestDigest, Name: name, Port: summary.ExposedPort}
}

// inspectUploadedImage verifies the committed archive's shape, digests and
// platform. imaged verifies every layer's diff ID when it builds the layer.
func inspectUploadedImage(partPath string) (ociarchive.Summary, *api.Problem) {
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
