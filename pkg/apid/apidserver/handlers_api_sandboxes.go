package apidserver

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apisandbox"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

const (
	apiSandboxLease            = 24 * time.Hour
	apiSandboxConsumerRef      = "gregale-api-sandbox-invites"
	apiSandboxInviteNamePrefix = "invite-"
)

func apiSandboxSlug(id string) string {
	return api.APISandboxSlugPrefix + strings.ReplaceAll(id, "-", "")
}

func apiSandboxPathID(r *http.Request, name string) string {
	raw := r.PathValue(name)
	if id, err := uuid.Parse(raw); err == nil && id != uuid.Nil && id.String() == raw {
		return raw
	}
	return ""
}

func (s *server) apiSandboxParent(w http.ResponseWriter, r *http.Request, acct state.Account) (state.App, string, bool) {
	parent, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return state.App{}, "", false
	}
	sourceID := apiSandboxPathID(r, "deployment")
	if sourceID == "" || parent.PreviewOfSlug != "" {
		s.notFound(w, "no such API release")
		return state.App{}, "", false
	}
	return parent, sourceID, true
}

func apiSandboxMatches(app, parent state.App, sourceID string) bool {
	return state.IsAPISandbox(app) && !app.Manifest.APISandboxRun && app.AccountID == parent.AccountID && app.OrgID == parent.OrgID &&
		app.PreviewOfSlug == state.APISandboxPreviewParent(parent.Slug) && app.Manifest.APISandboxSourceDeploymentID == sourceID
}

func (s *server) createAPISandbox(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	parent, sourceID, ok := s.apiSandboxParent(w, r, acct)
	if !ok {
		return
	}
	id := apiSandboxPathID(r, "sandbox")
	if id == "" {
		api.WriteProblem(w, api.ErrValidation("use a lowercase UUID for the sandbox ID"))
		return
	}
	operations, ok := s.store.(state.APISandboxOperationStore)
	if !ok || !s.apiSandboxWorkerEnabled() {
		api.WriteProblem(w, api.ErrCapacity("API sandbox provisioning is unavailable"))
		return
	}
	if !s.allowAPISandboxProvisioning(w, r, acct, parent) {
		return
	}
	ctx := r.Context()
	var request *api.APISandboxCreateRequest
	bodyErr := decodeJSONSized(r, &request, 4096)
	if errors.Is(bodyErr, io.EOF) {
		request = &api.APISandboxCreateRequest{}
	} else if bodyErr != nil || request == nil {
		api.WriteProblem(w, api.ErrValidation("expected a JSON object with optional smoke_path, scenario_revision, and partner_label"))
		return
	}
	if !apisandbox.ValidSmokePath(request.SmokePath) {
		api.WriteProblem(w, api.ErrValidation("smoke_path must start with / and contain no query or fragment"))
		return
	}
	if request.ScenarioRevision != "" {
		if parsed, err := uuid.Parse(request.ScenarioRevision); err != nil || parsed == uuid.Nil || parsed.String() != request.ScenarioRevision {
			api.WriteProblem(w, api.ErrValidation("use a lowercase UUID for scenario_revision"))
			return
		}
	}
	if request.PartnerLabel != "" {
		request.PartnerLabel = strings.TrimSpace(request.PartnerLabel)
		if !apisandbox.ValidPartnerLabel(request.PartnerLabel) || request.ScenarioRevision == "" {
			api.WriteProblem(w, api.ErrValidation("use a partner label of up to 100 characters and select a saved scenario"))
			return
		}
		if _, ok := s.store.(state.APISandboxPartnerStore); !ok {
			api.WriteProblem(w, api.ErrCapacity("partner sandboxes are unavailable"))
			return
		}
	}
	slug := apiSandboxSlug(id)
	app, err := s.store.AppBySlugIncludingDeleted(ctx, slug)
	status := http.StatusOK
	if errors.Is(err, state.ErrNotFound) {
		source, found := s.readAPISandboxSource(w, r, parent, sourceID)
		if !found || !supportedAPISandboxSource(w, parent, source) {
			return
		}
		var scenario *api.APISandboxScenario
		if request.ScenarioRevision != "" {
			var scenarioError error
			scenario, scenarioError = s.selectedAPISandboxScenario(ctx, parent.ID, sourceID, request.ScenarioRevision)
			if scenarioError != nil {
				api.WriteProblem(w, api.ErrCapacity("could not read the release scenario"))
				return
			}
			if scenario == nil || scenario.Revision != request.ScenarioRevision {
				api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Sandbox scenario changed", "reload the current scenario before creating a copy"))
				return
			}
		}
		candidate, problem := s.buildAPISandboxApp(acct, parent, sourceID, slug)
		if problem != nil {
			api.WriteProblem(w, problem)
			return
		}
		candidate.APISandboxSmokePath = request.SmokePath
		candidate.APISandboxScenario = scenario
		candidate.APISandboxPartnerLabel = request.PartnerLabel
		candidate.Manifest.APISandboxPartner = request.PartnerLabel != ""
		app, err = s.store.CreateAppIfUnderQuota(ctx, candidate, api.MustLimitsFor(acct.Plan))
		if err == nil {
			status = http.StatusAccepted
			s.emitAppCreated(ctx, app)
			s.audit.Emit(ctx, "api_sandbox.created", &acct.ID, map[string]any{"app_id": app.ID, "source_deployment_id": sourceID})
		} else if errors.Is(err, state.ErrConflict) {
			app, err = s.store.AppBySlugIncludingDeleted(ctx, slug)
		}
	}
	if writeAPISandboxQuota(w, acct, err) {
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not provision the sandbox"))
		return
	}
	if !apiSandboxMatches(app, parent, sourceID) {
		s.notFound(w, "no such sandbox for this release")
		return
	}
	if !state.APISandboxAcceptsRequests(app, time.Now()) {
		api.WriteProblem(w, api.NewProblem(http.StatusGone, api.CodeNotFound, "Sandbox ended", "create a new sandbox with a new ID"))
		return
	}
	op, err := operations.GetAPISandboxOperation(ctx, app.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not read sandbox provisioning"))
		return
	}
	var scenarioRevision string
	if op.Scenario != nil {
		scenarioRevision = op.Scenario.Revision
	}
	receipt, err := s.apiSandboxOwnerResponse(ctx, app, op)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not check sandbox readiness"))
		return
	}
	var partnerLabel string
	if receipt.Partner != nil {
		partnerLabel = receipt.Partner.Label
	}
	if bodyErr == nil && (op.SmokePath != request.SmokePath || scenarioRevision != request.ScenarioRevision || partnerLabel != request.PartnerLabel) {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Sandbox already configured", "reuse the original sandbox configuration or use a new sandbox ID"))
		return
	}
	writeJSON(w, status, receipt)
}

