package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/routerequirements"
)

func cmdRoutes(args []string) int {
	if len(args) > 0 {
		switch args[0] {
		case "monitor":
			return cmdRoutesMonitor(args[1:])
		case "health":
			return cmdRoutesHealth(args[1:])
		case "gate":
			return cmdRoutesGate(args[1:])
		case "requirements":
			return cmdRoutesRequirements(args[1:])
		case "check":
			return cmdRoutesCheck(args[1:])
		case "results":
			return cmdRoutesResults(args[1:])
		case "plan":
			return cmdRoutesPlan(args[1:])
		case "apply":
			return cmdRoutesApply(args[1:])
		case "impact":
			return cmdRoutesImpact(args[1:])
		case "contract":
			return cmdRoutesContract(args[1:])
		case "sunsets":
			return cmdRoutesSunsets(args[1:])
		case "lifecycle":
			return cmdRoutesLifecycle(args[1:])
		case "migration":
			return cmdRoutesMigration(args[1:])
		case "status":
			return cmdRoutesStatus(args[1:])
		case "advise":
			return cmdRoutesAdvise(args[1:])
		}
	}
	PrintUsage(osStderr, "usage: gregale routes <status|advise|requirements|gate|health|monitor|check|results|plan|apply|impact|contract|lifecycle|sunsets|migration> [slug] [flags]", "cli")
	return 1
}

func cmdRoutesPlan(args []string) int {
	flags, positional := splitArgsForFlags(args, "fail-on-unresolved", "consolidate-budgets", "saved")
	fs := newFlagSet("routes plan", flag.ContinueOnError)
	requirements := fs.String("requirements", "", "versioned route requirements YAML or JSON file")
	saved := fs.Bool("saved", false, "plan from the app's saved requirements and bind their revision")
	revision := fs.Int64("expected-revision", -1, "require this saved requirements revision")
	deployment := fs.String("deployment", "", "captured deployment UUID (required for version 2 groups)")
	output := fs.String("out", "", "save the JSON plan to a new file")
	burst := fs.Int("throttle-burst", 0, "burst for new throttles without an existing rate policy")
	consolidate := fs.Bool("consolidate-budgets", false, "combine compatible budgets within declared group prefixes, including uncaptured paths")
	failUnresolved := fs.Bool("fail-on-unresolved", false, "exit 1 when requirements remain unresolved after proposed changes")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(positional) != 1 || !validCLISlug(positional[0]) || *saved == (*requirements != "") || *burst < 0 {
		PrintUsage(osStderr, "usage: gregale routes plan <slug> (--requirements PATH | --saved) [--deployment ID] [--expected-revision N] [--consolidate-budgets] [--out PATH] [--throttle-burst N] [--fail-on-unresolved]", "cli")
		return 1
	}
	request := api.RoutePolicyPlanRequest{Saved: *saved, ThrottleBurst: *burst, DeploymentID: *deployment, ConsolidateBudgets: *consolidate}
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "expected-revision" {
			request.ExpectedRevision = revision
		}
	})
	if err := validateRoutePlanRevision(request); err != nil {
		return printErr("Invalid route requirements source", err)
	}
	if !*saved {
		config, _, err := readPreviewCoverageRequirements(*requirements)
		if err == nil {
			config, _, err = routerequirements.NormalizeCoverage(config)
		}
		if err != nil {
			return printErr("Invalid route requirements", err)
		}
		request.Requirements = config
	}
	if *saved || request.Requirements.Version == 2 {
		if id, err := uuid.Parse(*deployment); err != nil || id.String() != *deployment {
			return printErr("Invalid deployment", errors.New("saved or version 2 requirements need --deployment with a canonical captured deployment UUID"))
		}
	} else if *consolidate {
		return printErr("Invalid budget consolidation", errors.New("--consolidate-budgets requires version 2 route groups"))
	} else if *deployment != "" {
		return printErr("Invalid deployment", errors.New("--deployment is only supported with version 2 requirements"))
	}
	if *output != "" {
		if _, err := os.Lstat(*output); err == nil || !errors.Is(err, os.ErrNotExist) {
			return printErr("Invalid --out", errors.New("choose a new file path; existing files and symlinks are not replaced"))
		}
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	plan, err := client.PlanRoutePolicy(ctx, positional[0], request)
	if err != nil {
		return printErr("Could not plan route policy", err)
	}
	if request.Saved {
		if err := validateSavedRoutePlan(plan, positional[0], request); err != nil {
			return printErr("Invalid saved route plan", err)
		}
	}
	if *output != "" {
		body, err := json.MarshalIndent(plan, "", "  ")
		if err != nil {
			return printErr("Could not encode route plan", err)
		}
		if err := writeRoutePolicyPlan(*output, append(body, '\n')); err != nil {
			return printErr("Could not save route plan", err)
		}
	}
	if jsonOutput {
		if code := jsonOut(writeJSON(plan)); code != 0 {
			return code
		}
	} else {
		renderRoutePolicyPlan(osStdout, plan)
		if *output != "" {
			_, _ = fmt.Fprintf(osStdout, "\nSaved JSON plan: %s\n", previewReportText(*output))
		}
	}
	if *failUnresolved && len(plan.Unresolved) > 0 {
		return 1
	}
	return 0
}

func validateRoutePlanRevision(request api.RoutePolicyPlanRequest) error {
	if request.ExpectedRevision != nil && (!request.Saved || *request.ExpectedRevision < 1 || *request.ExpectedRevision > api.RouteRequirementsMaxRevision) {
		return errors.New("--expected-revision requires --saved and an existing revision within the supported range")
	}
	return nil
}

