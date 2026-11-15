package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/mcphosting"
	"github.com/onebox-faas/faas/pkg/toolgrant"
	"github.com/onebox-faas/faas/pkg/toolpublication"
)

func cmdMCPToolPublication(args []string) int {
	if len(args) == 0 {
		return printErr("MCP publication", errors.New("use get, approve or revoke"))
	}
	verb := args[0]
	fs := newFlagSet("mcp-tool-publication-"+verb, flag.ContinueOnError)
	var id, digest, ref string
	fs.StringVar(&id, "id", "", "exact publication identity")
	if verb == "approve" || verb == "revoke" {
		fs.StringVar(&digest, "request-sha256", "", "reviewed candidate verification request digest")
	}
	if verb == "approve" {
		fs.StringVar(&ref, "credential-secret-ref", "", "owner-selected secret:MCP_* test token; never a token value")
	}
	if fs.Parse(args[1:]) != nil {
		return 1
	}
	if fs.NArg() != 0 || !toolpublication.ValidID(id) || verb != "get" && verb != "approve" && verb != "revoke" || verb != "get" && !toolpublication.ValidID(digest) || verb == "approve" && !toolgrant.ValidCredentialRef(ref) {
		return printErr("MCP publication", errors.New("use get, approve or revoke with an exact --id, reviewed --request-sha256 and test-secret reference for approval"))
	}
	c, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	if verb == "get" {
		out, err := c.GetToolPublication(context.Background(), id)
		if err != nil {
			return printErr("MCP publication", err)
		}
		return jsonOut(writeJSON(out))
	}
	out, err := c.DecideToolPublicationVerification(context.Background(), id, api.ToolPublicationVerificationDecision{RequestSHA256: digest, CredentialSecretRef: ref, Revoke: verb == "revoke"})
	if err != nil {
		return printErr("MCP publication decision", err)
	}
	return jsonOut(writeJSON(out))
}

func validatePrivatePublicationOptions(o mcpDeployOptions, cfg mcphosting.Config, smoke *mcpSmokeGate) error {
	if o.publicationID == "" {
		return nil
	}
	if !toolpublication.ValidID(o.publicationID) || cfg.Auth.Mode != "external-oauth" || o.tokenEnv != "" || o.secretsFile != "" || o.validateEndpoint != "" || o.releasePolicy != "" || o.accessAudit != "" || o.mcpTaskMigrationOverride || !o.requireCatalogPin || !o.requireReviewedTools || o.expectedServing == nil || smoke == nil || !smoke.allowTools || !smoke.suite.RequiresToolExecution() {
		return errors.New("platform private publication requires a reviewed private candidate, catalog pin, tool allowlist, serving baseline and behavioral fixtures without local credentials or role/Task overrides")
	}
	return nil
}

func privatePublicationFixtures(path string) (string, error) {
	f, err := openCustomerFile(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, api.ToolPublicationFixturesMaxBytes+1))
	if err != nil || len(data) > api.ToolPublicationFixturesMaxBytes {
		return "", errors.New("publication fixtures exceed the review bound")
	}
	return string(data), nil
}

func waitPrivatePublicationVerification(ctx context.Context, c *Client, publication string, in api.ToolPublicationVerificationRequest) (api.ToolPublicationVerification, error) {
	notified := false
	for {
		v, err := c.VerifyToolPublication(ctx, publication, in)
		if err != nil {
			var capacity *api.APIError
			if !errors.As(err, &capacity) || capacity.Problem.Code != "publication_verification_capacity" {
				return v, err
			}
			if err := waitPrivatePublicationPoll(ctx); err != nil {
				return v, err
			}
			continue
		}
		if v.PublicationID != publication || v.DeploymentID != in.DeploymentID || v.CatalogSHA256 != in.CatalogSHA256 || !toolpublication.ValidID(v.RequestSHA256) {
			return v, errors.New("platform verification returned another candidate identity")
		}
		switch v.Status {
		case "completed":
			if v.Outcome == nil || !v.Outcome.OK || !v.Outcome.ProtocolVerified {
				return v, errors.New("candidate lacks verified private test evidence")
			}
			return v, nil
		case "pending", "approved", "running":
			if v.Status == "pending" && !notified {
				_, _ = fmt.Fprintf(osStderr, "Private candidate %s awaits owner approval (request %s). Inspect with gregale mcp server tool-publication get --id %s.\n", v.DeploymentID, v.RequestSHA256, publication)
				notified = true
			}
		default:
			return v, errors.New("private candidate verification stopped; inspect its retained receipt before creating another publication")
		}
		if err := waitPrivatePublicationPoll(ctx); err != nil {
			return v, err
		}
	}
}

