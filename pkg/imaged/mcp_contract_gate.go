package imaged

// ADR-20261127191645: the tool contract gate for sold MCP servers. After a
// production candidate of a listed app passed its smoke and verification
// contract, imaged lists the candidate's MCP catalog through the
// candidate-pinned session and compares it with the contract the seller
// promised. A breaking change fails the deployment like a failed smoke,
// unless the seller holds an audited one-shot override.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apihostingreceipt"
	"github.com/onebox-faas/faas/pkg/mcphosting"
	"github.com/onebox-faas/faas/pkg/state"
)

// toolContractBudget bounds opening the session, the optional secret and
// the catalog discovery of one candidate.
const toolContractBudget = 45 * time.Second

// ToolContractGate is what imaged needs to run the gate.
type ToolContractGate struct {
	Verifier apihostingreceipt.Verifier
	Store    state.Store
	// Secrets opens the listing's probe secret; nil makes a listing that
	// names one unavailable evidence.
	Secrets  VerificationSecretOpener
	NodeName string
}

// ApplyMCPToolContract runs the gate for a candidate whose smoke verified.
// It returns smoke with the check attached; a breaking candidate without an
// override turns smoke into a failure. A session, secret or store that is
// not available is unavailable evidence, retried through the hosting
// verification recovery window.
func ApplyMCPToolContract(ctx context.Context, g ToolContractGate, app state.App, dep state.Deployment, smoke apihostingreceipt.SmokeResult) (apihostingreceipt.SmokeResult, error) {
	if smoke.Status != apihostingreceipt.SmokeVerified || !verificationIsProduction(app, dep) {
		return smoke, nil
	}
	store, ok := g.Store.(state.MCPContractStore)
	if !ok {
		return smoke, nil
	}
	baseline, err := store.MCPContractBaseline(ctx, app.ID)
	if errors.Is(err, state.ErrNotFound) || err == nil && baseline.AccountID != app.AccountID {
		return smoke, nil
	}
	if err != nil {
		return contractUnavailable(smoke, apihostingreceipt.SmokeErrorContractUnavailable, fmt.Errorf("read MCP contract baseline: %w", err))
	}
	promised, err := mcphosting.ReadContract(bytes.NewReader(baseline.Contract))
	if err != nil {
		// apid validated it on publish; a stored contract that no longer
		// reads is the platform's problem, not the candidate's.
		return contractUnavailable(smoke, apihostingreceipt.SmokeErrorContractUnavailable, fmt.Errorf("stored MCP contract: %w", err))
	}
	runCtx, cancel := context.WithTimeout(ctx, toolContractBudget)
	defer cancel()
	session, err := g.Verifier.OpenCandidateSession(runCtx, app.Slug, dep.ID, HostingHealthPath(app, dep), toolContractBudget+15*time.Second)
	if err != nil {
		if ctx.Err() != nil {
			return smoke, ctx.Err()
		}
		var publication *apihostingreceipt.ChallengePublicationError
		if errors.As(err, &publication) {
			return contractUnavailable(smoke, apihostingreceipt.SmokeErrorAuthorizationUnavailable, err)
		}
		return contractUnavailable(smoke, apihostingreceipt.SmokeErrorGatewayUnavailable, err)
	}
	token, release, err := openProbeSecret(runCtx, g, app, dep, baseline.ProbeSecret)
	if err != nil {
		if ctx.Err() != nil {
			return smoke, ctx.Err()
		}
		return contractUnavailable(smoke, apihostingreceipt.SmokeErrorContractUnavailable, err)
	}
	defer release()
	candidate, discoverErr := discoverCandidateContract(runCtx, session, baseline.Endpoint, token, promised.ProtocolVersion)
	if ctx.Err() != nil {
		return smoke, ctx.Err()
	}
	check := state.MCPContractCheck{DeploymentID: dep.ID, AppID: app.ID, AccountID: app.AccountID, BaselineSHA256: baseline.SHA256}
	report := &apihostingreceipt.MCPContractReport{BaselineSHA256: baseline.SHA256}
	var diff mcphosting.ContractDiff
	if discoverErr != nil {
		check.Verdict = state.MCPContractUnverified
	} else {
		raw, err := mcphosting.MarshalContract(candidate)
		if err != nil {
			return contractUnavailable(smoke, apihostingreceipt.SmokeErrorContractUnavailable, fmt.Errorf("encode candidate MCP contract: %w", err))
		}
		if diff, err = mcphosting.CompareContracts(promised, candidate); err != nil {
			return contractUnavailable(smoke, apihostingreceipt.SmokeErrorContractUnavailable, fmt.Errorf("compare MCP contracts: %w", err))
		}
		sum := sha256.Sum256(raw)
		check.Candidate, check.CandidateSHA256 = raw, hex.EncodeToString(sum[:])
		check.Verdict = contractVerdict(diff, check.CandidateSHA256 == baseline.SHA256)
		if check.Changes, err = json.Marshal(diff.Changes); err != nil {
			return contractUnavailable(smoke, apihostingreceipt.SmokeErrorContractUnavailable, fmt.Errorf("encode MCP contract changes: %w", err))
		}
	}
	report.Verdict, report.ContractSHA256 = check.Verdict, check.CandidateSHA256
	for _, c := range diff.Changes {
		report.Changes = append(report.Changes, apihostingreceipt.MCPContractChange(c))
	}
	recorded, err := store.RecordMCPContractCheck(ctx, check, check.Verdict == state.MCPContractBreaking || check.Verdict == state.MCPContractUnverified)
	switch {
	case errors.Is(err, state.ErrMCPContractOverrideRequired) && check.Verdict == state.MCPContractUnverified:
		// Without an override, a catalog the platform cannot list is
		// unavailable evidence: retried, then failed closed.
		retry, retryErr := contractUnavailable(smoke, apihostingreceipt.SmokeErrorContractUnavailable,
			fmt.Errorf("the candidate's MCP tools could not be listed at %s: %w", endpointPath(baseline.Endpoint), discoverErr))
		retry.MCPContract = report
		return retry, retryErr
	case errors.Is(err, state.ErrMCPContractOverrideRequired):
		smoke.MCPContract = report
		smoke.Status = apihostingreceipt.SmokeFailed
		smoke.ErrorCode = apihostingreceipt.SmokeErrorMCPContractBreaking
		smoke.Error = breakingSummary(diff)
		return smoke, nil
	case errors.Is(err, state.ErrConflict):
		// Already accepted: an earlier attempt promoted this deployment.
		return smoke, nil
	case err != nil:
		return contractUnavailable(smoke, apihostingreceipt.SmokeErrorContractUnavailable, fmt.Errorf("record MCP contract check: %w", err))
	}
	report.Overridden = recorded.OverrideID != ""
	smoke.MCPContract = report
	return smoke, nil
}