func (s *server) buildAPISandboxApp(acct state.Account, parent state.App, sourceID, slug string) (state.App, *api.Problem) {
	candidate, problem := s.buildApp(acct, api.CreateAppRequest{Slug: parent.Slug, RAMMB: parent.RAMMB}, api.MustLimitsFor(acct.Plan))
	if problem != nil {
		return state.App{}, problem
	}
	candidate.Slug = slug
	candidate.OrgID = parent.OrgID
	candidate.PreviewOfSlug = state.APISandboxPreviewParent(parent.Slug)
	candidate.PreviewPrState = state.PreviewPrStateOpen
	expiresAt := time.Now().UTC().Add(apiSandboxLease)
	candidate.PreviewExpiresAt = &expiresAt
	candidate.RequireAuthn = false
	candidate.PublicAuthMode = api.AppPublicAuthModeOpen
	candidate.ConsumerAuthMode = api.ConsumerAuthModeRequired
	candidate.StartCommand = parent.StartCommand
	candidate.Manifest.APISandboxSourceDeploymentID = sourceID
	candidate.Manifest.Entrypoint = parent.Manifest.Entrypoint
	candidate.Manifest.Port = parent.Manifest.Port
	candidate.Manifest.WorkingDir = parent.Manifest.WorkingDir
	candidate.Manifest.User = parent.Manifest.User
	candidate.Manifest.Healthz = parent.Manifest.Healthz
	candidate.Manifest.HealthPath = parent.Manifest.HealthPath
	candidate.Manifest.StartupDeadlineS = parent.Manifest.StartupDeadlineS
	candidate.Manifest.BeforeCheckpoint = parent.Manifest.BeforeCheckpoint
	candidate.Manifest.AfterRestore = parent.Manifest.AfterRestore
	return candidate, nil
}

func writeAPISandboxQuota(w http.ResponseWriter, acct state.Account, err error) bool {
	var quota *state.QuotaError
	if !errors.As(err, &quota) {
		return false
	}
	limits := api.MustLimitsFor(acct.Plan)
	if quota.Kind == state.QuotaErrorKindDeveloperApps {
		api.WriteProblem(w, api.ErrPlanLimitDeveloperApps(limits, quota.Observed))
	} else {
		api.WriteProblem(w, api.ErrPlanLimitApps(limits, quota.Observed))
	}
	return true
}

