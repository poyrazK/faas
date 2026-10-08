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
)

func readLifecycleApprovalJSON(path string, value any) error {
	file, err := openCustomerFile(path)
	if err != nil {
		return err
	}
	defer file.Close()
	body, err := io.ReadAll(io.LimitReader(file, api.RoutePolicyRequestMaxBytes+1))
	if err != nil {
		return err
	}
	if len(body) > api.RoutePolicyRequestMaxBytes {
		return errors.New("approval input exceeds the request size limit")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(value); err != nil {
		return err
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return errors.New("approval input must contain one JSON value")
	}
	return nil
}
func cmdRoutesLifecyclePrepareApproval(args []string) int {
	flags, positional := splitArgsForFlags(args)
	fs := newFlagSet("routes lifecycle prepare-approval", flag.ContinueOnError)
	from := fs.String("from-deployment", "", "serving baseline UUID")
	to := fs.String("to-deployment", "", "candidate UUID")
	mappingFile := fs.String("mappings", "", "explicit successor mapping JSON array, optionally with destination app/deployment/capture pins")
	out := fs.String("out", "", "new approval request file")
	if fs.Parse(flags) != nil {
		return 1
	}
	if len(positional) != 1 || !validCLISlug(positional[0]) || !canonicalRouteHealthID(*from) || !canonicalRouteHealthID(*to) || *from == *to || *mappingFile == "" || *out == "" || rejectUnexpectedFlagArgs(fs) {
		PrintUsage(osStderr, "usage: gregale routes lifecycle prepare-approval <slug> --from-deployment UUID --to-deployment UUID --mappings FILE --out REQUEST.json [--json]", "cli")
		return 1
	}
	var mappings []api.RouteLifecycleMapping
	if err := readLifecycleApprovalJSON(*mappingFile, &mappings); err != nil {
		return printErr("Invalid mappings", err)
	}
	if len(mappings) == 0 {
		return printErr("Invalid mappings", errors.New("provide explicit successor operations"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	baseline, err := readRouteMigrationDeploymentSet(ctx, client, map[string]string{positional[0]: *from})
	if err != nil {
		return printErr("Could not read baseline", err)
	}
	candidate, err := readRouteMigrationDeploymentSet(ctx, client, map[string]string{positional[0]: *to})
	if err != nil {
		return printErr("Could not read candidate", err)
	}
	b, c := baseline[positional[0]], candidate[positional[0]]
	if b.spec == nil || c.spec == nil || b.deployment.AppID != c.deployment.AppID || len(b.captureSHA) != 64 || len(c.captureSHA) != 64 {
		return printErr("Incomplete captures", errors.New("complete app-owned captures and authoritative doc_sha256 values are required"))
	}
	gate, err := client.GetCanaryRouteGate(ctx, positional[0])
	if err != nil {
		return printErr("Could not read canary gate", err)
	}
	removal, err := client.GetRouteRemovalPolicy(ctx, positional[0])
	if err != nil {
		return printErr("Could not read removal policy", err)
	}
	check, err := client.CheckRouteRequirements(ctx, positional[0], api.CheckRouteRequirementsRequest{DeploymentID: *to})
	if err != nil {
		return printErr("Could not read configured route evidence", err)
	}
	if gate.AppID != c.deployment.AppID || removal.AppID != c.deployment.AppID || check.AppID != c.deployment.AppID || check.DeploymentID != *to || check.RequirementsRevision < 1 || len(check.ConfigurationSHA256) != 64 {
		return printErr("Incomplete policy evidence", errors.New("current app-bound policy revisions and configuration hash are required"))
	}
	request := api.ApproveRouteLifecycleRequest{ExpectedGateRevision: &gate.Revision, ExpectedRequirementsRevision: &check.RequirementsRevision, ExpectedRemovalPolicyRevision: &removal.Revision, ConfigurationSHA256: check.ConfigurationSHA256, BaselineDeploymentID: *from, CandidateDeploymentID: *to, BaselineContractSHA256: b.captureSHA, CandidateContractSHA256: c.captureSHA, Mappings: mappings}
	body, err := json.MarshalIndent(request, "", "  ")
	if err != nil {
		return printErr("Could not encode request", err)
	}
	if err = writeRoutePolicyPlan(*out, append(body, '\n')); err != nil {
		return printErr("Could not save request", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(request))
	}
	fmt.Fprintf(osStdout, "Saved pinned lifecycle approval request to %s. Review its mappings, then submit with routes lifecycle approve. Compatibility is checked by the server.\n", previewReportText(*out))
	return 0
}
func cmdRoutesLifecycleApprove(args []string) int {
	flags, positional := splitArgsForFlags(args)
	fs := newFlagSet("routes lifecycle approve", flag.ContinueOnError)
	path := fs.String("request", "", "reviewed pinned request JSON")
	if fs.Parse(flags) != nil {
		return 1
	}
	if len(positional) != 1 || !validCLISlug(positional[0]) || *path == "" || rejectUnexpectedFlagArgs(fs) {
		PrintUsage(osStderr, "usage: gregale routes lifecycle approve <slug> --request FILE [--json]", "cli")
		return 1
	}
	var request api.ApproveRouteLifecycleRequest
	if err := readLifecycleApprovalJSON(*path, &request); err != nil {
		return printErr("Invalid request", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	receipt, err := client.ApproveRouteLifecycle(ctx, positional[0], request)
	if err != nil {
		return printErr("Lifecycle approval failed", err)
	}
	return printLifecycleApproval(receipt)
}
func cmdRoutesLifecycleReceipt(args []string) int {
	flags, positional := splitArgsForFlags(args)
	fs := newFlagSet("routes lifecycle receipt", flag.ContinueOnError)
	id := fs.String("id", "", "persisted approval UUID")
	if fs.Parse(flags) != nil {
		return 1
	}
	if len(positional) != 1 || !validCLISlug(positional[0]) || !canonicalRouteHealthID(*id) || rejectUnexpectedFlagArgs(fs) {
		PrintUsage(osStderr, "usage: gregale routes lifecycle receipt <slug> --id UUID [--json]", "cli")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	receipt, err := client.GetRouteLifecycleApproval(ctx, positional[0], *id)
	if err != nil {
		return printErr("Could not read lifecycle receipt", err)
	}
	return printLifecycleApproval(receipt)
}
func printLifecycleApproval(receipt api.RouteLifecycleApproval) int {
	if jsonOutput {
		return jsonOut(writeJSON(receipt))
	}
	fmt.Fprintf(osStdout, "Lifecycle approval %s: %s; expires %s\n", previewReportText(receipt.ID), previewReportText(receipt.Compatibility), receipt.ValidUntil.UTC().Format(time.RFC3339))
	if receipt.InvalidatedAt != nil {
		fmt.Fprintln(osStdout, "Invalidated by a capture or routing configuration change; a fresh review is required.")
	}
	return 0
}
