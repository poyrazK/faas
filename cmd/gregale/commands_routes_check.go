package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/routerequirements"
)

func cmdRoutesCheck(args []string) int {
	flags, positional := splitArgsForFlags(args, "fail-on-requirements")
	fs := newFlagSet("routes check", flag.ContinueOnError)
	deployment := fs.String("deployment", "", "app-owned deployment UUID whose captured inventory is checked")
	revision := fs.Int64("expected-revision", -1, "fail if saved requirements differ from this revision")
	format := fs.String("format", "human", "output format: human or markdown")
	output := fs.String("out", "", "save the JSON check to a new file")
	fail := fs.Bool("fail-on-requirements", false, "exit 1 for violated or incomplete captured coverage")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	request := api.CheckRouteRequirementsRequest{DeploymentID: *deployment}
	fs.Visit(func(flag *flag.Flag) {
		if flag.Name == "expected-revision" {
			request.ExpectedRevision = revision
		}
	})
	id, idErr := uuid.Parse(*deployment)
	if len(positional) != 1 || !validCLISlug(positional[0]) || idErr != nil || id.String() != *deployment || request.ExpectedRevision != nil && (*revision < 1 || *revision > api.RouteRequirementsMaxRevision) || (*format != "human" && *format != "markdown") || jsonOutput && *format != "human" {
		return printErr("Invalid route check", errors.New("usage: gregale routes check <slug> --deployment ID [--expected-revision N] [--format human|markdown] [--out PATH] [--fail-on-requirements] [--json]"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	result, err := client.CheckRouteRequirements(ctx, positional[0], request)
	if err != nil {
		return printErr("Could not check saved route requirements", err)
	}
	if err := validateRouteCheckResult(result, positional[0], request); err != nil {
		return printErr("Invalid route check response", err)
	}
	if *output != "" {
		body, err := json.MarshalIndent(result, "", "  ")
		if err == nil {
			err = writeRoutePolicyPlan(*output, append(body, '\n'))
		}
		if err != nil {
			return printErr("Could not save route check", err)
		}
	}
	if jsonOutput {
		if code := jsonOut(writeJSON(result)); code != 0 {
			return code
		}
	} else {
		_, _ = fmt.Fprintf(osStdout, "Route check for %s\nRequirements revision: %d; SHA-256: %s\nDeployment: %s\nConfiguration SHA-256: %s\n", positional[0], result.RequirementsRevision, result.RequirementsSHA256, result.DeploymentID, result.ConfigurationSHA256)
		report := routerequirements.WrapReport(result.Report)
		renderPreviewRequirements(osStdout, &report, *format == "markdown")
	}
	if *fail && result.Report.Status != "satisfied" {
		return 1
	}
	return 0
}

func validateRouteCheckResult(result api.RouteRequirementsCheck, slug string, request api.CheckRouteRequirementsRequest) error {
	if result.Version != 1 || result.App != slug || result.AppID == "" || result.DeploymentID != request.DeploymentID || result.RequirementsRevision < 1 || result.RequirementsRevision > api.RouteRequirementsMaxRevision || request.ExpectedRevision != nil && result.RequirementsRevision != *request.ExpectedRevision || len(result.RequirementsSHA256) != 64 || len(result.ConfigurationSHA256) != 64 || result.Report.Version != 2 || result.Report.SHA256 != result.RequirementsSHA256 || result.Report.Coverage == nil {
		return errors.New("check identity, revision or captured provenance does not match the request")
	}
	coverage := result.Report.Coverage
	if coverage.Status == "available" {
		if coverage.Deployment != request.DeploymentID || len(coverage.SHA256) != 64 || coverage.RouteCount < 1 {
			return errors.New("complete captured inventory provenance is unavailable")
		}
	} else if coverage.Status != "unavailable" || result.Report.Status != "unknown" {
		return errors.New("incomplete captured inventory cannot pass a route check")
	}
	if result.Report.Status != "satisfied" && result.Report.Status != "violated" && result.Report.Status != "unknown" {
		return errors.New("unrecognized route check status")
	}
	return nil
}
