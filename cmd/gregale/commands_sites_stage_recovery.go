package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/hosting"
)

func resumeReviewedHostingBuild(ctx context.Context, client *api.Client, plan api.HostingDeploymentPlan, build reviewedHostingBuild, provenance *api.HostingReleaseProvenance, releaseID string) (api.HostingRelease, error) {
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
	current.APIOrigin = plan.APIOrigin // The CLI captured and checked the configured origin before API reads.
	if err = checkRetainedHostingPlan(plan, current); err != nil {
		return release, err
	}
	if !e.Capabilities.StagedReleasesSupported || provenance != nil && !e.Capabilities.HistoryEnabled {
		return release, errors.New("required staging/history API capabilities are disabled; no recovery mutations were sent")
	}
	release, err = retainedReviewedHostingRelease(e.Releases, e.Site, plan, releaseID, provenance)
	if err != nil {
		return release, err
	}
	if release.State == "ready" {
		// A lost finalization response must not cause more grants, uploads or
		// finalization. Refresh exact release evidence and site intent instead.
		latest, err := client.ListHostingReleases(ctx, plan.SiteID)
		if err != nil {
			return release, err
		}
		observed, err := retainedReviewedHostingRelease(latest, e.Site, plan, releaseID, provenance)
		if err != nil || observed.State != "ready" {
			return release, errors.New("verified release changed during recovery; inspect it before retrying")
		}
		if err = checkReviewedHostingSite(ctx, client, e.Site); err != nil {
			return observed, err
		}
		return observed, nil
	}
	if err = finishReviewedHostingStage(ctx, client, plan, e, build, release); err != nil {
		return release, fmt.Errorf("recovery of retained release %s is incomplete; inspect before retrying: %w", releaseID, err)
	}
	return finalizeReviewedHostingStage(ctx, client, plan, e.Site, releaseID, current.PayloadBytes)
}

func checkRetainedHostingPlan(saved, current api.HostingDeploymentPlan) error {
	// PlanDeployment projects a *new* release. Recovery reuses an exact admitted
	// release, so those three prospective quota blockers do not apply. Keep every
	// other blocker, all evidence validation, and the exact reviewed comparison.
	if current.State != "within_observed_limits" && current.State != "blocked" || current.State == "blocked" && len(current.Blockers) == 0 {
		return errors.New("current recovery evidence has an unknown or inconsistent state")
	}
	for _, blocker := range current.Blockers {
		switch blocker {
		case "storage_bytes", "pending_uploads", "releases_per_site":
		default:
			return fmt.Errorf("current staging recovery is blocked: %s", blocker)
		}
	}
	current.Blockers = []string{}
	current.State = "within_observed_limits"
	return hosting.CheckReviewedDeploymentPlan(saved, current)
}

func retainedReviewedHostingRelease(releases []api.HostingRelease, site api.HostingSite, plan api.HostingDeploymentPlan, releaseID string, provenance *api.HostingReleaseProvenance) (api.HostingRelease, error) {
	var selected api.HostingRelease
	seen := map[string]bool{}
	for _, r := range releases {
		if !hosting.ValidCIRunID(r.ID) || r.SiteID != site.ID || seen[r.ID] {
			return selected, errors.New("retained release inventory is inconsistent; inspect before retrying")
		}
		seen[r.ID] = true
		if r.ID == releaseID {
			selected = r
		}
	}
	if selected.ID != releaseID || selected.ID == site.ActiveReleaseID || selected.State != "pending" && selected.State != "ready" {
		return selected, errors.New("exact retained release is missing, active or no longer eligible; no replacement will be created")
	}
	if err := validateReviewedHostingRelease(selected, plan, selected.State, plan.PayloadBytes); err != nil {
		return selected, err
	}
	if provenance != nil && (selected.Provenance == nil || *selected.Provenance != *provenance) {
		return selected, errors.New("source labels differ from the retained release; recovery preserves immutable provenance")
	}
	return selected, nil
}
