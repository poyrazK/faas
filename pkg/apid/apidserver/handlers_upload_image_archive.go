package apidserver

// Upload session wiring for prebuilt image uploads (ADR-20261127120000); the
// rules live in pkg/apid/imageupload.

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/imageupload"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/state"
)

// uploadSessionSizeCap refuses an image upload the release flag does not
// cover, then applies imageupload.SizeCap.
func (s *server) uploadSessionSizeCap(acct state.Account, app state.App, limits api.Limits, opts *api.UploadDeployOptions, total int64) (int64, *api.Problem) {
	if prob := s.imageUploadReleaseProblem(acct, opts); prob != nil {
		return 0, prob
	}
	return imageupload.SizeCap(app, limits, opts, total)
}

// imageUploadReleaseProblem refuses an image archive while the
// local_image_deploys flag is off for the account. It is checked at start
// and at commit, so turning the flag off also stops open uploads.
func (s *server) imageUploadReleaseProblem(acct state.Account, opts *api.UploadDeployOptions) *api.Problem {
	if opts != nil && opts.IsImageArchive() && !s.platformFlagEnabled(platformflags.LocalImageDeploys, acct) {
		return api.ErrImageArchiveUnavailable()
	}
	return nil
}

// uploadSpoolBudgetBytes is imageupload.SpoolBudgetBytes with this server's
// source multiplier.
func uploadSpoolBudgetBytes(limits api.Limits, imageArchive bool) int64 {
	return imageupload.SpoolBudgetBytes(limits, imageArchive, uploadSessionSpoolBudgetMultiplier)
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
