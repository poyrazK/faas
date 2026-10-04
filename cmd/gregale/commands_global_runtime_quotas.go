package main

// adr: 486
import (
	"context"
	"flag"
	"fmt"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/globaldirectory"
)

func cmdGlobalRuntimeQuota(verb string, args []string) int {
	if len(args) == 0 || !globaldirectory.ValidSlug(args[0]) {
		return printErr("Invalid global app", fmt.Errorf("a canonical global app slug is required"))
	}
	fs := newFlagSet("global-app "+verb, flag.ContinueOnError)
	id := fs.String("allocation", "", "stable allocation UUID, reused across retries (required)")
	var shares stringListFlag
	var rollout string
	if verb == "reserve-quota" {
		fs.Var(&shares, "share", "region=concurrency; repeat for every selected region")
		fs.StringVar(&rollout, "rollout-region", "", "selected region receiving rollout overlap (default: first sorted region)")
	}
	if err := fs.Parse(args[1:]); err != nil {
		return 1
	}
	if fs.NArg() != 0 || !globaldirectory.ValidID(*id) {
		return printErr("Invalid allocation", fmt.Errorf("--allocation must be a canonical nonzero UUID"))
	}
	req, err := globalQuotaShares(shares, rollout, verb == "reserve-quota")
	if err != nil {
		return printErr("Invalid quota shares", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()
	var out api.GlobalRuntimeQuotaResponse
	switch verb {
	case "reserve-quota":
		out, err = client.PutGlobalRuntimeQuota(ctx, args[0], *id, req)
	case "show-quota":
		out, err = client.GetGlobalRuntimeQuota(ctx, args[0], *id)
	case "retire-quota":
		out, err = client.RetireGlobalRuntimeQuota(ctx, args[0], *id)
	default:
		return 1
	}
	if err != nil {
		return printErr("Could not access global quota reservation", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	_, _ = fmt.Fprintf(osStdout, "%s\tgeneration=%d\t%s\tplan=%s\n", out.AllocationID, out.Generation, out.Status, out.Policy.Plan)
	for _, target := range out.Targets {
		_, _ = fmt.Fprintf(osStdout, "  %s\tconcurrency=%d\trollout=%d\tissued=%t\treleased=%t\n", target.Region, target.Concurrency, target.RolloutConcurrency, target.IssuedAt != nil, target.ReleasedAt != nil)
	}
	return 0
}

func globalQuotaShares(shares []string, rollout string, required bool) (api.PutGlobalRuntimeQuotaRequest, error) {
	req := api.PutGlobalRuntimeQuotaRequest{RolloutRegion: rollout}
	if required && (len(shares) == 0 || len(shares) > api.GlobalRuntimeQuotaTargetsMax) {
		return req, fmt.Errorf("provide one --share region=concurrency for every selected region")
	}
	seen := make(map[string]bool, len(shares))
	for _, raw := range shares {
		region, value, ok := strings.Cut(raw, "=")
		n, err := strconv.Atoi(value)
		if !ok || region == "" || !api.ValidComputeRegion(region) || err != nil || n < 1 || seen[region] {
			return req, fmt.Errorf("shares require unique canonical regions and positive integer concurrency")
		}
		seen[region] = true
		req.Shares = append(req.Shares, api.GlobalRuntimeQuotaShare{Region: region, Concurrency: n})
	}
	if rollout != "" && !seen[rollout] {
		return req, fmt.Errorf("--rollout-region must be a selected share")
	}
	return req, nil
}