// contractVerdict names a candidate's comparison with the baseline.
func contractVerdict(diff mcphosting.ContractDiff, sameSHA bool) string {
	switch {
	case diff.Breaking:
		return state.MCPContractBreaking
	case diff.NeedsReview:
		return state.MCPContractReview
	case sameSHA:
		return state.MCPContractUnchanged
	default:
		return state.MCPContractCompatible
	}
}

// breakingSummary lists the breaking changes, at most eight, for the
// deployment's failure detail.
func breakingSummary(diff mcphosting.ContractDiff) string {
	var parts []string
	for _, c := range diff.Changes {
		if c.Severity != "breaking" {
			continue
		}
		if len(parts) == 8 {
			parts = append(parts, "…")
			break
		}
		subject := c.Tool
		if subject == "" {
			subject = strings.Trim(c.Resource+c.ResourceTemplate+c.Prompt+c.Capability, " ")
		}
		parts = append(parts, fmt.Sprintf("%s: %s %s", subject, c.Kind, c.Path))
	}
	return "breaking MCP tool contract changes for paying customers: " + strings.Join(parts, "; ")
}

// discoverCandidateContract lists the candidate's catalog at the listing
// endpoint's path, as a stateless client would.
func discoverCandidateContract(ctx context.Context, session *apihostingreceipt.CandidateSession, endpoint, token, version string) (mcphosting.Contract, error) {
	if version == "" {
		version = mcphosting.ProtocolVersion
	}
	client, err := mcphosting.NewClient(session.URL(endpointPath(endpoint)), token, version)
	if err != nil {
		return mcphosting.Contract{}, err
	}
	client.HTTP = session.Client()
	if err := client.OpenStateless(ctx); err != nil {
		return mcphosting.Contract{}, fmt.Errorf("initialize: %w", err)
	}
	catalog, exchange, err := client.DiscoverCatalog(ctx)
	if err != nil {
		return mcphosting.Contract{}, fmt.Errorf("list catalog: %w", err)
	}
	if len(exchange.RejectedTools) != 0 {
		return mcphosting.Contract{}, fmt.Errorf("%d tool definitions are invalid", len(exchange.RejectedTools))
	}
	return mcphosting.NewCatalogContract(client.Version, catalog)
}

// endpointPath is the listing endpoint's path; apid checked the host.
func endpointPath(endpoint string) string {
	u, err := url.Parse(endpoint)
	if err != nil || u.Path == "" {
		return "/mcp"
	}
	return u.EscapedPath()
}

// openProbeSecret opens the listing's probe secret through secretd under the
// deployment's verification lease. The returned release ends the lease.
func openProbeSecret(ctx context.Context, g ToolContractGate, app state.App, dep state.Deployment, name string) (string, func(), error) {
	if name == "" {
		return "", func() {}, nil
	}
	leases, ok := g.Store.(state.VerificationLeaseStore)
	if g.Secrets == nil || g.NodeName == "" || !ok {
		return "", nil, errors.New("this node cannot open the MCP probe secret")
	}
	until := time.Now().Add(toolContractBudget + api.VerificationLeaseGrace)
	if err := leases.PutDeploymentVerificationLease(ctx, dep.ID, app.ID, g.NodeName, until); err != nil {
		return "", nil, fmt.Errorf("record verification lease: %w", err)
	}
	release := func() { _ = leases.DeleteDeploymentVerificationLease(context.WithoutCancel(ctx), dep.ID) }
	values, missing, err := g.Secrets.OpenVerificationSecrets(ctx, app.ID, dep.ID, []string{name})
	if err != nil {
		release()
		return "", nil, fmt.Errorf("open MCP probe secret: %w", err)
	}
	if len(missing) > 0 || values[name] == "" {
		release()
		return "", nil, fmt.Errorf("the MCP probe secret %s is not set in this deployment's environment", name)
	}
	return values[name], release, nil
}
