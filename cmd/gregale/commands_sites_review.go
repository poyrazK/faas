package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/hosting"
)

func validateHostingWait(wait bool, timeout time.Duration) error {
	if timeout <= 0 || timeout > api.HostingPublicationWaitMax {
		return fmt.Errorf("wait-timeout must be positive and at most %s", api.HostingPublicationWaitMax)
	}
	if !wait && timeout != api.HostingPublicationWaitDefault {
		return errors.New("wait-timeout requires --wait")
	}
	return nil
}

func cmdSitesReview(verb string, args []string) int {
	flags := newFlagSet("sites-"+verb, flag.ContinueOnError)
	siteID := flags.String("site", "", "site UUID (required)")
	releaseID := flags.String("release", "", "verified release UUID (required)")
	expected := flags.Int64("expected-revision", 0, "site revision reviewed before promotion (required for promote)")
	wait := flags.Bool("wait", false, "wait for the exact accepted durable publication receipt")
	timeout := flags.Duration("wait-timeout", api.HostingPublicationWaitDefault, "bounded publication wait")
	if err := flags.Parse(args); err != nil || rejectUnexpectedFlagArgs(flags) {
		return 1
	}
	if uuid.Validate(*siteID) != nil || uuid.Validate(*releaseID) != nil {
		return printErr("Invalid hosting resource", errors.New("site and release UUIDs are required"))
	}
	if err := validateHostingWait(*wait, *timeout); err != nil {
		return printErr("Invalid wait options", err)
	}
	if verb == "diff" && (*expected != 0 || *wait) || verb == "promote" && *expected < 1 {
		return printErr("Invalid review options", errors.New("diff is read-only; promote requires a positive --expected-revision from the reviewed site"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if verb == "diff" {
		site, err := client.GetHostingSite(ctx, *siteID)
		if err != nil {
			return printErr("Hosting site unavailable", err)
		}
		releases, err := client.ListHostingReleases(ctx, site.ID)
		if err != nil {
			return printErr("Hosting releases unavailable", err)
		}
		review, err := hosting.ReviewRelease(site, releases, *releaseID)
		if err != nil {
			return printErr("Release comparison unavailable", err)
		}
		return jsonOut(writeJSON(review))
	}
	if *wait {
		site, err := client.GetHostingSite(ctx, *siteID)
		if err != nil {
			return printErr("Hosting site unavailable", err)
		}
		if !site.DurableRouting {
			return printErr("Publication waiting unavailable", errors.New("leased sites have no durable publication receipt; no promotion was sent"))
		}
	}
	accepted, err := client.PromoteHostingRelease(ctx, *siteID, api.PromoteHostingReleaseRequest{ReleaseID: *releaseID, ExpectedRevision: *expected})
	if err != nil {
		return printErr("Promotion not confirmed; refresh before retrying", err)
	}
	return finishHostingPromotion(ctx, client, accepted, *wait, *timeout)
}

func finishHostingPromotion(ctx context.Context, client *api.Client, site api.HostingSite, wait bool, timeout time.Duration) int {
	if !wait {
		return jsonOut(writeJSON(site))
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	observation, err := hosting.WaitPublication(ctx, client.GetHostingSiteStatus, site)
	if code := jsonOut(writeJSON(observation)); code != 0 {
		return code
	}
	if err != nil {
		return printErr("Promotion accepted for release "+site.ActiveReleaseID+"; publication not confirmed", err)
	}
	return 0
}
