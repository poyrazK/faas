package toolregistry

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/mcphosting"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/toolgrant"
	"github.com/onebox-faas/faas/pkg/toolpublication"
)

var errPublicationVerificationCapacity = errors.New("private publication verification workers unavailable")

func (e *PrivateExecutor) PublicationVerification() httpkit.AccountHandler {
	return func(w http.ResponseWriter, r *http.Request, acct state.Account) {
		w.Header().Set("Cache-Control", "no-store")
		if !e.opts.PublicationEnabled || e.opts.Identities == nil || e.opts.Endpoint == nil || e.opts.VerifyPin == nil || e.opts.PublicationProtocols == nil || e.opts.HTTP == nil {
			api.WriteProblem(w, api.NewProblem(503, "private_publication_disabled", "Private publication unavailable", "the operator must enable platform-held publication verification"))
			return
		}
		var err error
		r, err = e.freshPublicationRequest(r, acct, r.Method == http.MethodPut)
		if err != nil {
			respond(w, nil, err)
			return
		}
		if r.Method == http.MethodPut {
			e.decidePublicationVerification(w, r, acct)
			return
		}
		if !personalAccess(w, r, acct, e.store, true) {
			return
		}
		principal, ok := httpkit.PrincipalFrom(r)
		if !ok || principal.Key == nil {
			respond(w, nil, state.ErrNotFound)
			return
		}
		var in api.ToolPublicationVerificationRequest
		if !toolpublication.ValidID(r.PathValue("id")) || httpkit.DecodeJSONSized(r, &in, api.ToolPublicationVerificationRequestMaxBytes) != nil || !toolgrant.ValidResourceID(in.DeploymentID) || !toolpublication.ValidID(in.CatalogSHA256) {
			respond(w, nil, state.ErrInvalidArgument)
			return
		}
		out, err := e.requestPublicationVerification(r, acct, principal.Key.ID, in)
		if errors.Is(err, errPublicationVerificationCapacity) {
			problem := api.NewProblem(429, "publication_verification_capacity", "Verification workers busy", "retry the same candidate verification request")
			delay := int64(2)
			problem.RetryAfterSeconds = &delay
			api.WriteProblem(w, problem)
			return
		}
		respond(w, out, err)
	}
}

func (e *PrivateExecutor) publicationChange(ctx context.Context, account, id, key, action string, in state.ToolPublicationVerificationChange) (state.ToolPublicationVerificationResult, error) {
	store, ok := e.store.(state.ToolPublicationVerificationStore)
	if !ok {
		return state.ToolPublicationVerificationResult{}, state.ErrNotFound
	}
	return store.ChangeToolPublicationVerification(ctx, account, id, key, action, in)
}

func (e *PrivateExecutor) decidePublicationVerification(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !connectionOwnerAccess(w, r, acct, e.store) {
		return
	}
	var in api.ToolPublicationVerificationDecision
	if !toolpublication.ValidID(r.PathValue("id")) || httpkit.DecodeJSONSized(r, &in, api.ToolPublicationIntentMaxBytes) != nil || !toolpublication.ValidID(in.RequestSHA256) || in.Revoke && (in.CredentialSecretRef != "" || in.CredentialConnectionID != "") {
		respond(w, nil, state.ErrInvalidArgument)
		return
	}
	action := "approve"
	if in.Revoke {
		action = "revoke"
	}
	out, err := e.publicationChange(r.Context(), acct.ID, r.PathValue("id"), "", action, state.ToolPublicationVerificationChange{Decision: &in})
	if errors.Is(err, state.ErrToolOAuthBusy) || errors.Is(err, state.ErrToolOAuthReconnect) {
		connectionRespond(w, nil, err)
		return
	}
	if err == nil && e.opts.Audit != nil {
		e.opts.Audit(r.Context(), "tool_publication.verification."+action, acct.ID, map[string]any{"publication_id": r.PathValue("id"), "request_sha256": in.RequestSHA256, "deployment_id": out.Verification.DeploymentID})
	}
	respond(w, out.Verification, err)
}

func verificationFixtures(p api.ToolPublicationRecord, raw string) (mcphosting.SmokeSuite, error) {
	if !p.Intent.PlatformVerification || p.Intent.Auth == nil || toolpublication.Digest([]byte(raw)) != p.Intent.FixturesSHA256 {
		return mcphosting.SmokeSuite{}, state.ErrConflict
	}
	suite, err := mcphosting.LoadSmokeSuite(strings.NewReader(raw))
	if err != nil || !suite.RequiresToolExecution() {
		return suite, state.ErrInvalidArgument
	}
	names := []string{}
	for _, c := range suite.Cases {
		if c.Kind == "tool" {
			if _, ok := p.Intent.ToolScopes[c.Target]; !ok {
				return suite, state.ErrInvalidArgument
			}
		}
		names = append(names, c.Name)
	}
	if !slices.Equal(names, p.Intent.TestCases) {
		return suite, state.ErrConflict
	}
	return suite, nil
}

