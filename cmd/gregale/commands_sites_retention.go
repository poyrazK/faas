package main

import (
	"context"
	"errors"
	"flag"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/hosting"
)

func cmdSitesRetention(args []string, cleanup bool) int {
	if value := strings.ToLower(os.Getenv("FAAS_FRONTEND_HOSTING_RETENTION_ENABLED")); value != "true" && value != "1" {
		return printErr("Hosting retention is disabled", errors.New("set FAAS_FRONTEND_HOSTING_RETENTION_ENABLED=1; API opt-in is separate"))
	}
	verb := "show"
	if !cleanup {
		if len(args) == 0 {
			return printErr("Retention action required", errors.New("use retention <show|set>"))
		}
		verb = args[0]
		args = args[1:]
	}
	f := newFlagSet("sites-retention", flag.ContinueOnError)
	site := f.String("site", "", "site UUID")
	pending := f.Duration("pending-age", api.HostingRetentionAgeDefault, "idle age before abandoning uploads")
	staged := f.Duration("staged-age", api.HostingRetentionAgeDefault, "idle age before retiring expired CI releases")
	enabled := f.Bool("enabled", false, "explicit per-site automatic retention opt-in")
	revision := f.Int64("expected-revision", -1, "retention policy revision actually reviewed")
	if err := f.Parse(args); err != nil || rejectUnexpectedFlagArgs(f) {
		return 1
	}
	supplied := map[string]bool{}
	f.Visit(func(v *flag.Flag) { supplied[v.Name] = true })
	rules := api.HostingRetentionRules{PendingAgeSeconds: int64(*pending / time.Second), StagedAgeSeconds: int64(*staged / time.Second)}
	if uuid.Validate(*site) != nil || verb != "show" && verb != "set" || !hosting.ValidRetentionRules(rules) || *pending%time.Second != 0 || *staged%time.Second != 0 || verb == "set" && (!supplied["enabled"] || !supplied["pending-age"] || !supplied["staged-age"] || *revision < 0) || cleanup && (supplied["enabled"] || supplied["expected-revision"]) || verb == "show" && !cleanup && (supplied["enabled"] || supplied["expected-revision"] || supplied["pending-age"] || supplied["staged-age"]) {
		return printErr("Invalid retention options", errors.New("use a site UUID, bounded whole-second ages, and explicit --enabled, both age flags and reviewed --expected-revision for set"))
	}
	if cleanup && supplied["pending-age"] != supplied["staged-age"] {
		return printErr("Both proposed ages are required", errors.New("provide --pending-age and --staged-age together"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()
	caps, err := client.GetHostingCapabilities(ctx)
	if err != nil {
		return printErr("Capabilities unavailable", err)
	}
	if !caps.RetentionEnabled {
		return printErr("API retention unavailable", errors.New("no retention policy was changed"))
	}
	if verb == "set" {
		p, err := client.SetHostingRetentionPolicy(ctx, *site, api.SetHostingRetentionRequest{HostingRetentionRules: rules, Enabled: *enabled, ExpectedRevision: *revision})
		if err != nil {
			return printErr("Retention update not confirmed; refresh before retrying", err)
		}
		return jsonOut(writeJSON(p))
	}
	var override *api.HostingRetentionRules
	if supplied["pending-age"] || supplied["staged-age"] {
		override = &rules
	}
	plan, err := client.GetHostingCleanupPlan(ctx, *site, override)
	if err != nil {
		return printErr("Cleanup plan unavailable", err)
	}
	if !cleanup {
		return jsonOut(writeJSON(plan.Policy))
	}
	return jsonOut(writeJSON(plan))
}