func validateSavedRoutePlan(plan api.RoutePolicyPlan, slug string, request api.RoutePolicyPlanRequest) error {
	if plan.Version != 3 || plan.Authority != "server" || plan.App != slug || plan.AppID == "" || plan.Requirements == nil || plan.DeploymentID != request.DeploymentID || plan.RequirementsRevision < 1 || plan.RequirementsRevision > api.RouteRequirementsMaxRevision || request.ExpectedRevision != nil && plan.RequirementsRevision != *request.ExpectedRevision {
		return errors.New("saved plan identity, captured deployment or revision does not match the request")
	}
	config, digest, err := routerequirements.NormalizeCoverage(*plan.Requirements)
	if err != nil || config.Version != 2 || digest != plan.RequirementsSHA256 {
		return errors.New("saved plan requirements fingerprint is unavailable")
	}
	return nil
}

func renderRoutePolicyPlan(w io.Writer, plan routerequirements.PolicyPlan) {
	_, _ = fmt.Fprintf(w, "Route policy plan for %s\nStatus: %s; changes: %d; unresolved: %d\n",
		previewReportText(plan.App), plan.Status, len(plan.Changes), len(plan.Unresolved))
	_, _ = fmt.Fprintf(w, "Requirements: %s -> %s (proposed configuration)\nPlan SHA-256: %s\n",
		plan.Before.Status, plan.After.Status, plan.SHA256)
	if plan.RequirementsRevision > 0 {
		_, _ = fmt.Fprintf(w, "Saved requirements revision: %d; SHA-256: %s\n", plan.RequirementsRevision, plan.RequirementsSHA256)
	}
	if plan.Before.Coverage != nil {
		_, _ = fmt.Fprintf(w, "Capture: %s; SHA-256: %s; routes: %d; inventory: %s\n", plan.DeploymentID, plan.Before.Coverage.SHA256, plan.Before.Coverage.RouteCount, plan.Before.Coverage.Status)
	}
	if plan.RuleUsage != nil {
		_, _ = fmt.Fprintf(w, "App rules: %d -> %d; quota: %d (includes disabled rules)\n", plan.RuleUsage.Before, plan.RuleUsage.After, plan.RuleUsage.Limit)
	}
	for _, group := range plan.After.Groups {
		_, _ = fmt.Fprintf(w, "Group %s: %s; captured routes: %d; exceptions: %d\n", previewReportText(group.Name), group.Status, group.MatchedRoutes, group.ExemptRoutes)
	}
	for _, change := range plan.Changes {
		_, _ = fmt.Fprintf(w, "\n%s %s for %s %s on %s\n  expected: %s\n  before: %s\n  after: %s\n",
			change.Operation, change.Kind, change.Impact.Method, previewReportText(change.Impact.Path),
			previewReportText(change.Impact.Host), previewReportText(change.Expected), previewReportText(change.Before), previewReportText(change.After))
		if change.RuleID != "" {
			_, _ = fmt.Fprintf(w, "  rule: %s\n", previewReportText(change.RuleID))
		}
		if change.BudgetGroup != "" {
			_, _ = fmt.Fprintf(w, "  consolidated budget group: %s\n", previewReportText(change.BudgetGroup))
		}
		if len(change.DisplacedRuleIDs) > 0 {
			_, _ = fmt.Fprintf(w, "  displaced for this request: %s\n", previewReportText(fmt.Sprint(change.DisplacedRuleIDs)))
		}
		for _, affected := range change.Impact.Captured {
			_, _ = fmt.Fprintf(w, "  captured impact: %s %s (%s; public exception: %t) %s -> %s\n", affected.Method, previewReportText(affected.Path), affected.Relation, affected.Public, previewReportText(affected.BeforeRuleID), previewReportText(affected.AfterRuleID))
		}
		if change.Impact.BeyondCapture {
			_, _ = fmt.Fprintln(w, "  wildcard scope also includes uncaptured and future paths")
		}
		var payload any = change.Create
		if change.Update != nil {
			payload = change.Update
		}
		body, _ := json.MarshalIndent(payload, "  ", "  ")
		_, _ = fmt.Fprintf(w, "  proposed API body:\n  %s\n  %s\n", body, change.Reason)
	}
	for _, unresolved := range plan.Unresolved {
		_, _ = fmt.Fprintf(w, "\nUnresolved %s %s / %s (%s): %s\n  %s\n", unresolved.Method,
			previewReportText(unresolved.Path), unresolved.Requirement, previewReportText(unresolved.Code), previewReportText(unresolved.Reason), previewReportText(unresolved.NextAction))
	}
	_, _ = fmt.Fprintf(w, "\n%s\n", plan.Scope)
}

// Publish a complete owner-readable artifact without overwriting a destination
// (including a symlink) that appeared while authenticated reads were running.
func writeRoutePolicyPlan(path string, body []byte) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".gregale-route-plan-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary plan: %w", err)
	}
	defer func() {
		_ = file.Close()
		_ = os.Remove(file.Name())
	}()
	if _, err := file.Write(body); err != nil {
		return fmt.Errorf("write plan: %w", err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync plan: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close plan: %w", err)
	}
	if err := os.Link(file.Name(), path); err != nil {
		return fmt.Errorf("publish plan to a new file: %w", err)
	}
	return nil
}