func (s *server) allowAPISandboxProvisioning(w http.ResponseWriter, r *http.Request, acct state.Account, parent state.App) bool {
	if !s.consumerFeatureAllowed(w, acct) {
		return false
	}
	pgLimits, _ := api.ManagedPostgresLimitsFor(acct.Plan)
	if managedPostgresNotInPlan(acct.Plan) || !pgLimits.DevelopmentAllowed {
		managedPostgresProblem(w, managedpostgres.ErrNotInPlan)
		return false
	}
	ctx := r.Context()
	if parent.OrgID != "" {
		org, err := s.store.OrgByPersonalAccount(ctx, acct.ID)
		if err != nil && !errors.Is(err, state.ErrNotFound) {
			api.WriteProblem(w, api.ErrCapacity("could not check the release workspace"))
			return false
		}
		if org.ID != parent.OrgID {
			api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Personal workspace required", "API sandboxes currently use personal managed PostgreSQL"))
			return false
		}
	}
	return true
}

func (s *server) readAPISandboxSource(w http.ResponseWriter, r *http.Request, parent state.App, sourceID string) (state.Deployment, bool) {
	source, err := s.store.DeploymentByID(r.Context(), sourceID)
	if errors.Is(err, state.ErrNotFound) || (err == nil && source.AppID != parent.ID) {
		s.notFound(w, "no such API release")
		return state.Deployment{}, false
	}
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not read the API release"))
		return state.Deployment{}, false
	}
	return source, true
}

func apiSandboxHTTPSourceApp(parent state.App) bool {
	return parent.Type == state.AppTypeApp && (parent.WorkloadClass == "" || parent.WorkloadClass == state.WorkloadClassHTTP) &&
		(parent.AppProtocol == "" || parent.AppProtocol == "http1")
}

func apiSandboxSourceEligible(parent state.App, source state.Deployment) bool {
	return apiSandboxHTTPSourceApp(parent) && state.APISandboxSourceSupported(source)
}

func supportedAPISandboxSource(w http.ResponseWriter, parent state.App, source state.Deployment) bool {
	if !apiSandboxSourceEligible(parent, source) {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Release does not support API sandboxes",
			"choose a retained single-process HTTP release with a release command that initializes a fresh PostgreSQL database"))
		return false
	}
	return true
}

func (s *server) apiSandboxResponse(ctx context.Context, app state.App, op state.APISandboxOperation) (api.APISandboxResponse, error) {
	appURL := appURLForDomain(app.Slug, s.domain)
	hex := strings.TrimPrefix(app.Slug, api.APISandboxSlugPrefix)
	id, _ := uuid.Parse(hex)
	receipt := api.APISandboxResponse{
		ID: id.String(), SourceDeploymentID: app.Manifest.APISandboxSourceDeploymentID,
		DeploymentID: op.DeploymentID, Status: op.Phase, URL: appURL, OpenAPIURL: appURL + apisandbox.OpenAPIPath,
		ExpiresAt: *app.PreviewExpiresAt, SmokePath: op.SmokePath, Failure: op.Failure,
		Verification: op.Verification, Scenario: op.Scenario.Receipt(),
	}
	switch {
	case app.PreviewPrState == state.PreviewPrStateTearingDown:
		receipt.Status = "tearing_down"
	case !state.APISandboxAcceptsRequests(app, time.Now()):
		receipt.Status = "ended"
	case op.Phase == state.APISandboxLive:
		deployment, err := s.store.LatestDeployment(ctx, app.ID)
		if err != nil && !errors.Is(err, state.ErrNotFound) {
			return api.APISandboxResponse{}, err
		}
		if !apiSandboxVerifiedDeployment(op, deployment) {
			receipt.Status = state.APISandboxFailed
			receipt.Verification = nil
			receipt.Failure = &api.APISandboxFailure{Code: "release_unavailable", Detail: "The verified sandbox release is no longer live. Create a new sandbox."}
		}
	}
	return receipt, nil
}

func apiSandboxVerifiedDeployment(op state.APISandboxOperation, deployment state.Deployment) bool {
	return op.Phase == state.APISandboxLive && op.Verification != nil && op.DeploymentID != "" &&
		op.Verification.DeploymentID == op.DeploymentID && op.DeploymentID == deployment.ID && deployment.Status == state.DeployLive
}