func (e *PrivateExecutor) requestPublicationVerification(r *http.Request, acct state.Account, key string, in api.ToolPublicationVerificationRequest) (api.ToolPublicationVerification, error) {
	journal, ok := e.store.(state.ToolPublicationStore)
	if !ok {
		return api.ToolPublicationVerification{}, state.ErrNotFound
	}
	p, err := journal.GetToolPublication(r.Context(), acct.ID, r.PathValue("id"))
	if err != nil {
		return api.ToolPublicationVerification{}, err
	}
	suite, err := verificationFixtures(p, in.FixturesJSON)
	if err != nil {
		return api.ToolPublicationVerification{}, err
	}
	if p.Verification != nil {
		if p.Verification.KeyID != key || p.Verification.DeploymentID != in.DeploymentID || p.Verification.CatalogSHA256 != in.CatalogSHA256 {
			return api.ToolPublicationVerification{}, state.ErrConflict
		}
		if p.Verification.Status != "approved" {
			return *p.Verification, nil
		}
	} else {
		if p.Status != "running" || !toolpublication.ValidID(in.CatalogSHA256) {
			return api.ToolPublicationVerification{}, state.ErrConflict
		}
		dep, err := e.store.DeploymentByID(r.Context(), in.DeploymentID)
		if err != nil {
			return api.ToolPublicationVerification{}, err
		}
		app, err := e.store.AppByID(r.Context(), dep.AppID)
		if err != nil || !personalApp(r, acct, e.store, app) || app.Slug != p.Intent.App || p.Intent.AppID != "" && app.ID != p.Intent.AppID {
			return api.ToolPublicationVerification{}, state.ErrNotFound
		}
		v := api.ToolPublicationVerification{PublicationID: p.ID, KeyID: key, AppID: app.ID, DeploymentID: dep.ID, DeploymentSourceSHA256: dep.SourceSHA256, CatalogSHA256: in.CatalogSHA256}
		v.RequestSHA256 = toolpublication.VerificationDigest(v, p.Intent)
		out, err := e.publicationChange(r.Context(), acct.ID, p.ID, key, "review", state.ToolPublicationVerificationChange{Review: &v})
		return out.Verification, err
	}
	release, err := e.reserve()
	if err != nil {
		return api.ToolPublicationVerification{}, errPublicationVerificationCapacity
	}
	out, err := e.publicationChange(r.Context(), acct.ID, p.ID, key, "begin", state.ToolPublicationVerificationChange{})
	if err != nil || !out.Created {
		release()
		return out.Verification, err
	}
	e.launchPublicationVerification(acct.ID, p, out.Verification, suite, release) //nolint:contextcheck // Accepted verification uses the API-owned bounded context and survives request cancellation.
	return out.Verification, nil
}

func (e *PrivateExecutor) launchPublicationVerification(account string, p api.ToolPublicationRecord, v api.ToolPublicationVerification, suite mcphosting.SmokeSuite, release func()) {
	ctx, cancel := context.WithDeadline(e.ctx, v.Deadline)
	go func() {
		defer release()
		defer cancel()
		e.runPublicationVerification(ctx, account, p, v, suite)
	}()
}

type publicationVerificationTransport struct {
	executor     *PrivateExecutor
	account      string
	verification api.ToolPublicationVerification
	next         http.RoundTripper
}

func (t publicationVerificationTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	a, err := t.executor.store.AccountByID(r.Context(), t.account)
	if err != nil || !a.Active() {
		return nil, state.ErrNotFound
	}
	_, err = t.executor.publicationChange(r.Context(), t.account, t.verification.PublicationID, t.verification.KeyID, "dispatch", state.ToolPublicationVerificationChange{})
	if err != nil {
		return nil, err
	}
	return t.next.RoundTrip(r)
}

