package apidserver

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

const apiSandboxRunReportTTL = 24 * time.Hour

func (s *server) apiSandboxRunRelease(w http.ResponseWriter, r *http.Request, acct state.Account) (state.App, state.Deployment, state.APISandboxRunStore, bool) {
	w.Header().Set("Cache-Control", "no-store")
	parent, sourceID, ok := s.apiSandboxParent(w, r, acct)
	if !ok {
		return state.App{}, state.Deployment{}, nil, false
	}
	source, ok := s.readAPISandboxSource(w, r, parent, sourceID)
	if !ok {
		return state.App{}, state.Deployment{}, nil, false
	}
	store, ok := s.store.(state.APISandboxRunStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("scenario runs are unavailable"))
	}
	return parent, source, store, ok
}

func (s *server) createAPISandboxRun(w http.ResponseWriter, r *http.Request, acct state.Account) {
	parent, source, store, ok := s.apiSandboxRunRelease(w, r, acct)
	if !ok {
		return
	}
	id := apiSandboxPathID(r, "run")
	var input api.APISandboxRunRequest
	if id == "" || decodeJSONSized(r, &input, 4096) != nil {
		api.WriteProblem(w, api.ErrValidation("use a lowercase UUID for the run ID and supply scenario_revision"))
		return
	}
	if rev, err := uuid.Parse(input.ScenarioRevision); err != nil || rev == uuid.Nil || rev.String() != input.ScenarioRevision {
		api.WriteProblem(w, api.ErrValidation("use the saved scenario_revision UUID"))
		return
	}
	if input.Mode == "" {
		input.Mode = api.APISandboxRunModeAutomated
	}
	if input.Mode != api.APISandboxRunModeAutomated && input.Mode != api.APISandboxRunModePartner {
		api.WriteProblem(w, api.ErrValidation("run mode must be automated or partner"))
		return
	}
	ctx := r.Context()
	if existing, err := store.GetAPISandboxRun(ctx, id); err == nil {
		s.writeExistingAPISandboxRun(w, existing, source.ID, input)
		return
	} else if !errors.Is(err, state.ErrNotFound) {
		api.WriteProblem(w, api.ErrCapacity("could not check an existing scenario run"))
		return
	}
	if !s.apiSandboxWorkerEnabled() {
		api.WriteProblem(w, api.ErrCapacity("scenario provisioning is unavailable"))
		return
	}
	if !s.allowAPISandboxProvisioning(w, r, acct, parent) || !supportedAPISandboxSource(w, parent, source) {
		return
	}
	scenario, err := s.selectedAPISandboxScenario(ctx, parent.ID, source.ID, input.ScenarioRevision)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not read the release scenario"))
		return
	}
	if scenario == nil || scenario.Revision != input.ScenarioRevision {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Sandbox scenario changed", "reload the saved scenario before starting a new run"))
		return
	}
	if input.Mode == api.APISandboxRunModePartner {
		if scenario.StripeWebhook != nil || scenario.OutboundWebhook != nil {
			api.WriteProblem(w, api.ErrValidation("use an automated run for a webhook scenario"))
			return
		}
		if detail := partnerScenarioValidation(scenario.Requests); detail != "" {
			api.WriteProblem(w, api.ErrValidation(detail))
			return
		}
	}
	if input.PartnerWebhook {
		if input.Mode != api.APISandboxRunModeAutomated || scenario.OutboundWebhook == nil {
			api.WriteProblem(w, api.ErrValidation("a partner webhook receiver requires an automated outbound webhook scenario"))
			return
		}
		partners, ok := s.store.(state.APISandboxWebhookPartnerStore)
		if !ok {
			api.WriteProblem(w, api.ErrCapacity("sandbox webhook partners are unavailable"))
			return
		}
		p, err := partners.GetAPISandboxWebhookPartner(ctx, id)
		if err != nil && !errors.Is(err, state.ErrNotFound) {
			api.WriteProblem(w, api.ErrCapacity("could not read the partner receiver"))
			return
		}
		if !p.ReadyFor(source.ID, scenario.Revision, time.Now()) {
			api.WriteProblem(w, sandboxReceiverConflict("ask the partner to verify their receiver and confirm readiness before starting the run"))
			return
		}
	}
	app, problem := s.buildAPISandboxApp(acct, parent, source.ID, apiSandboxSlug(id))
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	app.APISandboxRunID = id
	app.APISandboxRunPartnerWebhook = input.PartnerWebhook
	app.APISandboxScenario = scenario
	app.Manifest.APISandboxRun = true
	if input.Mode == api.APISandboxRunModePartner {
		app.Manifest.APISandboxPartner = true
		app.APISandboxPartnerLabel = "Scenario partner"
	}
	created, err := s.store.CreateAppIfUnderQuota(ctx, app, api.MustLimitsFor(acct.Plan))
	if errors.Is(err, state.ErrConflict) {
		existing, readErr := store.GetAPISandboxRun(ctx, id)
		if readErr == nil {
			s.writeExistingAPISandboxRun(w, existing, source.ID, input)
			return
		}
		if errors.Is(readErr, state.ErrNotFound) {
			api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Scenario run ID is in use", "use a new run ID"))
			return
		}
	}
	if writeAPISandboxQuota(w, acct, err) {
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not reserve a fresh scenario sandbox"))
		return
	}
	s.emitAppCreated(ctx, created)
	s.audit.Emit(ctx, "api_sandbox.run_created", &acct.ID, map[string]any{"app_id": created.ID, "run_id": id})
	run, err := store.GetAPISandboxRun(ctx, id)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not read the queued scenario run"))
		return
	}
	writeJSON(w, http.StatusAccepted, run.APISandboxRunReport)
}