func (s *server) listAPISandboxes(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	parent, sourceID, ok := s.apiSandboxParent(w, r, acct)
	if !ok {
		return
	}
	operations, ok := s.store.(state.APISandboxOperationStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("API sandbox provisioning is unavailable"))
		return
	}
	rows, err := s.store.ListPreviewsForAccount(r.Context(), acct.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not list API sandboxes"))
		return
	}
	out := api.APISandboxListResponse{Sandboxes: []api.APISandboxResponse{}}
	for _, app := range rows {
		if !apiSandboxMatches(app, parent, sourceID) || app.PreviewExpiresAt == nil {
			continue
		}
		op, err := operations.GetAPISandboxOperation(r.Context(), app.ID)
		if errors.Is(err, state.ErrNotFound) && !state.APISandboxAcceptsRequests(app, time.Now()) {
			continue
		}
		if err != nil {
			api.WriteProblem(w, api.ErrCapacity("could not read sandbox provisioning"))
			return
		}
		receipt, err := s.apiSandboxOwnerResponse(r.Context(), app, op)
		if err != nil {
			api.WriteProblem(w, api.ErrCapacity("could not check a sandbox release"))
			return
		}
		out.Sandboxes = append(out.Sandboxes, receipt)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) loadAPISandbox(w http.ResponseWriter, r *http.Request, acct state.Account) (state.App, bool) {
	parent, sourceID, ok := s.apiSandboxParent(w, r, acct)
	if !ok {
		return state.App{}, false
	}
	id := apiSandboxPathID(r, "sandbox")
	if id == "" {
		s.notFound(w, "no such sandbox")
		return state.App{}, false
	}
	app, err := s.store.AppBySlug(r.Context(), apiSandboxSlug(id))
	if errors.Is(err, state.ErrNotFound) || (err == nil && !apiSandboxMatches(app, parent, sourceID)) {
		s.notFound(w, "no such sandbox")
		return state.App{}, false
	}
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not check the sandbox"))
		return state.App{}, false
	}
	return app, true
}

func (s *server) endAPISandbox(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadAPISandbox(w, r, acct)
	if !ok {
		return
	}
	// Close ingress before attempting providers' cleanup, which can be retried.
	if app.PreviewPrState != state.PreviewPrStateTearingDown {
		if _, err := s.store.SetPreviewPrState(r.Context(), app.ID, state.PreviewPrStateTearingDown); err != nil {
			api.WriteProblem(w, api.ErrCapacity("could not revoke sandbox access"))
			return
		}
	}
	consumers, err := s.store.ListAPIConsumersForApp(r.Context(), acct.ID, app.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not revoke sandbox invitations"))
		return
	}
	for _, consumer := range consumers {
		if consumer.Active() {
			if _, err := s.store.RevokeAPIConsumer(r.Context(), acct.ID, consumer.ID); err != nil {
				api.WriteProblem(w, api.ErrCapacity("could not revoke sandbox invitations"))
				return
			}
		}
	}
	if err := s.checkAPISandboxCleanup(r.Context(), app); errors.Is(err, state.ErrConflict) {
		// The lease janitor will finish cleanup after the worker settles.
		w.WriteHeader(http.StatusNoContent)
		return
	} else if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not check sandbox cleanup"))
		return
	}
	s.destroyPreviewApp(w, r, acct, app, "api_sandbox.ended")
}

func (s *server) createAPISandboxInvite(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	app, ok := s.loadAPISandbox(w, r, acct)
	if !ok {
		return
	}
	if app.Manifest.APISandboxPartner {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Private partner sandbox", "issue a one-time partner invite for this copy"))
		return
	}
	ctx := r.Context()
	receipt, err := s.getAPISandboxReceipt(ctx, app)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not check sandbox readiness"))
		return
	}
	if receipt.Status != state.APISandboxLive {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Sandbox is not ready", "wait for the sandbox API checks to pass"))
		return
	}
	if !s.consumerFeatureAllowed(w, acct) {
		return
	}
	consumer, err := s.apiSandboxConsumer(ctx, app, apiSandboxConsumerRef, "API sandbox invitations")
	if err != nil || consumer.ID == "" {
		api.WriteProblem(w, api.ErrCapacity("could not prepare the sandbox invite"))
		return
	}
	_, token, problem := s.issueConsumerKey(ctx, acct, consumer, apiSandboxInviteNamePrefix+uuid.NewString(), []string{"read", "write"}, app.PreviewExpiresAt)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	writeJSON(w, http.StatusCreated, api.APISandboxInviteResponse{Sandbox: receipt, Token: token})
}

