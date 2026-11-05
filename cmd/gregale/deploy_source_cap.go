package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// sourceUploadCap is the per-plan source upload cap a deploy packs against
// (issue #1182). verified is false when the account's plan could not be read
// and the cap is the smallest plan's, a local fallback only: apid enforces
// the account's real cap on upload either way.
type sourceUploadCap struct {
	mb       int
	verified bool
	lookup   error
}

// sourceCapLookupBudgets bounds the account lookup. The first attempt keeps
// a flaky API from stalling every deploy; one retry with a longer budget
// covers the transient timeouts a Scale account hit on 2026-11-05, which
// packed against the 100 MB floor and called it the Free/Hobby cap (#6631).
var sourceCapLookupBudgets = []time.Duration{5 * time.Second, 10 * time.Second}

func resolveSourceUploadCap(ctx context.Context, client *Client) sourceUploadCap {
	var lookupErr error
	for _, budget := range sourceCapLookupBudgets {
		lookupCtx, cancel := context.WithTimeout(ctx, budget)
		acct, err := client.Whoami(lookupCtx)
		cancel()
		if err == nil {
			if limits, ok := api.LimitsFor(api.Plan(acct.Plan)); ok {
				return sourceUploadCap{mb: limits.SourceTarballMaxMB, verified: true}
			}
			lookupErr = fmt.Errorf("unknown plan %q", acct.Plan)
			break
		}
		if readRefused(err) {
			// A deploy credential may not read the account's plan. apid
			// refuses an upload above the account's cap, so the pack is
			// bounded only by the largest plan's (ADR-20261031162858
			// amendment 1); the Free floor refused Pro and Scale sources.
			return sourceUploadCap{mb: largestSourceTarballMB(), verified: true}
		}
		lookupErr = err
		if ctx.Err() != nil || !isTransportError(err) {
			break
		}
	}
	return sourceUploadCap{mb: defaultZeroConfigSourceCapMB, lookup: lookupErr}
}

// warning is the notice for a fallback cap; empty for a verified one.
func (c sourceUploadCap) warning() string {
	if c.verified {
		return ""
	}
	return fmt.Sprintf("could not read the account's plan (%v); packing against the smallest plan's %d MB source limit. This limit is local only and says nothing about your plan: the server enforces your plan's limit on upload", c.lookup, c.mb)
}

// explain adds the fallback to a pack that went over an unverified cap, so
// the customer retries instead of trimming a source their plan accepts.
func (c sourceUploadCap) explain(err error) error {
	var over *sourceUploadLimitErr
	if c.verified || !errors.As(err, &over) {
		return err
	}
	return fmt.Errorf("packed source is over %d MB, the smallest plan's limit, used because the account's plan could not be read (%v). Run the command again once the account lookup succeeds; your plan's own limit may be higher", c.mb, c.lookup)
}