func partnerScenarioValidation(steps []api.APISandboxExampleRequest) string {
	if len(steps) < 2 {
		return "a partner run needs at least one partner request followed by a final GET with a JSON assertion"
	}
	final := steps[len(steps)-1]
	if final.Method != http.MethodGet || len(final.Assertions) == 0 {
		return "finish the scenario with a GET that has a JSON assertion"
	}
	for _, step := range steps[:len(steps)-1] {
		if len(step.Assertions) != 0 {
			return "partner requests can check HTTP status. Move JSON assertions to the final GET"
		}
	}
	return ""
}

func (s *server) issueAPISandboxRunInvite(w http.ResponseWriter, r *http.Request, acct state.Account) {
	run, _, ok := s.loadAPISandboxRun(w, r, acct)
	if !ok {
		return
	}
	acceptsInvite := run.Status == api.APISandboxRunAwaitingPartner || (!run.Terminal() && run.SandboxDeploymentID == "")
	if run.Mode != api.APISandboxRunModePartner || !acceptsInvite {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Partner invite unavailable", "start a new partner run to invite someone"))
		return
	}
	app, err := s.store.AppByID(r.Context(), run.AppID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not read the partner run sandbox"))
		return
	}
	store, ok := s.store.(state.APISandboxPartnerStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("partner sandboxes are unavailable"))
		return
	}
	s.writeAPISandboxPartnerInvite(w, r, acct, app, store)
}

func (s *server) writeExistingAPISandboxRun(w http.ResponseWriter, run state.APISandboxRun, sourceID string, input api.APISandboxRunRequest) {
	if run.SourceDeploymentID != sourceID {
		s.notFound(w, "no such scenario run for this release")
		return
	}
	if run.ScenarioRevision != input.ScenarioRevision || run.Mode != input.Mode || run.PartnerWebhook != input.PartnerWebhook {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Scenario run already configured", "retry with the original scenario revision, mode, and receiver selection or start a new run"))
		return
	}
	writeJSON(w, http.StatusOK, run.APISandboxRunReport)
}

