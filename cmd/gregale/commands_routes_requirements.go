package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/routerequirements"
)

func cmdRoutesRequirements(args []string) int {
	if len(args) == 0 {
		return printErr("Invalid route requirements command", errors.New("usage: gregale routes requirements <get|set> <slug> [flags]"))
	}
	switch args[0] {
	case "set":
		return cmdRoutesRequirementsSet(args[1:])
	case "get":
		return cmdRoutesRequirementsGet(args[1:])
	default:
		return printErr("Invalid route requirements command", errors.New("usage: gregale routes requirements <get|set> <slug> [flags]"))
	}
}

func cmdRoutesRequirementsSet(args []string) int {
	flags, positional := splitArgsForFlags(args)
	fs := newFlagSet("routes requirements set", flag.ContinueOnError)
	path := fs.String("requirements", "", "version 2 route requirements YAML or JSON file")
	revision := fs.Int64("expected-revision", -1, "current revision; use 0 for the first save")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(positional) != 1 || !validCLISlug(positional[0]) || *path == "" || *revision < 0 || *revision > api.RouteRequirementsMaxRevision {
		return printErr("Invalid route requirements save", errors.New("usage: gregale routes requirements set <slug> --requirements PATH --expected-revision N (0 for first save)"))
	}
	config, _, err := readPreviewCoverageRequirements(*path)
	if err == nil {
		config, _, err = routerequirements.NormalizeCoverage(config)
	}
	if err != nil || config.Version != 2 {
		if err == nil {
			err = errors.New("saved deployment checks need version 2 requirements for complete inventory coverage")
		}
		return printErr("Invalid route requirements", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	saved, err := client.SaveRouteRequirements(ctx, positional[0], api.SaveRouteRequirementsRequest{Requirements: config, ExpectedRevision: revision})
	if err != nil {
		return printErr("Could not save route requirements; read the current revision before retrying", err)
	}
	saved, err = normalizeSavedRequirementsResponse(saved)
	_, digest, _ := routerequirements.NormalizeCoverage(config)
	if err != nil || saved.SHA256 != digest || saved.Revision != *revision && saved.Revision != *revision+1 {
		return printErr("Invalid saved route requirements", errors.New("saved intent or revision does not match the submitted requirements"))
	}
	if jsonOutput {
		return jsonOut(writeJSON(saved))
	}
	_, _ = fmt.Fprintf(osStdout, "Saved route requirements for %s\nRevision: %d\nSHA-256: %s\n", positional[0], saved.Revision, saved.SHA256)
	return 0
}

func cmdRoutesRequirementsGet(args []string) int {
	flags, positional := splitArgsForFlags(args)
	fs := newFlagSet("routes requirements get", flag.ContinueOnError)
	output := fs.String("out", "", "export normalized requirements JSON to a new file for policy planning")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(positional) != 1 || !validCLISlug(positional[0]) {
		return printErr("Invalid route requirements read", errors.New("usage: gregale routes requirements get <slug> [--out PATH]"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	saved, err := client.GetSavedRouteRequirements(ctx, positional[0])
	if err != nil {
		return printErr("Could not read saved route requirements", err)
	}
	saved, err = normalizeSavedRequirementsResponse(saved)
	if err != nil {
		return printErr("Invalid saved route requirements", err)
	}
	if *output != "" {
		body, err := json.MarshalIndent(saved.Requirements, "", "  ")
		if err == nil {
			err = writeRoutePolicyPlan(*output, append(body, '\n'))
		}
		if err != nil {
			return printErr("Could not export route requirements", err)
		}
	}
	if jsonOutput {
		return jsonOut(writeJSON(saved))
	}
	_, _ = fmt.Fprintf(osStdout, "Route requirements for %s\nRevision: %d\nSHA-256: %s\nUpdated: %s\n", positional[0], saved.Revision, saved.SHA256, saved.UpdatedAt.Format(time.RFC3339))
	if *output != "" {
		_, _ = fmt.Fprintf(osStdout, "Exported normalized requirements: %s\n", previewReportText(*output))
	}
	return 0
}

func normalizeSavedRequirementsResponse(saved api.SavedRouteRequirements) (api.SavedRouteRequirements, error) {
	config, digest, err := routerequirements.NormalizeCoverage(saved.Requirements)
	if err != nil || config.Version != 2 || digest != saved.SHA256 || saved.AppID == "" || saved.Revision < 1 || saved.Revision > api.RouteRequirementsMaxRevision {
		return saved, errors.New("saved requirements identity, revision or fingerprint is unavailable")
	}
	saved.Requirements = config
	return saved, nil
}