func waitPrivatePublicationPoll(ctx context.Context) error {
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func finishPrivateMCPDeploy(ctx context.Context, c *Client, o mcpDeployOptions, cfg mcphosting.Config, id string, pin *mcpCatalogPinReceipt) int {
	if pin == nil || pin.SHA256 == "" {
		return printErr("MCP private publication", errors.New("catalog pin required"))
	}
	if err := checkMCPPublicationBaseline(ctx, c, o.slug, id, pin); err != nil {
		return printErr("MCP publication baseline", err)
	}
	endpoint, candidate, serving, err := prepareMCPCandidate(ctx, c, o.slug, id, cfg)
	if err != nil {
		return printErr("MCP candidate", err)
	}
	if o.expectedServing == nil || serving != *o.expectedServing {
		return printErr("MCP publication baseline", errors.New("serving release changed since review"))
	}
	policy, err := c.GetMCPReadinessPolicy(ctx, o.slug)
	if err != nil {
		return printErr("MCP readiness policy", err)
	}
	if policy.Enabled {
		return printErr("MCP private publication", errors.New("hosted private publication does not yet support configured readiness roles"))
	}
	raw, err := privatePublicationFixtures(o.smokeFixtures)
	if err != nil {
		return printErr("MCP publication fixtures", err)
	}
	v, err := waitPrivatePublicationVerification(ctx, c, o.publicationID, api.ToolPublicationVerificationRequest{DeploymentID: id, CatalogSHA256: pin.SHA256, FixturesJSON: raw})
	if err != nil {
		return printErr("MCP private verification", err)
	}
	contract, err := mcphosting.ReadContract(bytes.NewReader(v.Outcome.Catalog))
	if err != nil {
		return printErr("MCP private catalog", err)
	}
	report := mcphosting.Report{Endpoint: candidate, OK: true, Tools: contract.Tools, Capabilities: contract.Capabilities, Extensions: contract.Extensions, Resources: contract.Resources, ResourceTemplates: contract.ResourceTemplates, Prompts: contract.Prompts}
	if err := validateMCPReviewedToolCatalog(cfg, report); err != nil {
		return printErr("MCP reviewed catalog", err)
	}
	health := probeMCPCatalogDeployment(ctx, c, o.slug, id, candidate, 2*time.Second)
	if err := validateMCPDeploymentCatalogFleetPin(pin, health); err != nil {
		return printErr("MCP catalog pin", err)
	}
	if err := checkMCPPublicationBaseline(ctx, c, o.slug, id, pin); err != nil {
		return printErr("MCP publication baseline", err)
	}
	smoke := mcphosting.SmokeReport{OK: true, Checks: []mcphosting.Check{}}
	for _, check := range v.Outcome.Checks {
		smoke.Checks = append(smoke.Checks, mcphosting.Check{Name: check.Name, Status: check.Status})
	}
	_, err = c.PatchDeploymentTrafficIfServingWithMCPAccess(ctx, id, 100, serving, nil)
	receipt := map[string]any{"app": o.slug, "deployment_id": id, "endpoint": endpoint, "auth_mode": "external-oauth", "verification": report, "smoke_tests": smoke, "promoted": err == nil,
		"catalog_integrity": map[string]any{"deployment_id": id, "pin": pin, "health": health}, "protocol_integrity": map[string]any{"qualification": map[string]any{"qualified": true}}, "platform_verification": v.RequestSHA256}
	if err != nil {
		receipt["promotion_error"] = "guarded promotion failed; inspect the candidate and serving release"
	}
	if code := jsonOut(writeJSON(receipt)); code != 0 {
		return code
	}
	if err != nil {
		return 1
	}
	return 0
}
