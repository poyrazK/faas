package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/routerequirements"
)

func readRoutePolicyPlan(path, slug string) (api.RoutePolicyPlan, error) {
	file, err := openCustomerFile(path)
	if err != nil {
		return api.RoutePolicyPlan{}, fmt.Errorf("open route plan: %w", err)
	}
	defer func() { _ = file.Close() }()
	body, err := io.ReadAll(io.LimitReader(file, api.RoutePolicyArtifactMaxBytes+1))
	if err != nil {
		return api.RoutePolicyPlan{}, fmt.Errorf("read route plan: %w", err)
	}
	if len(body) > api.RoutePolicyArtifactMaxBytes {
		return api.RoutePolicyPlan{}, errors.New("route plan exceeds the artifact size limit")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	var plan api.RoutePolicyPlan
	if err := decoder.Decode(&plan); err != nil {
		return plan, fmt.Errorf("decode route plan: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return plan, errors.New("route plan must contain exactly one JSON object")
	}
	return plan, routerequirements.ValidateArtifact(plan, slug)
}

func cmdRoutesApply(args []string) int {
	flags, positional := splitArgsForFlags(args, "confirm")
	fs := newFlagSet("routes apply", flag.ContinueOnError)
	path := fs.String("plan", "", "reviewed server plan JSON file")
	confirm := fs.Bool("confirm", false, "confirm application of every reviewed change")
	key := fs.String("idempotency-key", "", "retry key (defaults to the reviewed plan fingerprint)")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(positional) != 1 || !validCLISlug(positional[0]) || *path == "" || !*confirm {
		return printErr("Invalid route apply", errors.New("usage: gregale routes apply <slug> --plan PATH --confirm [--idempotency-key KEY]"))
	}
	plan, err := readRoutePolicyPlan(*path, positional[0])
	if err != nil {
		return printErr("Invalid route plan", err)
	}
	if *key == "" {
		*key = plan.SHA256
	}
	if len(*key) > api.RoutePolicyIdempotencyKeyMaxBytes {
		return printErr("Invalid idempotency key", errors.New("key exceeds size limit"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	request := api.RoutePolicyPlanRequest{Requirements: *plan.Requirements, ThrottleBurst: plan.ThrottleBurst, DeploymentID: plan.DeploymentID, ConsolidateBudgets: plan.ConsolidateBudgets}
	if plan.RequirementsRevision > 0 {
		request.Requirements = api.RouteRequirementsConfig{}
		request.Saved, request.ExpectedRevision = true, &plan.RequirementsRevision
	}
	response, err := client.ApplyRoutePolicy(ctx, positional[0], *key, api.RoutePolicyApplyRequest{
		RoutePolicyPlanRequest: request,
		ExpectedPlanSHA256:     plan.SHA256, Confirm: true})
	if err != nil {
		return printErr("Could not apply route policy; retry this plan with the same idempotency key to recover any committed result", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(response))
	}
	_, _ = fmt.Fprintf(osStdout, "Route policy committed for %s\nReceipt: %s\nChanges: %d; verification: %s; replayed: %t\nGateway confirmation: %s\n",
		positional[0], response.Receipt.ID, len(response.Receipt.Changes), response.Receipt.Verification.Status, response.Replayed, response.GatewayState)
	for _, change := range response.Receipt.Changes {
		_, _ = fmt.Fprintf(osStdout, "  %s %s: %s\n", change.Operation, change.Kind, change.RuleID)
	}
	return 0
}
