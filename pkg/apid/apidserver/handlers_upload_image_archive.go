package apidserver

// Upload session wiring for prebuilt image uploads (ADR-20261127120000); the
// rules live in pkg/apid/imageupload.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/imageupload"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
	"github.com/onebox-faas/faas/pkg/ociarchive"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
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

// srvImageBases lets the grace sweep delete purged apps' image bases
// through the server's store, opened on first use.
type srvImageBases struct {
	srv *server
	ctx context.Context
}

func (b srvImageBases) Delete(ctx context.Context, appID string) error {
	store, err := b.srv.imageBaseStore(b.ctx)
	if err != nil {
		return err
	}
	return store.Delete(ctx, appID)
}

// imageBaseStore opens the store of apps' image bases once
// (ADR-20261127120000 amendment 1).
func (s *server) imageBaseStore(ctx context.Context) (*imageupload.BaseStore, error) {
	s.imageBasesOnce.Do(func() {
		s.imageBases, s.imageBasesErr = imageupload.OpenBaseStore(context.WithoutCancel(ctx), spoolRoot())
	})
	return s.imageBases, s.imageBasesErr
}

// handleGetAppImageBase is GET /v1/apps/{slug}/image-base: the layers of the
// app's latest uploaded image, which a redeploy may leave out of its upload.
// It answers whoever may deploy the app, and the same 404 to anyone else.
func (s *server) handleGetAppImageBase(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, err := s.store.AppBySlug(r.Context(), r.PathValue("slug"))
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			s.notFound(w, "no such app")
			return
		}
		writeCustomerInternalProblem(w, r, s.log, "load app for image base",
			"Gregale could not read this app.", "Retry; contact support if it continues.", err)
		return
	}
	owner, prob := s.authorizeUploadApp(r, acct, app)
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	if !s.platformFlagEnabled(platformflags.LocalImageDeploys, owner) {
		api.WriteProblem(w, api.ErrImageArchiveUnavailable())
		return
	}
	base, err := s.readImageBase(r.Context(), app.ID)
	if errors.Is(err, imageupload.ErrNoBase) {
		api.WriteProblem(w, api.ErrNoImageBase(app.Slug))
		return
	}
	if err != nil {
		writeCustomerInternalProblem(w, r, s.log, "read image base",
			"Gregale could not read the app's previous image.", "Deploy again; the CLI uploads the full image.", err)
		return
	}
	writeJSON(w, http.StatusOK, base)
}

func (s *server) readImageBase(ctx context.Context, appID string) (api.ImageBase, error) {
	store, err := s.imageBaseStore(ctx)
	if err != nil {
		return api.ImageBase{}, err
	}
	return store.Summary(ctx, appID)
}

// rebuildImageUpload turns a thin image upload into the full archive from
// the app's image base, in place, and returns its size.
func (s *server) rebuildImageUpload(ctx context.Context, app state.App, partPath string, limits api.Limits) (int64, *api.Problem) {
	store, err := s.imageBaseStore(ctx)
	if err != nil {
		s.log.Warn("image base store unavailable", "app", app.ID, "err", err)
		return 0, api.ErrImageBaseChanged()
	}
	return store.RebuildProblem(ctx, app.ID, partPath, limits.ImageArchiveMaxBytes())
}

// saveImageBase keeps the accepted archive as the app's image base, in the
// background and best effort: on failure the next upload sends every layer.
func (s *server) saveImageBase(ctx context.Context, app state.App, partPath string, summary ociarchive.Summary) {
	store, err := s.imageBaseStore(ctx)
	if err != nil {
		s.log.Warn("image base store unavailable", "app", app.ID, "err", err)
		return
	}
	store.SaveAsync(ctx, app.ID, partPath, summary, func(err error) {
		s.log.Warn("store image base failed", "app", app.ID, "err", err)
	})
}

// uploadCommitWorkBytes is how many bytes a commit processes: a thin image
// upload is rebuilt into an archive up to the largest plan's image cap.
func uploadCommitWorkBytes(row sqlc.UploadSession) int64 {
	var opts struct {
		Reuse bool `json:"reuse_image_base"`
	}
	if json.Unmarshal(row.DeployOptions, &opts) == nil && opts.Reuse {
		return max(row.TotalSize, api.MustLimitsFor(api.PlanScale).ImageArchiveMaxBytes())
	}
	return row.TotalSize
}
