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
	"github.com/onebox-faas/faas/pkg/mcptoolscan"
	"github.com/onebox-faas/faas/pkg/state"
)

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
	candidate, unavailableCode, discoverErr := listCandidateContract(ctx, g, app, dep, baseline, promised.ProtocolVersion)
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
		if check.Changes, err = storedChanges(diff.Changes); err != nil {
			return contractUnavailable(smoke, apihostingreceipt.SmokeErrorContractUnavailable, fmt.Errorf("encode MCP contract changes: %w", err))
		}
		if report.ToolScan, err = scanCandidate(&check, candidate, baseline); err != nil {
			return contractUnavailable(smoke, apihostingreceipt.SmokeErrorContractUnavailable, err)
		}
	}
	report.Verdict, report.ContractSHA256 = check.Verdict, check.CandidateSHA256
	// The report carries the changes the check stores, so the receipt, the
	// owner view and the webhook count the same list.
	var stored []mcphosting.ContractChange
	if len(check.Changes) > 0 {
		_ = json.Unmarshal(check.Changes, &stored)
	}
	for _, c := range stored {
		report.Changes = append(report.Changes, apihostingreceipt.MCPContractChange(c))
	}
	claims := state.MCPContractClaims{Contract: contractClaim(ctx, check.Verdict)}
	if check.ToolScanNewHigh > 0 {
		claims.ToolScan = state.MCPContractClaimAny
	}
	recorded, err := store.RecordMCPContractCheck(ctx, check, claims)
	switch {
	case errors.Is(err, state.ErrMCPContractOverrideRequired) && check.Verdict == state.MCPContractUnverified:
		// Without an override, a catalog the platform cannot list is
		// unavailable evidence: retried, then failed closed.
		retry, retryErr := contractUnavailable(smoke, unavailableCode,
			fmt.Errorf("the candidate's MCP tools could not be listed at %s: %w", endpointPath(baseline.Endpoint), discoverErr))
		retry.MCPContract = report
		return retry, retryErr
	case errors.Is(err, state.ErrMCPContractOverrideRequired):
		smoke.MCPContract = report
		smoke.Status = apihostingreceipt.SmokeFailed
		smoke.ErrorCode = apihostingreceipt.SmokeErrorMCPContractBreaking
		smoke.Error = breakingSummary(diff)
		return smoke, nil
	case errors.Is(err, state.ErrMCPToolScanOverrideRequired):
		smoke.MCPContract = report
		smoke.Status = apihostingreceipt.SmokeFailed
		smoke.ErrorCode = apihostingreceipt.SmokeErrorMCPToolScanBlocked
		smoke.Error = toolScanSummary(report.ToolScan)
		return smoke, nil
	case errors.Is(err, state.ErrConflict):
		// Already accepted: an earlier attempt promoted this deployment.
		return smoke, nil
	case err != nil:
		return contractUnavailable(smoke, apihostingreceipt.SmokeErrorContractUnavailable, fmt.Errorf("record MCP contract check: %w", err))
	}
	report.Overridden = recorded.OverrideID != ""
	if report.ToolScan != nil {
		report.ToolScan.Overridden = recorded.ToolScanOverrideID != ""
	}
	smoke.MCPContract = report
	return smoke, nil
}

// listCandidateContract opens the candidate session and the optional probe
// secret and lists the candidate's catalog. Any failure is returned as the
// error (with the smoke code a retry reports), so the caller records the
// candidate as unverified, which an override admits.
func listCandidateContract(ctx context.Context, g ToolContractGate, app state.App, dep state.Deployment, baseline state.MCPContractBaseline, version string) (mcphosting.Contract, string, error) {
	runCtx, cancel := context.WithTimeout(ctx, api.MCPContractGateBudget)
	defer cancel()
	session, err := g.Verifier.OpenCandidateSession(runCtx, app.Slug, dep.ID, HostingHealthPath(app, dep), api.MCPContractGateBudget+15*time.Second)
	if err != nil {
		var publication *apihostingreceipt.ChallengePublicationError
		if errors.As(err, &publication) {
			return mcphosting.Contract{}, apihostingreceipt.SmokeErrorAuthorizationUnavailable, err
		}
		return mcphosting.Contract{}, apihostingreceipt.SmokeErrorGatewayUnavailable, err
	}
	token, release, err := openProbeSecret(runCtx, g, app, dep, baseline.ProbeSecret)
	if err != nil {
		return mcphosting.Contract{}, apihostingreceipt.SmokeErrorContractUnavailable, err
	}
	defer release()
	candidate, err := discoverCandidateContract(runCtx, session, baseline.Endpoint, token, version)
	return candidate, apihostingreceipt.SmokeErrorContractUnavailable, err
}

