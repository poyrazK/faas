// adr: 20261104104521
package main

import (
	"context"
	"flag"
	"fmt"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// cmdAppScaling exposes the same recovery identity and revision as management MCP.
func cmdAppScaling(slug string, args []string) int {
	fs := newFlagSet("app scaling", flag.ContinueOnError)
	environment := fs.String("environment", "", "select a named project environment")
	appID := fs.String("expected-app-id", "", "immutable app UUID from the reviewed scaling snapshot")
	environmentID := fs.String("expected-environment-id", "", "immutable environment UUID from the scaling snapshot")
	receiptID := fs.String("receipt", "", "recover this request UUID without writing")
	requestID := fs.String("request-id", "", "stable request UUID for this reviewed change")
	revision := fs.Int64("expected-revision", -1, "revision returned by app scaling")
	ram := fs.Int("ram", 0, "RAM in MB")
	cpu := fs.Int("cpu-millicores", 0, "CPU allowance in millicores")
	concurrency := fs.Int("max-concurrency", 0, "most instances the app runs at once")
	minimum := fs.Int("min", 0, "minimum warm instance count, including zero")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 0 {
		return printErr("Invalid scaling arguments", fmt.Errorf("unexpected positional argument"))
	}
	req := api.ApplyAppScalingRequest{ExpectedAppID: *appID, RequestID: *requestID, ExpectedEnvironmentID: *environmentID, ExpectedRevision: revision}
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "ram":
			req.RAMMB = ram
		case "cpu-millicores":
			req.CPUMillicores = cpu
		case "max-concurrency":
			req.MaxConcurrency = concurrency
		case "min":
			req.MinInstances = minimum
		}
	})
	write := *appID != "" || *environmentID != "" || *requestID != "" || *revision != -1 || req.RAMMB != nil || req.CPUMillicores != nil || req.MaxConcurrency != nil || req.MinInstances != nil
	if *receiptID != "" && write {
		return printErr("Invalid scaling arguments", fmt.Errorf("--receipt cannot be combined with a scaling write"))
	}
	if write {
		if p := req.Validate(); p != nil {
			// Name the field that failed: a zero --expected-app-id was
			// reported as missing flags (Hunt 32 H32-60).
			return printErr("Invalid scaling arguments", fmt.Errorf("%s; a write needs --expected-app-id, --request-id, --expected-revision and at least one scaling setting", appScalingFlagNames.Replace(p.Detail)))
		}
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()
	var out any
	switch {
	case *receiptID != "":
		out, err = client.GetAppScalingReceipt(ctx, slug, *environment, *receiptID)
	case write:
		out, err = client.ApplyAppScaling(ctx, slug, *environment, req)
	default:
		out, err = client.GetAppScaling(ctx, slug, *environment)
	}
	if err != nil {
		return printErr("Could not complete scaling request; recover or retry the original request ID", err)
	}
	return jsonOut(writeJSON(out))
}

// appScalingFlagNames spells the request's JSON fields as this command's
// flags in validation messages.
var appScalingFlagNames = strings.NewReplacer(
	"expected_environment_id", "--expected-environment-id",
	"expected_app_id", "--expected-app-id",
	"expected_revision", "--expected-revision",
	"request_id", "--request-id",
)