func (e *PrivateExecutor) runPublicationVerification(ctx context.Context, account string, p api.ToolPublicationRecord, v api.ToolPublicationVerification, suite mcphosting.SmokeSuite) {
	outcome := api.ToolPublicationVerificationOutcome{Code: "credential_unavailable", Checks: []api.ToolPublicationCheck{}}
	defer func() {
		persist, cancel := context.WithTimeout(context.WithoutCancel(ctx), api.ToolOAuthPersistTimeout)
		defer cancel()
		_, _ = e.publicationChange(persist, account, p.ID, v.KeyID, "finish", state.ToolPublicationVerificationChange{Outcome: &outcome})
	}()
	access, sensitive, err := e.publicationCredential(ctx, account, v)
	if err != nil {
		if errors.Is(err, state.ErrToolOAuthReconnect) {
			outcome.Code = "reconnect_required"
		}
		return
	}
	token := access.token
	if access.expiresAt != nil {
		var cancel context.CancelFunc
		ctx, cancel = context.WithDeadline(ctx, *access.expiresAt)
		defer cancel()
	}
	outcome.Code = "candidate_unavailable"
	app, err := e.store.AppByID(ctx, v.AppID)
	if err != nil {
		return
	}
	dep, err := e.store.DeploymentByID(ctx, v.DeploymentID)
	if err != nil {
		return
	}
	endpoint, err := e.opts.Endpoint(ctx, app, dep)
	if err != nil {
		return
	}
	endpoint = strings.TrimRight(endpoint, "/") + p.Intent.EndpointPath
	target := api.ToolInvocationTarget{App: p.Intent.App, AppID: v.AppID, DeploymentID: v.DeploymentID, DeploymentSourceSHA256: v.DeploymentSourceSHA256, CatalogSHA256: v.CatalogSHA256, EndpointPath: p.Intent.EndpointPath}
	if err = e.opts.VerifyPin(ctx, target, endpoint); err != nil {
		return
	}
	outcome.Code = "catalog_verification_failed"
	wrap := func(next http.RoundTripper) http.RoundTripper {
		if next == nil {
			next = http.DefaultTransport
		}
		return publicationVerificationTransport{executor: e, account: account, verification: v, next: next}
	}
	probe, err := mcphosting.NewClient(endpoint, token, mcphosting.ProtocolVersion)
	if err != nil {
		return
	}
	client := *e.opts.HTTP
	client.Transport = wrap(e.opts.HTTP.Transport)
	client.Timeout = 10 * time.Second
	probe.HTTP = &client
	auth := p.Intent.Auth
	probe.ExpectedAuth = &mcphosting.AuthConfig{Mode: "external-oauth", Issuer: auth.Issuer, JWKSURL: auth.JWKSURL, Resource: auth.Resource, Scopes: auth.Scopes, ToolScopes: p.Intent.ToolScopes}
	report := mcphosting.Doctor(ctx, probe, p.Intent.Legacy, "", nil)
	if !report.OK || len(report.Tools) != len(p.Intent.ToolScopes) {
		return
	}
	for _, tool := range report.Tools {
		if _, ok := p.Intent.ToolScopes[tool.Name]; !ok {
			return
		}
	}
	contract, err := mcphosting.NewCatalogContract(mcphosting.ProtocolVersion, mcphosting.Catalog{Tools: report.Tools, Capabilities: report.Capabilities, Extensions: report.Extensions, Resources: report.Resources, ResourceTemplates: report.ResourceTemplates, Prompts: report.Prompts})
	if err != nil {
		return
	}
	catalog, err := mcphosting.MarshalContract(contract)
	if err != nil || publicationCatalogSensitive(catalog, sensitive) {
		return
	}
	if len(catalog) > api.ToolPublicationCatalogMaxBytes || !publicationVerificationFits(p, v, catalog) {
		outcome.Code = "publication_evidence_too_large"
		return
	}
	outcome.Code = "protocol_verification_failed"
	if err = e.opts.PublicationProtocols(ctx, target, endpoint, token, contract, p.Intent.Legacy, wrap); err != nil {
		return
	}
	outcome.Code = "behavioral_fixtures_failed"
	smoke := mcphosting.RunSmoke(ctx, probe, suite, true)
	for _, c := range smoke.Checks {
		outcome.Checks = append(outcome.Checks, api.ToolPublicationCheck{Name: c.Name, Status: c.Status})
	}
	if !smoke.OK {
		return
	}
	outcome.Code = "catalog_integrity_failed"
	if err = e.opts.VerifyPin(ctx, target, endpoint); err != nil {
		return
	}
	checks := []api.ToolPublicationCheck{}
	for _, c := range smoke.Checks {
		checks = append(checks, api.ToolPublicationCheck{Name: c.Name, Status: c.Status})
	}
	outcome = api.ToolPublicationVerificationOutcome{OK: true, ProtocolVerified: true, Catalog: catalog, Checks: checks}
}

// The journal's SQL payload ceiling is the combined outcome and catalog budgets.
// Reserve both retained catalogs and both check lists before running fixtures.
// Indented JSON conservatively includes PostgreSQL's JSONB separator spacing.
func publicationVerificationFits(p api.ToolPublicationRecord, v api.ToolPublicationVerification, catalog []byte) bool {
	checks := make([]api.ToolPublicationCheck, 0, len(p.Intent.TestCases))
	for _, name := range p.Intent.TestCases {
		checks = append(checks, api.ToolPublicationCheck{Name: name, Status: "passed"})
	}
	v.Status, v.Outcome = "completed", &api.ToolPublicationVerificationOutcome{OK: true, ProtocolVerified: true, Catalog: catalog, Checks: checks}
	p.Status, p.Verification, p.DeploymentSourceSHA256 = "completed", &v, v.DeploymentSourceSHA256
	p.Outcome = &api.ToolPublicationOutcome{Status: "completed", DeploymentID: v.DeploymentID, CatalogSHA256: v.CatalogSHA256, Catalog: catalog, ProtocolVerified: true, Checks: checks}
	data, err := json.MarshalIndent(p, "", " ")
	return err == nil && len(data) <= api.ToolPublicationOutcomeMaxBytes+api.ToolPublicationCatalogMaxBytes
}