// scanCandidate runs the tool safety scan (ADR-20261128135329) over the
// candidate's catalog, stores it on the check, and counts the high findings
// the effective baseline had not already reviewed.
func scanCandidate(check *state.MCPContractCheck, candidate mcphosting.Contract, baseline state.MCPContractBaseline) (*apihostingreceipt.MCPToolScanReport, error) {
	scan := mcptoolscan.Scan(candidate)
	raw, err := json.Marshal(scan)
	if err != nil {
		return nil, fmt.Errorf("encode MCP tool scan: %w", err)
	}
	var prior mcptoolscan.Report
	if baseline.ToolScanReviewed && len(baseline.ToolScan) > 0 {
		_ = json.Unmarshal(baseline.ToolScan, &prior)
	}
	out := &apihostingreceipt.MCPToolScanReport{RuleSet: scan.RuleSet}
	for _, f := range mcptoolscan.UnreviewedHigh(scan, prior) {
		out.NewHigh++
		out.Findings = append(out.Findings, apihostingreceipt.MCPToolScanFinding(f))
	}
	for _, f := range scan.Findings {
		switch f.Severity {
		case mcptoolscan.SeverityHigh:
			out.High++
		case mcptoolscan.SeverityMedium:
			out.Medium++
		default:
			out.Low++
		}
	}
	check.ToolScan, check.ToolScanHigh, check.ToolScanNewHigh = raw, out.High, out.NewHigh
	return out, nil
}

// toolScanSummary names the unreviewed high findings, at most
// api.MCPContractBreakingSummaryMax, for the deployment's failure detail.
func toolScanSummary(r *apihostingreceipt.MCPToolScanReport) string {
	var parts []string
	for i, f := range r.Findings {
		if i == api.MCPContractBreakingSummaryMax {
			parts = append(parts, "…")
			break
		}
		parts = append(parts, fmt.Sprintf("%s %s: %s at %s", f.Kind, f.Name, f.Rule, f.Path))
	}
	return "tool safety scan found high-severity issues in tools paying customers' models read: " + strings.Join(parts, "; ")
}

// contractClaim is the override a check may consume. A breaking candidate
// takes a bound override or the app's next unbound one. An unverified one
// takes a bound override at once, but an unbound one only in the last half of
// the recovery window: a catalog that could not be listed is usually
// transient, so earlier attempts retry instead of spending an override the
// seller meant for a breaking change.
func contractClaim(ctx context.Context, verdict string) state.MCPContractClaim {
	switch verdict {
	case state.MCPContractBreaking:
		return state.MCPContractClaimAny
	case state.MCPContractUnverified:
		deadline, ok := apihostingreceipt.VerificationRecoveryDeadline(ctx)
		if !ok || time.Until(deadline) <= api.HostingVerificationRecoveryWindow/2 {
			return state.MCPContractClaimAny
		}
		return state.MCPContractClaimBound
	default:
		return state.MCPContractClaimNone
	}
}

// storedChanges encodes at most api.MCPContractChangesMax changes, fewer if
// they would not fit the column's api.MCPContractChangesMaxBytes.
func storedChanges(changes []mcphosting.ContractChange) (json.RawMessage, error) {
	if len(changes) > api.MCPContractChangesMax {
		changes = changes[:api.MCPContractChangesMax]
	}
	for {
		raw, err := json.Marshal(changes)
		if err != nil || len(raw) <= api.MCPContractChangesMaxBytes {
			return raw, err
		}
		changes = changes[:len(changes)/2]
	}
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

// breakingSummary lists the breaking changes, at most
// api.MCPContractBreakingSummaryMax, for the deployment's failure detail.
func breakingSummary(diff mcphosting.ContractDiff) string {
	var parts []string
	for _, c := range diff.Changes {
		if c.Severity != "breaking" {
			continue
		}
		if len(parts) == api.MCPContractBreakingSummaryMax {
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
	// Tools the client rejects are left out, as the CLI leaves them out of
	// the listing's contract; a tool that turns invalid reads as removed.
	catalog, _, err := client.DiscoverCatalog(ctx)
	if err != nil {
		return mcphosting.Contract{}, fmt.Errorf("list catalog: %w", err)
	}
	contract, err := mcphosting.NewCatalogContract(client.Version, catalog)
	if err != nil {
		return mcphosting.Contract{}, err
	}
	if raw, err := mcphosting.MarshalContract(contract); err == nil && len(raw) > api.MCPListingContractMaxBytes {
		return mcphosting.Contract{}, fmt.Errorf("the catalog takes %d bytes, more than the %d a contract holds", len(raw), api.MCPListingContractMaxBytes)
	}
	return contract, nil
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
	until := time.Now().Add(api.MCPContractGateBudget + api.VerificationLeaseGrace)
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