func (s *server) listAPISandboxRuns(w http.ResponseWriter, r *http.Request, acct state.Account) {
	_, source, store, ok := s.apiSandboxRunRelease(w, r, acct)
	if !ok {
		return
	}
	runs, err := store.ListAPISandboxRuns(r.Context(), source.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not list scenario runs"))
		return
	}
	response := api.APISandboxRunsResponse{Runs: runs}
	if partners, ok := s.store.(state.APISandboxWebhookPartnerStore); ok {
		response.ReceiverInvites, err = partners.ListAPISandboxWebhookPartnerSetups(r.Context(), source.ID, time.Now().UTC())
		if err != nil {
			api.WriteProblem(w, api.ErrCapacity("could not list scenario receiver invites"))
			return
		}
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *server) loadAPISandboxRun(w http.ResponseWriter, r *http.Request, acct state.Account) (state.APISandboxRun, state.APISandboxRunStore, bool) {
	_, source, store, ok := s.apiSandboxRunRelease(w, r, acct)
	if !ok {
		return state.APISandboxRun{}, nil, false
	}
	id := apiSandboxPathID(r, "run")
	if id == "" {
		s.notFound(w, "no such scenario run")
		return state.APISandboxRun{}, nil, false
	}
	run, err := store.GetAPISandboxRun(r.Context(), id)
	if errors.Is(err, state.ErrNotFound) || (err == nil && run.SourceDeploymentID != source.ID) {
		s.notFound(w, "no such scenario run for this release")
		return state.APISandboxRun{}, nil, false
	}
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not read the scenario report"))
		return state.APISandboxRun{}, nil, false
	}
	return run, store, true
}

func (s *server) getAPISandboxRun(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if run, _, ok := s.loadAPISandboxRun(w, r, acct); ok {
		writeJSON(w, http.StatusOK, run.APISandboxRunReport)
	}
}

func (s *server) shareAPISandboxRun(w http.ResponseWriter, r *http.Request, acct state.Account) {
	run, store, ok := s.loadAPISandboxRun(w, r, acct)
	if !ok {
		return
	}
	if !run.Terminal() {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Scenario still running", "share the report after the run finishes"))
		return
	}
	var nonce [32]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		api.WriteProblem(w, api.ErrInternal("could not generate a scenario report link"))
		return
	}
	token := api.APISandboxRunSharePrefix + base64.RawURLEncoding.EncodeToString(nonce[:])
	digest := sha256.Sum256([]byte(token))
	expiresAt := time.Now().UTC().Add(apiSandboxRunReportTTL)
	if err := store.ShareAPISandboxRun(r.Context(), run.ID, digest[:], expiresAt); err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not share the scenario report"))
		return
	}
	writeJSON(w, http.StatusCreated, api.APISandboxRunShareResponse{Token: token, ExpiresAt: expiresAt})
}

func (s *server) readSharedAPISandboxRun(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	id := apiSandboxPathID(r, "run")
	token, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	encoded, valid := strings.CutPrefix(token, api.APISandboxRunSharePrefix)
	store, ok := s.store.(state.APISandboxRunStore)
	if id != "" && valid && len(encoded) == base64.RawURLEncoding.EncodedLen(32) && ok {
		run, err := store.GetAPISandboxRun(r.Context(), id)
		hash := sha256.Sum256([]byte(token))
		if err == nil && run.Terminal() && run.ShareExpiresAt != nil && run.ShareExpiresAt.After(time.Now()) &&
			subtle.ConstantTimeCompare(run.ShareHash, hash[:]) == 1 {
			writeJSON(w, http.StatusOK, run.APISandboxRunReport)
			return
		}
	}
	api.WriteProblem(w, api.NewProblem(http.StatusUnauthorized, api.CodeUnauthorized,
		"Scenario report unavailable", "ask the release owner for an active report link"))
}
