package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"os/signal"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/hosting"
)

func cmdSitesPlan(args []string) int {
	enabled := os.Getenv("FAAS_FRONTEND_HOSTING_DEPLOYMENT_PLAN_ENABLED")
	if enabled != "1" && enabled != "true" {
		return printErr("Hosting deployment planning is disabled", errors.New("set FAAS_FRONTEND_HOSTING_DEPLOYMENT_PLAN_ENABLED=1; the existing operator API opt-in is separate"))
	}
	f := newFlagSet("sites-plan", flag.ContinueOnError)
	site := f.String("site", "", "owned site UUID (required)")
	dir := f.String("path", "dist", "already-built static directory")
	spa := f.Bool("spa", false, "SPA navigation fallback, matching deployment settings")
	if err := f.Parse(args); err != nil || rejectUnexpectedFlagArgs(f) {
		return 1
	}
	if !hosting.ValidCIRunID(*site) {
		return printErr("Invalid deployment plan request", errors.New("provide a canonical site UUID"))
	}
	manifest, err := staticHostingManifest(*dir, *spa)
	if err != nil {
		return printErr("Invalid prebuilt output; no API requests were sent", err)
	}
	data, err := json.Marshal(manifest)
	if err != nil || int64(len(data)) > api.HostingManifestMaxBytes {
		return printErr("Invalid prebuilt output; no API requests were sent", errors.New("serialized manifest exceeds the byte limit"))
	}
	origin, err := hosting.HostingAPIOrigin(apiBase())
	if err != nil {
		return printErr("Invalid deployment plan target", err)
	}
	client, err := hostingDeploymentPlanClient(origin)
	if err != nil {
		return printErr("Operator connection unavailable", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, api.HostingDeploymentPlanTimeout)
	defer cancel()
	started := time.Now().UTC()
	evidence, err := readHostingPlanEvidence(ctx, client, *site)
	if err != nil {
		return printErr("Deployment plan unavailable; no deployment was sent", err)
	}
	plan, err := hosting.PlanDeployment(evidence, manifest, started, time.Now().UTC())
	if err != nil {
		return printErr("Deployment plan unavailable; no deployment was sent", err)
	}
	plan.APIOrigin = origin
	if code := jsonOut(writeJSON(plan)); code != 0 {
		return code
	}
	if len(plan.Blockers) > 0 {
		return 2
	}
	return 0
}

func hostingDeploymentPlanClient(origin string) (*api.Client, error) {
	// Capture the checked origin once. A profile change during local validation
	// must not move this deployment to a different API.
	token := loadToken()
	if token == "" {
		return nil, errors.New("use an existing operator credential for deployment planning and staging")
	}
	return api.NewHostingAutomationClient(origin, token), nil
}

func readHostingPlanEvidence(ctx context.Context, client *api.Client, site string) (hosting.DeploymentPlanEvidence, error) {
	var e hosting.DeploymentPlanEvidence
	var err error
	if e.Capabilities, err = client.GetHostingCapabilities(ctx); err != nil {
		return e, err
	}
	if e.Capabilities.AutomationSiteID != "" {
		return e, errors.New("planning requires an operator credential with account usage access; site CI credentials are insufficient")
	}
	if e.Site, err = client.GetHostingSite(ctx, site); err != nil {
		return e, err
	}
	if e.Site.ID != site {
		return e, errors.New("site response does not match the requested site")
	}
	if e.Releases, err = client.ListHostingReleases(ctx, site); err != nil {
		return e, err
	}
	if e.Releases == nil {
		return e, errors.New("release inventory is missing; an explicit empty array is required for an empty site")
	}
	if e.Usage, err = client.GetHostingUsage(ctx); err != nil {
		return e, err
	}
	latest, err := client.GetHostingSite(ctx, site)
	if err != nil {
		return e, err
	}
	if latest != e.Site {
		return e, errors.New("site changed while planning; refresh and review again")
	}
	return e, nil
}
