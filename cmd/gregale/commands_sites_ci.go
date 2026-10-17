package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/hosting"
)

func cmdSitesCI(args []string) int {
	if !hostingAutomationWorkstationEnabled() {
		return printErr("Site automation is disabled", errors.New("set FAAS_FRONTEND_HOSTING_AUTOMATION_ENABLED=1"))
	}
	flags := newFlagSet("sites-ci", flag.ContinueOnError)
	trust := flags.String("oidc-trust", "", "explicit GitHub OIDC trust binding; requires FAAS_FRONTEND_HOSTING_OIDC_ENABLED=1")
	site := flags.String("site", "", "one site UUID")
	runID := flags.String("run-id", "", "stable UUID for this CI intent, reused on every retry")
	dir := flags.String("path", "dist", "prebuilt output directory")
	reserve := flags.Bool("reserve-preview", false, "reserve capacity before upload; requires the independent preview reservation opt-in")
	spa := flags.Bool("spa", false, "SPA navigation fallback")
	ttl := flags.Duration("preview-ttl", api.HostingPreviewTTLDefault, "preview TTL measured from first accepted run")
	timeout := flags.Duration("wait-timeout", api.HostingCIWaitDefault, "wait for provider publication receipts")
	repository := flags.String("source-repository", "", "unverified repository label")
	commit := flags.String("source-commit", "", "unverified full lowercase commit SHA")
	branch := flags.String("source-branch", "", "unverified source branch")
	description := flags.String("description", "", "unverified deployment description")
	if err := flags.Parse(args); err != nil || rejectUnexpectedFlagArgs(flags) {
		return 1
	}
	if uuid.Validate(*site) != nil || !hosting.ValidCIRunID(*runID) || *ttl < api.HostingPreviewTTLMin || *ttl > api.HostingPreviewTTLMax || *ttl%time.Second != 0 || *timeout <= 0 || *timeout > api.HostingCIWaitMax {
		return printErr("Invalid CI intent", errors.New("site and stable run UUIDs, bounded preview TTL and wait timeout are required"))
	}
	if *reserve && !hostingPreviewReservationsWorkstationEnabled() {
		return printErr("Preview reservations are disabled", errors.New("set FAAS_FRONTEND_HOSTING_PREVIEW_RESERVATIONS_ENABLED=1"))
	}
	token := ""
	if *trust == "" {
		token = loadToken()
	}
	base := apiBase()
	u, err := url.Parse(base)
	loopback := u != nil && (u.Hostname() == "localhost" || net.ParseIP(u.Hostname()).IsLoopback())
	if err != nil || u == nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" && u.Path != "/" || u.Scheme != "https" && (u.Scheme != "http" || !loopback) || *trust == "" && !api.ValidHostingAutomationToken(token) {
		return printErr("Invalid CI connection", errors.New("use a site-scoped automation credential and HTTPS API origin (loopback HTTP is allowed for local tests)"))
	}
	manifest, err := staticHostingManifest(*dir, *spa)
	if err != nil {
		return printErr("Invalid prebuilt directory", err)
	}
	req := api.HostingCIRunRequest{Manifest: manifest, PreviewTTLSeconds: int64(*ttl / time.Second), ReservePreview: *reserve}
	if *repository != "" || *commit != "" || *branch != "" || *description != "" {
		req.Provenance = &api.HostingReleaseProvenance{Repository: *repository, CommitSHA: *commit, Branch: *branch, Description: *description}
		if !hostingHistoryWorkstationEnabled() || hosting.ValidateProvenance(req.Provenance) != nil {
			return printErr("Invalid or disabled release provenance", errors.New("enable history and use bounded source metadata"))
		}
	}
	client := api.NewHostingAutomationClient(strings.TrimSuffix(base, "/"), token)
	if *trust != "" {
		enabled := os.Getenv("FAAS_FRONTEND_HOSTING_OIDC_ENABLED")
		if (enabled != "1" && enabled != "true") || os.Getenv("FAAS_TOKEN") != "" {
			return printErr("OIDC is disabled or credentials are mixed", errors.New("explicitly enable hosting OIDC and omit FAAS_TOKEN"))
		}
		client, err = api.NewGitHubHostingOIDCClient(base, *site, *trust, os.Getenv("ACTIONS_ID_TOKEN_REQUEST_URL"), os.Getenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN"))
		if err != nil {
			return printErr("Invalid GitHub job identity", err)
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, api.HostingCIWorkflowTimeout)
	defer cancel()
	result, err := runHostingCI(ctx, client, *site, *runID, *dir, req, *timeout)
	_ = writeJSON(result)
	if err != nil {
		return printErr("CI workflow incomplete; retry the same run ID and unchanged files", err)
	}
	return 0
}
func runHostingCI(ctx context.Context, client *api.Client, site, runID, dir string, req api.HostingCIRunRequest, timeout time.Duration) (api.HostingCIResult, error) {
	result := api.HostingCIResult{Run: api.HostingCIRun{ID: runID, SiteID: site}, State: "incomplete"}
	caps, err := client.GetHostingCapabilities(ctx)
	if err != nil {
		return result, err
	}
	ops := map[string]bool{}
	for _, op := range caps.AutomationOperations {
		ops[op] = true
	}
	if caps.AutomationSiteID != site || !ops["stage"] || !ops["preview"] || !caps.AutomationEnabled || !caps.StagedReleasesSupported || !caps.PreviewsEnabled || req.Provenance != nil && !caps.HistoryEnabled {
		return result, errors.New("required API capabilities are disabled; no CI run was created")
	}
	owned, err := client.GetHostingSite(ctx, site)
	if err != nil {
		return result, err
	}
	if !owned.DurableRouting || owned.Status != "active" || owned.Suspended {
		return result, errors.New("CI previews require an active, unsuspended durable site")
	}
	run, err := client.CreateHostingCIRun(ctx, site, runID, req)
	if err != nil {
		return result, err
	}
	fingerprint, _ := hosting.CIFingerprint(req)
	if run.ID != runID || run.SiteID != site || uuid.Validate(run.ReleaseID) != nil || uuid.Validate(run.PreviewID) != nil || run.Fingerprint != fingerprint || run.PreviewReserved != req.ReservePreview {
		return result, errors.New("CI identity response does not match the requested intent")
	}
	result.Run = run
	if run.Canceled {
		result.State = "canceled"
		return result, errors.New("this CI intent was permanently canceled; it cannot resume or allocate replacements")
	}
	if !run.PreviewExpiresAt.After(time.Now()) {
		return result, errors.New("the original CI retry window expired; inspect the retained run before starting a new intent")
	}
	releases, err := client.ListHostingReleases(ctx, site)
	if err != nil {
		return result, err
	}
	var release *api.HostingRelease
	for i := range releases {
		if releases[i].ID == run.ReleaseID {
			release = &releases[i]
			break
		}
	}
	if release == nil || release.SiteID != site || release.State != "pending" && release.State != "ready" {
		return result, errors.New("the retained run release is no longer usable; inspect the run instead of allocating a replacement")
	}
	if release.State == "pending" {
		grants, err := client.HostingReleaseUploads(ctx, site, run.ReleaseID)
		if err != nil {
			return result, err
		}
		if err = validateCIUploadGrants(grants, run, req.Manifest, caps.UploadBaseURL); err != nil {
			return result, err
		}
		if err = uploadHostingFiles(ctx, dir, req.Manifest, grants.Uploads); err != nil {
			return result, err
		}
		if _, err = client.FinalizeHostingRelease(ctx, site, run.ReleaseID); err != nil {
			return result, err
		}
	}
	preview, err := client.CreateHostingCIPreview(ctx, site, runID)
	if err != nil {
		return result, err
	}
	result.Run.PreviewCreated = true
	result.State = "publication_pending"
	wait, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	for {
		if preview.ID != run.PreviewID || preview.ReleaseID != run.ReleaseID || preview.SiteID != site || preview.Hostname != "p-"+run.PreviewID+strings.TrimPrefix(owned.Hostname, site) || !preview.ExpiresAt.Equal(run.PreviewExpiresAt) || preview.Revoked || !preview.ExpiresAt.After(time.Now()) {
			return result, errors.New("the original preview is expired, revoked or mismatched; it will not be replaced")
		}
		status, err := client.GetHostingSiteStatus(wait, site)
		if err != nil {
			return result, err
		}
		if status.Site.ID != site || !status.Site.DurableRouting || status.Site.Status != "active" || status.Site.Suspended {
			return result, errors.New("site traffic was disabled while publication was pending")
		}
		if preview.PublishedGeneration == preview.Generation && preview.Generation > 0 && preview.PublishedAt != nil && status.Publication.State == "published" && status.Publication.Generation > 0 && status.Publication.PublishedGeneration == status.Publication.Generation && status.Publication.PublishedAt != nil {
			result.PreviewURL = "https://" + preview.Hostname
			result.Published = true
			result.State = "publication_acknowledged"
			return result, nil
		}
		timer := time.NewTimer(api.HostingPublicationPollInterval)
		select {
		case <-wait.Done():
			timer.Stop()
			return result, errors.New("publication wait ended; accepted CI intent is retained")
		case <-timer.C:
		}
		previews, err := client.ListHostingPreviews(wait, site)
		if err != nil {
			return result, err
		}
		found := false
		for _, p := range previews {
			if p.ID == run.PreviewID {
				preview = p
				found = true
				break
			}
		}
		if !found {
			return result, errors.New("original preview no longer exists; it will not be recreated")
		}
	}
}
func validateCIUploadGrants(grants api.HostingUploadResponse, run api.HostingCIRun, manifest api.HostingManifest, base string) error {
	return validateHostingUploadGrants(grants, run.SiteID, run.ReleaseID, manifest, base)
}

func validateHostingUploadGrants(grants api.HostingUploadResponse, siteID, releaseID string, manifest api.HostingManifest, base string) error {
	destination, err := url.Parse(base)
	if err != nil || destination.Scheme != "https" || destination.Host == "" || destination.User != nil || destination.RawQuery != "" || destination.Fragment != "" {
		return errors.New("invalid configured upload origin")
	}
	if grants.Release.ID != releaseID || grants.Release.SiteID != siteID || grants.Release.State != "pending" || len(grants.Uploads) != len(manifest.Files) {
		return errors.New("upload grants do not match the hosting release")
	}
	files := map[string]api.HostingFile{}
	for _, f := range manifest.Files {
		files[f.Path] = f
	}
	for _, grant := range grants.Uploads {
		f, ok := files[grant.Path]
		if !ok {
			return errors.New("unexpected or duplicate upload grant")
		}
		delete(files, grant.Path)
		u, err := url.Parse(grant.URL)
		expected := strings.TrimSuffix(destination.Path, "/") + "/hosting/staging/" + siteID + "/" + releaseID + "/" + grant.Path
		if err != nil || u.Scheme != "https" || u.Host != destination.Host || u.User != nil || u.Fragment != "" || u.Path != expected {
			return errors.New("upload grant is outside the hosting release")
		}
		for key, values := range grant.Headers {
			if len(values) != 1 {
				return errors.New("unsupported upload headers")
			}
			value := values[0]
			switch strings.ToLower(key) {
			case "host":
				if value != u.Host {
					return errors.New("invalid upload host")
				}
			case "content-type":
				if value != f.ContentType {
					return errors.New("invalid upload content type")
				}
			case "content-length":
				if value != fmt.Sprint(f.Size) {
					return errors.New("invalid upload size")
				}
			default:
				return errors.New("unsupported upload headers")
			}
		}
	}
	return nil
}