func (s *server) readAPISandboxInvite(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	id := apiSandboxPathID(r, "sandbox")
	token, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if id != "" && api.ValidConsumerKeyFormat(token) {
		if sandbox, ok := s.authorizedAPISandboxInvite(r, id, token); ok {
			writeJSON(w, http.StatusOK, sandbox)
			return
		}
	}
	api.WriteProblem(w, api.NewProblem(http.StatusUnauthorized, api.CodeUnauthorized,
		"Sandbox invite unavailable", "ask the owner for an active invitation"))
}

func (s *server) authorizedAPISandboxInvite(r *http.Request, id, token string) (api.APISandboxResponse, bool) {
	ctx := r.Context()
	app, err := s.store.AppBySlug(ctx, apiSandboxSlug(id))
	now := time.Now()
	if err != nil || (app.Manifest.APISandboxRun && !app.Manifest.APISandboxPartner) || !state.APISandboxAcceptsRequests(app, now) {
		return api.APISandboxResponse{}, false
	}
	prefix, _, _ := strings.Cut(strings.TrimPrefix(token, api.ConsumerKeyPrefix), "_")
	key, err := s.store.ConsumerKeyByAppAndPrefix(ctx, app.AccountID, app.ID, prefix)
	if err != nil || !key.Active(now) ||
		!state.CredentialMatches(state.CredentialColumnConsumerKey, api.HashConsumerKey(token), key.Hash) {
		return api.APISandboxResponse{}, false
	}
	consumer, err := s.store.GetAPIConsumerByID(ctx, app.AccountID, key.ConsumerID)
	if err != nil || consumer.AppID != app.ID || !consumer.Active() {
		return api.APISandboxResponse{}, false
	}
	if app.Manifest.APISandboxPartner {
		store, ok := s.store.(state.APISandboxPartnerStore)
		if !ok || consumer.ExternalRef != api.APISandboxPartnerConsumerRef {
			return api.APISandboxResponse{}, false
		}
		partner, err := store.GetAPISandboxPartner(ctx, app.ID)
		if err != nil || !partner.ActiveKey(key.ID) {
			return api.APISandboxResponse{}, false
		}
	} else if consumer.ExternalRef != apiSandboxConsumerRef || !strings.HasPrefix(key.Name, apiSandboxInviteNamePrefix) {
		return api.APISandboxResponse{}, false
	}
	receipt, err := s.getAPISandboxReceipt(ctx, app)
	if err != nil || receipt.Status != state.APISandboxLive {
		return api.APISandboxResponse{}, false
	}
	if app.Manifest.APISandboxRun {
		runs, ok := s.store.(state.APISandboxRunStore)
		if !ok {
			return api.APISandboxResponse{}, false
		}
		run, err := runs.GetAPISandboxRun(ctx, id)
		if err != nil || run.AppID != app.ID || run.Mode != api.APISandboxRunModePartner || run.SandboxDeploymentID != receipt.DeploymentID {
			return api.APISandboxResponse{}, false
		}
		receipt.PartnerStepCount = run.PartnerStepCount
	}
	return receipt, true
}

func (s *server) getAPISandboxReceipt(ctx context.Context, app state.App) (api.APISandboxResponse, error) {
	operations, ok := s.store.(state.APISandboxOperationStore)
	if !ok {
		return api.APISandboxResponse{}, state.ErrNotFound
	}
	op, err := operations.GetAPISandboxOperation(ctx, app.ID)
	if err != nil {
		return api.APISandboxResponse{}, err
	}
	return s.apiSandboxResponse(ctx, app, op)
}

func (s *server) apiSandboxConsumer(ctx context.Context, app state.App, ref, name string) (state.APIConsumer, error) {
	consumer, err := s.store.CreateAPIConsumer(ctx, app.AccountID, app.ID, ref, name)
	if !errors.Is(err, state.ErrConflict) {
		return consumer, err
	}
	consumers, err := s.store.ListAPIConsumersForApp(ctx, app.AccountID, app.ID)
	if err != nil {
		return state.APIConsumer{}, err
	}
	for _, existing := range consumers {
		if existing.ExternalRef == ref && existing.Active() {
			return existing, nil
		}
	}
	return state.APIConsumer{}, state.ErrConflict
}

func (s *server) checkAPISandboxCleanup(ctx context.Context, app state.App) error {
	operations, ok := s.store.(state.APISandboxOperationStore)
	if !ok || !state.IsAPISandbox(app) {
		return nil
	}
	op, err := operations.GetAPISandboxOperation(ctx, app.ID)
	if errors.Is(err, state.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if op.LeaseUntil != nil && time.Now().Before(*op.LeaseUntil) {
		return state.ErrConflict
	}
	return nil
}
