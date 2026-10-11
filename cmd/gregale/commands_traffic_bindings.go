// adr: 429 — gated promotion always uses an API route that enforces the policy.
package main

import (
	"context"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func promoteTrafficWithBindings(ctx context.Context, client *api.Client, target api.DeploymentResponse, servingID string, age time.Duration, allowUnsupported, requireAck bool) int {
	req := api.BindingPromotionRequest{MaxVerificationAge: age.String(), AllowUnsupported: allowUnsupported, RequireApplicationAck: requireAck}
	if servingID != "" {
		req.ExpectedServingDeploymentID = &servingID
	}
	receipt, err := client.PromoteDeploymentWithBindings(ctx, target.ID, req)
	if err != nil {
		return printBindingPromotionError(err)
	}
	report := receipt.BindingsCheck
	scope := target.Scope
	if scope == "" {
		scope = "default"
	}
	if report == nil || !report.Passed || !sameBindingDeployment(report.DeploymentID, target.ID) || !sameBindingDeployment(report.ExpectedDeploymentID, target.ID) ||
		report.Scope != scope || report.CheckedAt.IsZero() || !bindingPromotionPolicyAtLeast(report, age, allowUnsupported, requireAck) || len(report.Blockers) != 0 ||
		!sameBindingDeployment(receipt.Deployment.ID, target.ID) || receipt.Deployment.TrafficPercent != 100 || receipt.ToPercent != 100 {
		return printErr("Traffic promote failed", fmt.Errorf("server did not confirm a passed bindings check for the requested deployment and policy; inspect traffic before retrying"))
	}
	if jsonOutput {
		return jsonOut(writeJSON(receipt))
	}
	if receipt.AlreadyPromoted {
		PrintOK(osStdout, "%s is already promoted at 100%% production traffic; bindings check passed (%s coverage).", deploymentLabel(receipt.Deployment), report.Coverage)
	} else {
		PrintOK(osStdout, "Promoted %s: %d%% → 100%% production traffic; bindings check passed (%s coverage).", deploymentLabel(receipt.Deployment), receipt.FromPercent, report.Coverage)
	}
	return 0
}

func printBindingPromotionError(err error) int {
	return printErr("Traffic promote failed", err)
}

func bindingPromotionPolicyAtLeast(report *api.BindingCheckReport, age time.Duration, allowUnsupported, requireAck bool) bool {
	checkedAge, err := time.ParseDuration(report.MaxVerificationAge)
	return err == nil && checkedAge > 0 && checkedAge <= age && (!report.AllowUnsupported || allowUnsupported) && (!requireAck || report.RequireApplicationAck)
}
