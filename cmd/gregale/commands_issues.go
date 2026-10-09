package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const issuesCmdUsage = "usage: gregale issues <list|get|assign|resolve|reopen|ignore|impact-alert|ownership-rules|tokens|create-token|revoke-token> --app <slug> [<issue-id>] [--sort recent|impact] [--min-customers N] [flags]"

func cmdIssues(args []string) int {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		PrintUsage(os.Stderr, issuesCmdUsage, "issues")
		if len(args) > 0 {
			return 0
		}
		return 1
	}
	if args[0] == "get" || args[0] == "resolve" || args[0] == "ignore" || args[0] == "reopen" {
		for _, arg := range args[1:] {
			if arg == "--interactive" || strings.HasPrefix(arg, "--interactive=") {
				if args[0] == "reopen" {
					return cmdIssuesReopenInteractive(args[1:])
				}
				if args[0] == "ignore" {
					return cmdIssuesIgnoreInteractive(args[1:])
				}
				if args[0] == "resolve" {
					return cmdIssuesResolveInteractive(args[1:])
				}
				return cmdIssuesGetInteractive(args[1:])
			}
		}
	}
	action := args[0]
	fs := newFlagSet("issues "+action, flag.ContinueOnError)
	app := fs.String("app", "", "application slug")
	stateFilter := fs.String("state", "", "open, resolved, or ignored")
	environment := fs.String("environment", "", "environment filter or token environment")
	cursor := fs.String("cursor", "", "issue-list or occurrence pagination cursor")
	releaseCursor := fs.String("release-cursor", "", "release history cursor")
	activityCursor := fs.String("activity-cursor", "", "activity history cursor")
	since := fs.String("since", "", "impact window start (RFC3339)")
	deployment := fs.String("deployment", "", "fixed or token-bound deployment UUID")
	assignee := fs.String("assignee", "", "list filter: me, unassigned, or account UUID; assignment owner UUID (empty unassigns)")
	sortBy := fs.String("sort", "", "list order: recent (default) or impact by verified customers in 24h")
	minCustomers := fs.Int64("min-customers", 0, "list filter: minimum verified customers affected in 24h")
	until := fs.String("until", "", "ignore until (RFC3339)")
	name := fs.String("name", "issues", "ingest token name")
	expires := fs.Duration("expires-in", 24*time.Hour, "ingest token lifetime (max 90 days)")
	rulesFile := fs.String("rules-file", "", "JSON policy file for ownership-rules; omit to read the current policy")
	flags, positionals := normalizeDebugFlagArgs(args[1:], map[string]bool{"app": true, "state": true, "environment": true, "cursor": true, "release-cursor": true, "activity-cursor": true, "since": true, "deployment": true, "assignee": true, "sort": true, "min-customers": true, "until": true, "name": true, "expires-in": true, "rules-file": true})
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if *app == "" {
		PrintUsage(os.Stderr, issuesCmdUsage, "issues")
		return 1
	}
	impactThresholdSpecified := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "min-customers" {
			impactThresholdSpecified = true
		}
	})
	c, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()
	switch action {
	case "list":
		if len(positionals) != 0 {
			return issueCLIUsage()
		}
		if *sortBy != "" && *sortBy != "recent" && *sortBy != "impact" {
			return printErr("Invalid issue sort", fmt.Errorf("sort must be recent or impact"))
		}
		if *minCustomers < 0 {
			return printErr("Invalid customer threshold", fmt.Errorf("min-customers must be non-negative"))
		}
		out, err := c.ListIssuesWithOptions(ctx, *app, api.IssueListOptions{
			State: *stateFilter, Environment: *environment, Assignee: *assignee,
			Sort: *sortBy, MinCustomers: *minCustomers, Cursor: *cursor,
		})
		if err != nil {
			return printErr("Could not list issues", err)
		}
		if jsonOutput {
			return jsonOut(writeJSON(out))
		}
		w := tabwriter.NewWriter(osStdout, 0, 4, 2, ' ', 0)
		if _, err := fmt.Fprintln(w, "ID\tSTATE\tOWNER\tVERIFIED CUSTOMERS (24H)\tEVENTS (24H)\tUNATTRIBUTED (24H)\tEVENTS\tRECURRENCES\tLAST SEEN\tTITLE"); err != nil {
			return printErr("Could not write issues", err)
		}
		for _, i := range out.Items {
			owner := i.AssigneeAccountID
			if owner == "" {
				owner = "unassigned"
			}
			identified, observed, unattributed := "—", "—", "—"
			if i.Impact24h != nil {
				identified = fmt.Sprint(i.Impact24h.IdentifiedCustomers)
				observed = fmt.Sprint(i.Impact24h.ObservedEvents)
				unattributed = fmt.Sprint(i.Impact24h.UnattributedEvents)
			}
			if _, err := fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%d\t%d\t%s\t%s\n", i.ID, i.State, owner, identified, observed, unattributed, i.EventCount, i.RegressionCount, i.LastSeenAt.Format(time.RFC3339), i.Title); err != nil {
				return printErr("Could not write issues", err)
			}
		}
		if err := w.Flush(); err != nil {
			return printErr("Could not write issues", err)
		}
		if out.NextCursor != "" {
			if _, err := fmt.Fprintf(osStdout, "Next cursor: %s\n", out.NextCursor); err != nil {
				return printErr("Could not write issue cursor", err)
			}
		}
		return 0
	case "get":
		if len(positionals) != 1 {
			return issueCLIUsage()
		}
		out, err := c.GetIssuePage(ctx, *app, positionals[0], *since, *cursor, *releaseCursor, *activityCursor)
		if err != nil {
			return printErr("Could not read issue", err)
		}
		return jsonOut(writeJSON(out))
	case "impact-alert":
		if len(positionals) != 0 {
			return issueCLIUsage()
		}
		var policy api.IssueImpactAlertPolicy
		if impactThresholdSpecified {
			if *minCustomers < 0 || *minCustomers > api.IssueImpactAlertMaxCustomers {
				return printErr("Invalid customer threshold", fmt.Errorf("min-customers must be between 0 and %d", api.IssueImpactAlertMaxCustomers))
			}
			policy, err = c.SetIssueImpactAlertPolicy(ctx, *app, api.UpdateIssueImpactAlertPolicyRequest{MinimumCustomers: *minCustomers})
		} else {
			policy, err = c.GetIssueImpactAlertPolicy(ctx, *app)
		}
		if err != nil {
			return printErr("Could not read impact alert policy", err)
		}
		if jsonOutput {
			return jsonOut(writeJSON(policy))
		}
		if !policy.Enabled {
			PrintOK(osStdout, "Customer-impact alerts are disabled.")
			return 0
		}
		PrintOK(osStdout, "Customer-impact alerts fire at %d verified customers within 24 hours.", policy.MinimumCustomers)
		return 0
	case "ownership-rules":
		if len(positionals) != 0 {
			return issueCLIUsage()
		}
		var policy api.IssueOwnershipRules
		if *rulesFile == "" {
			policy, err = c.GetIssueOwnershipRules(ctx, *app)
		} else {
			var raw []byte
			if *rulesFile == "-" {
				raw, err = io.ReadAll(os.Stdin)
			} else {
				raw, err = os.ReadFile(*rulesFile)
			}
			if err != nil {
				return printErr("Could not read ownership-rules file", err)
			}
			if err = json.Unmarshal(raw, &policy); err != nil {
				return printErr("Invalid ownership-rules JSON", err)
			}
			policy, err = c.SetIssueOwnershipRules(ctx, *app, policy)
		}
		if err != nil {
			return printErr("Could not read ownership rules", err)
		}
		if jsonOutput {
			return jsonOut(writeJSON(policy))
		}
		if len(policy.Rules) == 0 {
			PrintOK(osStdout, "Automatic issue assignment is disabled.")
			return 0
		}
		for i, rule := range policy.Rules {
			matchers := make([]string, 0, 3)
			if rule.ExceptionType != "" {
				matchers = append(matchers, "exception="+rule.ExceptionType)
			}
			if rule.SourceKind != "" {
				matchers = append(matchers, "source="+rule.SourceKind)
			}
			if rule.RoutePrefix != "" {
				matchers = append(matchers, "route="+rule.RoutePrefix)
			}
			if _, err := fmt.Fprintf(osStdout, "%d. %s -> %s\n", i+1, strings.Join(matchers, " & "), rule.AssigneeAccountID); err != nil {
				return printErr("Could not write ownership rules", err)
			}
		}
		return 0
	case "assign", "resolve", "reopen", "ignore":
		if len(positionals) != 1 {
			return issueCLIUsage()
		}
		// production-us hunt #4: `issues resolve <id>` reached the server and
		// came back "resolution requires fixed_deployment_id", a field name
		// the CLI never mentions.
		if action == "resolve" && strings.TrimSpace(*deployment) == "" {
			return printErr("Invalid issue resolution", errors.New("--deployment <uuid> is required: name the deployment that fixed the issue"))
		}
		in := api.IssueActionRequest{Action: action, AssigneeAccountID: *assignee, FixedDeploymentID: *deployment}
		if *until != "" {
			v, err := time.Parse(time.RFC3339, *until)
			if err != nil {
				return printErr("Invalid ignore time", err)
			}
			in.IgnoredUntil = &v
		}
		out, err := c.ActOnIssue(ctx, *app, positionals[0], in)
		if err != nil {
			return printErr("Could not update issue", err)
		}
		if jsonOutput {
			return jsonOut(writeJSON(out))
		}
		PrintOK(osStdout, "Issue %s is %s.", out.ID, out.State)
		return 0
	case "create-token":
		if len(positionals) != 0 || *deployment == "" || *expires <= 0 || *expires > api.IssueMaxTokenLifetime {
			return issueCLIUsage()
		}
		out, err := c.CreateIssueIngestToken(ctx, *app, api.CreateIssueIngestTokenRequest{Name: *name, DeploymentID: *deployment, Environment: *environment, ExpiresAt: time.Now().UTC().Add(*expires)})
		if err != nil {
			return printErr("Could not create ingest token", err)
		}
		return jsonOut(writeJSON(out))
	case "tokens":
		if len(positionals) != 0 {
			return issueCLIUsage()
		}
		out, err := c.ListIssueIngestTokens(ctx, *app)
		if err != nil {
			return printErr("Could not list ingest tokens", err)
		}
		return jsonOut(writeJSON(out))
	case "revoke-token":
		if len(positionals) != 1 {
			return issueCLIUsage()
		}
		if err := c.RevokeIssueIngestToken(ctx, *app, positionals[0]); err != nil {
			return printErr("Could not revoke ingest token", err)
		}
		PrintOK(osStdout, "Ingest token revoked.")
		return 0
	default:
		return issueCLIUsage()
	}
}
func issueCLIUsage() int { PrintUsage(os.Stderr, issuesCmdUsage, "issues"); return 1 }
