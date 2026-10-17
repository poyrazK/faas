package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/hosting"
)

func hostingCLIEnabled() bool {
	value := strings.ToLower(os.Getenv("FAAS_FRONTEND_HOSTING_ENABLED"))
	return value == "1" || value == "true"
}

func cmdSites(args []string) int {
	if !hostingCLIEnabled() {
		return unknownCommand("sites")
	}
	if len(args) == 0 {
		PrintUsage(os.Stderr, "usage: gregale sites <create|list|status|serve|plan|capacity|cancel|runs|diagnose|ci-setup|ci-preflight|deploy|releases|diff|ci|tokens|cleanup|retention|history|previews|promote|rollback|suspend|resume|retire|delete> [flags]", "sites")
		return 1
	}
	if args[0] == "serve" {
		return cmdSitesServe(args[1:])
	}
	if args[0] == "plan" {
		return cmdSitesPlan(args[1:])
	}
	if args[0] == "cleanup" || args[0] == "retention" {
		return cmdSitesRetention(args[1:], args[0] == "cleanup")
	}
	if args[0] == "ci-setup" || args[0] == "ci-preflight" {
		return cmdSitesCISetup(args[1:], args[0] == "ci-preflight")
	}
	if args[0] == "cancel" {
		return cmdSitesCancel(args[1:])
	}
	if args[0] == "capacity" {
		return cmdSitesCapacity(args[1:])
	}
	if args[0] == "runs" {
		return cmdSitesRuns(args[1:])
	}
	if args[0] == "diagnose" {
		return cmdSitesDiagnose(args[1:])
	}
	if args[0] == "ci" {
		return cmdSitesCI(args[1:])
	}
	if args[0] == "tokens" {
		return cmdSitesTokens(args[1:])
	}
	if args[0] == "history" {
		return cmdSitesHistory(args[1:])
	}
	if args[0] == "previews" {
		return cmdSitesPreviews(args[1:])
	}
	if args[0] == "deploy" {
		return cmdSitesDeploy(args[1:])
	}
	if args[0] == "diff" || args[0] == "promote" {
		return cmdSitesReview(args[0], args[1:])
	}
	return cmdSitesManage(args[0], args[1:])
}

