package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/hosting"
)

type reviewedHostingBuild struct {
	manifest api.HostingManifest
	files    map[string][]byte
}

func cmdSitesPlannedStage(filename, siteID, dir string, spa, noPromote bool, provenance *api.HostingReleaseProvenance, resumeRelease string) int {
	enabled := os.Getenv("FAAS_FRONTEND_HOSTING_PLANNED_STAGING_ENABLED")
	if enabled != "1" && enabled != "true" {
		return printErr("Staging from a saved plan is disabled", errors.New("set FAAS_FRONTEND_HOSTING_PLANNED_STAGING_ENABLED=1"))
	}
	if resumeRelease != "" {
		recovery := os.Getenv("FAAS_FRONTEND_HOSTING_STAGE_RECOVERY_ENABLED")
		if recovery != "1" && recovery != "true" {
			return printErr("Staging recovery is disabled", errors.New("set FAAS_FRONTEND_HOSTING_STAGE_RECOVERY_ENABLED=1"))
		}
	}
	if filename == "" || !noPromote || !hosting.ValidCIRunID(siteID) {
		return printErr("Invalid planned staging options", errors.New("provide --plan FILE, a canonical --site UUID and --no-promote"))
	}
	raw, err := readHostingSetupFile(filename, api.HostingDeploymentPlanMaxBytes)
	if err != nil {
		return printErr("Cannot read a bounded regular saved plan file", err)
	}
	plan, err := hosting.ParseDeploymentPlan([]byte(raw))
	if err != nil {
		return printErr("Invalid saved deployment plan", err)
	}
	origin, err := hosting.HostingAPIOrigin(apiBase())
	if err != nil || plan.APIOrigin != origin || plan.SiteID != siteID {
		return printErr("Saved deployment target differs", errors.New("use the reviewed API origin and site; generate a new plan for another target"))
	}
	build, err := snapshotReviewedHostingBuild(dir, spa)
	if err != nil {
		return printErr("Cannot snapshot reviewed output; no API requests were sent", err)
	}
	if hosting.HostingManifestFingerprint(build.manifest) != plan.ManifestFingerprint {
		return printErr("Build differs from the saved plan; no API requests were sent", errors.New("keep reviewed files and SPA/config settings unchanged or generate and review a new plan"))
	}
	client, err := hostingDeploymentPlanClient(origin)
	if err != nil {
		return printErr("Operator connection unavailable", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, api.HostingPlannedStageTimeout)
	defer cancel()
	var release api.HostingRelease
	if resumeRelease != "" {
		release, err = resumeReviewedHostingBuild(ctx, client, plan, build, provenance, resumeRelease)
	} else {
		release, err = stageReviewedHostingBuild(ctx, client, plan, build, provenance)
	}
	if err != nil {
		return printErr("Planned staging incomplete; inspect retained releases before retrying", err)
	}
	return jsonOut(writeJSON(struct {
		State            string             `json:"state"`
		Release          api.HostingRelease `json:"release"`
		ExpectedRevision int64              `json:"expected_revision"`
		Resumed          bool               `json:"resumed,omitempty"`
	}{State: "staged", Release: release, ExpectedRevision: plan.ExpectedRevision, Resumed: resumeRelease != ""}))
}

func snapshotReviewedHostingBuild(dir string, spa bool) (reviewedHostingBuild, error) {
	var out reviewedHostingBuild
	root, err := os.OpenRoot(dir)
	if err != nil {
		return out, err
	}
	defer func() { _ = root.Close() }()
	out.manifest, err = staticHostingManifest(dir, spa)
	if err != nil {
		return out, err
	}
	out.manifest.Config, err = hostingLocalConfig(root)
	if err != nil {
		return out, err
	}
	raw, err := json.Marshal(out.manifest)
	if err != nil || int64(len(raw)) > api.HostingManifestMaxBytes || hosting.ValidateManifest(out.manifest) != nil {
		return out, errors.New("invalid or oversized reviewed build manifest")
	}
	out.files = make(map[string][]byte, len(out.manifest.Files))
	for _, f := range out.manifest.Files {
		body, err := hostingLocalRead(root, filepath.FromSlash(f.Path), f.Size)
		if err != nil {
			return out, err
		}
		digest := sha256.Sum256(body)
		if int64(len(body)) != f.Size || hex.EncodeToString(digest[:]) != f.SHA256 {
			return out, errors.New("build changed while snapshotting; keep reviewed output unchanged")
		}
		out.files[f.Path] = body
	}
	return out, nil
}

func stageReviewedHostingBuild(ctx context.Context, client *api.Client, plan api.HostingDeploymentPlan, build reviewedHostingBuild, provenance *api.HostingReleaseProvenance) (api.HostingRelease, error) {
	var release api.HostingRelease
	readCtx, cancel := context.WithTimeout(ctx, api.HostingDeploymentPlanTimeout)
	started := time.Now().UTC()
	e, err := readHostingPlanEvidence(readCtx, client, plan.SiteID)
	cancel()
	if err != nil {
		return release, err
	}
	current, err := hosting.PlanDeployment(e, build.manifest, started, time.Now().UTC())
	if err != nil {
		return release, err
	}
	current.APIOrigin = plan.APIOrigin // The CLI checked the configured API before constructing this client.
	if err = hosting.CheckReviewedDeploymentPlan(plan, current); err != nil {
		return release, err
	}
	if !e.Capabilities.StagedReleasesSupported || provenance != nil && !e.Capabilities.HistoryEnabled {
		return release, errors.New("required staging/history API capabilities are disabled; no release was created")
	}
	release, err = client.CreateHostingReleaseWithProvenance(ctx, plan.SiteID, build.manifest, provenance)
	if err != nil {
		return release, fmt.Errorf("release creation was not confirmed; inspect site releases before another attempt: %w", err)
	}
	if err = validateReviewedHostingRelease(release, plan, "pending", current.PayloadBytes); err != nil {
		return release, err
	}
	for _, previous := range e.Releases {
		if previous.ID == release.ID {
			return release, errors.New("creation response reused an existing release; no uploads were sent")
		}
	}
	if err = finishReviewedHostingStage(ctx, client, plan, e, build, release); err != nil {
		return release, fmt.Errorf("release %s is retained for inspection: %w", release.ID, err)
	}
	return finalizeReviewedHostingStage(ctx, client, plan, e.Site, release.ID, current.PayloadBytes)
}

func finalizeReviewedHostingStage(ctx context.Context, client *api.Client, plan api.HostingDeploymentPlan, site api.HostingSite, releaseID string, payload int64) (api.HostingRelease, error) {
	release, err := client.FinalizeHostingRelease(ctx, plan.SiteID, releaseID)
	if err == nil {
		err = validateReviewedHostingRelease(release, plan, "ready", payload)
	}
	if err == nil && release.ID != releaseID {
		err = errors.New("verification response selected another release")
	}
	if err == nil {
		err = checkReviewedHostingSite(ctx, client, site)
	}
	if err != nil {
		return release, fmt.Errorf("verification or final revision check failed for retained release %s; inspect before retrying: %w", releaseID, err)
	}
	return release, nil
}

func validateReviewedHostingRelease(r api.HostingRelease, plan api.HostingDeploymentPlan, state string, payload int64) error {
	if !hosting.ValidCIRunID(r.ID) || r.SiteID != plan.SiteID || r.State != state || r.FirstPromotedAt != nil || r.ArtifactBytes != payload || hosting.ValidateManifest(r.Manifest) != nil || hosting.HostingManifestFingerprint(r.Manifest) != plan.ManifestFingerprint {
		return errors.New("release response differs from reviewed content; inspect site releases before retrying")
	}
	return nil
}

func checkReviewedHostingSite(ctx context.Context, client *api.Client, expected api.HostingSite) error {
	current, err := client.GetHostingSite(ctx, expected.ID)
	if err != nil {
		return err
	}
	if current != expected {
		return errors.New("production intent changed during staging; review the retained release against the current site before promotion")
	}
	return nil
}

func finishReviewedHostingStage(ctx context.Context, client *api.Client, plan api.HostingDeploymentPlan, e hosting.DeploymentPlanEvidence, build reviewedHostingBuild, release api.HostingRelease) error {
	if err := checkReviewedHostingSite(ctx, client, e.Site); err != nil {
		return err
	}
	grants, err := client.HostingReleaseUploads(ctx, plan.SiteID, release.ID)
	if err != nil {
		return err
	}
	if err = validateReviewedHostingRelease(grants.Release, plan, "pending", release.ArtifactBytes); err != nil {
		return err
	}
	if err = validateHostingUploadGrants(grants, plan.SiteID, release.ID, build.manifest, e.Capabilities.UploadBaseURL); err != nil {
		return err
	}
	uploader := &http.Client{Timeout: api.HostingStorageTimeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	contentTypes := make(map[string]string, len(build.manifest.Files))
	for _, file := range build.manifest.Files {
		contentTypes[file.Path] = file.ContentType
	}
	for _, grant := range grants.Uploads {
		if err = uploadReviewedHostingBody(ctx, uploader, build.files[grant.Path], contentTypes[grant.Path], grant); err != nil {
			return err
		}
	}
	return nil
}

func uploadReviewedHostingBody(ctx context.Context, client *http.Client, body []byte, contentType string, grant api.HostingUpload) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, grant.URL, bytes.NewReader(body))
	if err != nil {
		return errors.New("invalid reviewed upload request")
	}
	for key, values := range grant.Headers {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}
	req.Header.Set("Content-Type", contentType)
	response, err := client.Do(req)
	if err != nil {
		return errors.New("reviewed upload unavailable")
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("reviewed upload rejected with HTTP %d", response.StatusCode)
	}
	return nil
}