func cmdSitesManage(verb string, args []string) int {
	flags := newFlagSet("sites-"+verb, flag.ContinueOnError)
	site := flags.String("site", "", "site UUID")
	name := flags.String("name", "", "site name")
	project := flags.String("project", "", "project slug")
	environment := flags.String("environment", "production", "registered environment")
	release := flags.String("release", "", "release UUID")
	if err := flags.Parse(args); err != nil || rejectUnexpectedFlagArgs(flags) {
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()
	var result any
	switch verb {
	case "create":
		result, err = client.CreateHostingSite(ctx, api.CreateHostingSiteRequest{Name: *name, Project: *project, Environment: *environment})
	case "list":
		result, err = client.ListHostingSites(ctx)
	case "status":
		result, err = client.GetHostingSiteStatus(ctx, *site)
	case "releases":
		result, err = client.ListHostingReleases(ctx, *site)
	case "retire":
		result, err = client.RetireHostingRelease(ctx, *site, *release)
	case "delete":
		result, err = client.DeleteHostingSite(ctx, *site)
	case "rollback":
		var current api.HostingSite
		current, err = client.GetHostingSite(ctx, *site)
		if err == nil {
			result, err = client.PromoteHostingRelease(ctx, *site, api.PromoteHostingReleaseRequest{ReleaseID: *release, ExpectedRevision: current.Revision})
		}
	case "suspend", "resume":
		var current api.HostingSite
		current, err = client.GetHostingSite(ctx, *site)
		if err == nil {
			result, err = client.SetHostingSiteSuspended(ctx, *site, api.SetHostingSuspendedRequest{Suspended: verb == "suspend", ExpectedRevision: current.Revision})
		}
	default:
		return printErr("Unknown sites command", errors.New(verb))
	}
	if err != nil {
		return printErr("Hosting request failed", err)
	}
	return jsonOut(writeJSON(result))
}

func cmdSitesDeploy(args []string) int {
	flags := newFlagSet("sites-deploy", flag.ContinueOnError)
	siteID := flags.String("site", "", "site UUID (required)")
	dir := flags.String("path", "dist", "already-built static directory")
	spa := flags.Bool("spa", false, "serve index.html for extensionless HTML navigations")
	repository := flags.String("source-repository", "", "caller-provided repository label (unverified)")
	commit := flags.String("source-commit", "", "caller-provided full lowercase Git commit SHA (unverified)")
	branch := flags.String("source-branch", "", "caller-provided source branch (unverified)")
	description := flags.String("description", "", "caller-provided deployment description")
	noPromote := flags.Bool("no-promote", false, "verify and stage the release without making it routable")
	plan := flags.String("plan", "", "reviewed saved plan JSON; requires --no-promote and independent planned-staging opt-in")
	resumeRelease := flags.String("resume-release", "", "exact retained release UUID; requires --plan, --no-promote and stage-recovery opt-in")
	wait := flags.Bool("wait", false, "wait for durable publication after promotion")
	timeout := flags.Duration("wait-timeout", api.HostingPublicationWaitDefault, "bounded publication wait")
	if err := flags.Parse(args); err != nil || rejectUnexpectedFlagArgs(flags) {
		return 1
	}
	if *noPromote && *wait {
		return printErr("Invalid deployment options", errors.New("--no-promote cannot be combined with --wait"))
	}
	if err := validateHostingWait(*wait, *timeout); err != nil {
		return printErr("Invalid wait options", err)
	}
	var provenance *api.HostingReleaseProvenance
	if *repository != "" || *commit != "" || *branch != "" || *description != "" {
		provenance = &api.HostingReleaseProvenance{Repository: *repository, CommitSHA: *commit, Branch: *branch, Description: *description}
		if !hostingHistoryWorkstationEnabled() {
			return printErr("Release provenance is disabled", errors.New("set FAAS_FRONTEND_HOSTING_HISTORY_ENABLED=1 before sending provenance"))
		}
		if err := hosting.ValidateProvenance(provenance); err != nil {
			return printErr("Invalid release provenance", err)
		}
	}
	planSupplied := false
	resumeSupplied := false
	flags.Visit(func(f *flag.Flag) {
		planSupplied = planSupplied || f.Name == "plan"
		resumeSupplied = resumeSupplied || f.Name == "resume-release"
	})
	if resumeSupplied && (!planSupplied || !hosting.ValidCIRunID(*resumeRelease)) {
		return printErr("Invalid staging recovery options", errors.New("--resume-release requires an exact canonical release UUID and --plan FILE"))
	}
	if planSupplied {
		return cmdSitesPlannedStage(*plan, *siteID, *dir, *spa, *noPromote, provenance, *resumeRelease)
	}
	manifest, err := staticHostingManifest(*dir, *spa)
	if err != nil {
		return printErr("Invalid static directory", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	site, err := client.GetHostingSite(ctx, *siteID)
	if err != nil {
		return printErr("Hosting site unavailable", err)
	}
	if *wait && !site.DurableRouting {
		return printErr("Publication waiting unavailable", errors.New("leased sites have no durable publication receipt; no release was created"))
	}
	if *noPromote || provenance != nil {
		caps, err := client.GetHostingCapabilities(ctx)
		if err != nil {
			return printErr("Hosting capabilities unavailable", err)
		}
		if *noPromote && !caps.StagedReleasesSupported {
			return printErr("Staging unavailable", errors.New("upgrade all API writers and publishers before using staged deployment"))
		}
		if provenance != nil && !caps.HistoryEnabled {
			return printErr("Release provenance unavailable", errors.New("enable the API history feature before creating a release"))
		}
	}
	var release api.HostingRelease
	if provenance != nil {
		release, err = client.CreateHostingReleaseWithProvenance(ctx, site.ID, manifest, provenance)
	} else {
		release, err = client.CreateHostingRelease(ctx, site.ID, manifest)
	}
	if err != nil {
		return printErr("Create release failed", err)
	}
	grants, err := client.HostingReleaseUploads(ctx, site.ID, release.ID)
	if err != nil {
		return printErr("Upload grants unavailable", err)
	}
	if err := uploadHostingFiles(ctx, *dir, manifest, grants.Uploads); err != nil {
		return printErr("Upload failed; retire release "+release.ID+" before retrying", err)
	}
	verified, err := client.FinalizeHostingRelease(ctx, site.ID, release.ID)
	if err != nil {
		return printErr("Release verification failed; inspect retained release "+release.ID, err)
	}
	release = verified
	if *noPromote {
		return jsonOut(writeJSON(struct {
			State            string             `json:"state"`
			Release          api.HostingRelease `json:"release"`
			ExpectedRevision int64              `json:"expected_revision"`
		}{State: "staged", Release: release, ExpectedRevision: site.Revision}))
	}
	site, err = client.PromoteHostingRelease(ctx, site.ID, api.PromoteHostingReleaseRequest{ReleaseID: release.ID, ExpectedRevision: site.Revision})
	if err != nil {
		return printErr("Promotion failed; the verified release is retained as "+release.ID, err)
	}
	return finishHostingPromotion(ctx, client, site, *wait, *timeout)
}

func staticHostingManifest(dir string, spa bool) (api.HostingManifest, error) {
	manifest := api.HostingManifest{SPAFallback: spa, Files: []api.HostingFile{}}
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() {
		return manifest, errors.New("path must be a real directory")
	}
	err = filepath.WalkDir(dir, func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			return errors.New("static sites cannot contain symbolic links or special files")
		}
		rel, err := filepath.Rel(dir, name)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == hosting.ReleaseConfigFilename {
			if os.Getenv("FAAS_FRONTEND_HOSTING_CONFIG_ENABLED") != "1" && os.Getenv("FAAS_FRONTEND_HOSTING_CONFIG_ENABLED") != "true" {
				return errors.New("release configuration requires FAAS_FRONTEND_HOSTING_CONFIG_ENABLED=1")
			}
			configFile, err := openCustomerFile(name)
			if err != nil {
				return err
			}
			data, readErr := io.ReadAll(io.LimitReader(configFile, api.HostingConfigMaxBytes+1))
			closeErr := configFile.Close()
			if readErr != nil || closeErr != nil {
				return errors.New("cannot read hosting configuration")
			}
			manifest.Config, err = hosting.ParseReleaseConfig(data)
			return err
		}
		if !hosting.ValidPath(rel) {
			return errors.New("static directory contains a noncanonical URL path")
		}
		file, err := hostingFileManifest(name, rel)
		if err != nil {
			return err
		}
		manifest.Files = append(manifest.Files, file)
		if len(manifest.Files) > api.HostingFilesMax {
			return errors.New("too many static files")
		}
		return nil
	})
	if err != nil {
		return manifest, err
	}
	return manifest, hosting.ValidateManifest(manifest)
}

func hostingFileManifest(name, rel string) (api.HostingFile, error) {
	f, err := openCustomerFile(name)
	if err != nil {
		return api.HostingFile{}, err
	}
	defer func() { _ = f.Close() }()
	hash := sha256.New()
	size, err := io.Copy(hash, io.LimitReader(f, api.HostingFileMaxBytes+1))
	if err != nil || size > api.HostingFileMaxBytes {
		return api.HostingFile{}, errors.New("static file exceeds byte limit or cannot be read")
	}
	contentType := mime.TypeByExtension(filepath.Ext(rel))
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	return api.HostingFile{Path: rel, SHA256: hex.EncodeToString(hash.Sum(nil)), Size: size, ContentType: contentType}, nil
}

func uploadHostingFiles(ctx context.Context, dir string, manifest api.HostingManifest, grants []api.HostingUpload) error {
	files := make(map[string]api.HostingFile, len(manifest.Files))
	for _, file := range manifest.Files {
		files[file.Path] = file
	}
	if len(grants) != len(files) {
		return errors.New("incomplete upload grants")
	}
	client := &http.Client{Timeout: api.HostingStorageTimeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	for _, grant := range grants {
		file, ok := files[grant.Path]
		if !ok || !hosting.ValidPath(grant.Path) {
			return errors.New("unexpected upload path")
		}
		if err := uploadHostingFile(ctx, client, filepath.Join(dir, filepath.FromSlash(file.Path)), file, grant); err != nil {
			return err
		}
		delete(files, grant.Path)
	}
	return nil
}

func uploadHostingFile(ctx context.Context, client *http.Client, path string, file api.HostingFile, grant api.HostingUpload) error {
	u, err := url.Parse(grant.URL)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
		return errors.New("invalid upload grant")
	}
	f, err := openCustomerFile(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, grant.URL, io.LimitReader(f, file.Size))
	if err != nil {
		return errors.New("invalid upload request")
	}
	req.ContentLength = file.Size
	if file.Size == 0 {
		req.Body = http.NoBody
	}
	for key, values := range grant.Headers {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}
	req.Header.Set("Content-Type", file.ContentType)
	// A fresh client carries neither the Gregale bearer nor platform cookies.
	response, err := client.Do(req)
	if err != nil {
		return errors.New("storage upload unavailable")
	} // never print a signed URL
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("storage upload rejected with HTTP %d", response.StatusCode)
	}
	return nil
}
