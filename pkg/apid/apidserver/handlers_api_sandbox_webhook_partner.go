package apidserver

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/webhookout"
)

const apiSandboxReceiverInviteTTL = 24 * time.Hour

func newSandboxReceiverToken(prefix string) (string, []byte, error) {
	var nonce [32]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", nil, err
	}
	token := prefix + base64.RawURLEncoding.EncodeToString(nonce[:])
	hash := sha256.Sum256([]byte(token))
	return token, hash[:], nil
}

func (s *server) createAPISandboxReceiverInvite(w http.ResponseWriter, r *http.Request, acct state.Account) {
	parent, source, runs, ok := s.apiSandboxRunRelease(w, r, acct)
	if !ok {
		return
	}
	id := apiSandboxPathID(r, "run")
	var input api.APISandboxReceiverInviteRequest
	if id == "" || decodeJSONSized(r, &input, 4096) != nil {
		api.WriteProblem(w, api.ErrValidation("use a lowercase UUID for the run ID and supply scenario_revision"))
		return
	}
	if !s.apiSandboxWorkerEnabled() {
		api.WriteProblem(w, api.ErrCapacity("scenario provisioning is unavailable"))
		return
	}
	if !s.allowAPISandboxProvisioning(w, r, acct, parent) || !supportedAPISandboxSource(w, parent, source) {
		return
	}
	ctx := r.Context()
	scenario, err := s.selectedAPISandboxScenario(ctx, parent.ID, source.ID, input.ScenarioRevision)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not read the release scenario"))
		return
	}
	if scenario == nil || scenario.Revision != input.ScenarioRevision || scenario.OutboundWebhook == nil {
		api.WriteProblem(w, sandboxReceiverConflict("save an outbound webhook scenario and use its current revision"))
		return
	}
	if _, err := runs.GetAPISandboxRun(ctx, id); err == nil {
		api.WriteProblem(w, sandboxReceiverConflict("use a new run ID for the receiver invite"))
		return
	} else if !errors.Is(err, state.ErrNotFound) {
		api.WriteProblem(w, api.ErrCapacity("could not check the scenario run"))
		return
	}
	partners, ok := s.store.(state.APISandboxWebhookPartnerStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("sandbox webhook partners are unavailable"))
		return
	}
	token, hash, err := newSandboxReceiverToken(api.APISandboxReceiverInvitePrefix)
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not generate a receiver invite"))
		return
	}
	partner := state.APISandboxWebhookPartner{
		RunID: id, SourceDeploymentID: source.ID, ScenarioRevision: scenario.Revision,
		InviteHash: hash, ExpiresAt: time.Now().UTC().Add(apiSandboxReceiverInviteTTL),
	}
	switch err := partners.CreateAPISandboxWebhookPartner(ctx, partner); {
	case errors.Is(err, state.ErrConflict):
		api.WriteProblem(w, sandboxReceiverConflict("use a new run ID for the receiver invite"))
	case err != nil:
		api.WriteProblem(w, api.ErrCapacity("could not save the receiver invite"))
	default:
		response := partner.InviteStatus(time.Now())
		response.Token = token
		writeJSON(w, http.StatusCreated, response)
	}
}

func (s *server) getAPISandboxReceiverInvite(w http.ResponseWriter, r *http.Request, acct state.Account) {
	_, source, _, ok := s.apiSandboxRunRelease(w, r, acct)
	if !ok {
		return
	}
	partners, ok := s.store.(state.APISandboxWebhookPartnerStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("sandbox webhook partners are unavailable"))
		return
	}
	id := apiSandboxPathID(r, "run")
	if id == "" {
		s.notFound(w, "no such receiver invite")
		return
	}
	p, err := partners.GetAPISandboxWebhookPartner(r.Context(), id)
	switch {
	case errors.Is(err, state.ErrNotFound), err == nil && p.SourceDeploymentID != source.ID:
		s.notFound(w, "no such receiver invite")
	case err != nil:
		api.WriteProblem(w, api.ErrCapacity("could not read the receiver invite"))
	default:
		writeJSON(w, http.StatusOK, p.InviteStatus(time.Now()))
	}
}

func sandboxReceiverConflict(detail string) *api.Problem {
	return api.NewProblem(http.StatusConflict, api.CodeConflict, "Sandbox receiver is unavailable", detail)
}

func (s *server) authorizeSandboxReceiver(w http.ResponseWriter, r *http.Request, prefix string) (state.APISandboxWebhookPartner, state.APISandboxWebhookPartnerStore, []byte, bool) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	id := apiSandboxPathID(r, "run")
	token, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	encoded, valid := strings.CutPrefix(token, prefix)
	partners, ok := s.store.(state.APISandboxWebhookPartnerStore)
	if id != "" && valid && len(encoded) == base64.RawURLEncoding.EncodedLen(32) && ok {
		p, err := partners.GetAPISandboxWebhookPartner(r.Context(), id)
		if err != nil && !errors.Is(err, state.ErrNotFound) {
			api.WriteProblem(w, api.ErrCapacity("could not read the receiver registration"))
			return state.APISandboxWebhookPartner{}, nil, nil, false
		}
		want := p.InviteHash
		if prefix == api.APISandboxReceiverOutcomePrefix {
			want = p.OutcomeTokenHash
		}
		hash := sha256.Sum256([]byte(token))
		if err == nil && p.ExpiresAt.After(time.Now()) && subtle.ConstantTimeCompare(want, hash[:]) == 1 {
			return p, partners, hash[:], true
		}
	}
	api.WriteProblem(w, api.NewProblem(http.StatusUnauthorized, api.CodeUnauthorized,
		"Sandbox receiver credentials unavailable", "use an active receiver invite or its run-scoped outcome token"))
	return state.APISandboxWebhookPartner{}, nil, nil, false
}

func sandboxReceiverProof(hash []byte) string {
	data := append([]byte("gregale:sandbox:receiver-proof:"), hash...)
	proof := sha256.Sum256(data)
	return "gregale-receiver-" + base64.RawURLEncoding.EncodeToString(proof[:])
}

func (s *server) getAPISandboxReceiverProof(w http.ResponseWriter, r *http.Request) {
	if p, _, hash, ok := s.authorizeSandboxReceiver(w, r, api.APISandboxReceiverInvitePrefix); ok {
		writeJSON(w, http.StatusOK, api.APISandboxReceiverProof{Proof: sandboxReceiverProof(hash), ExpiresAt: p.ExpiresAt})
	}
}

// Restrict the proof and callback to the same exact HTTPS endpoint. The
// guarded transport separately validates all resolved addresses at dial time.
func sandboxReceiverTarget(raw string) (string, bool) {
	if raw == "" || len(raw) > 2048 || strings.ContainsAny(raw, "?#") {
		return "", false
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Hostname() == "" || u.Opaque != "" ||
		strings.HasSuffix(u.Host, ":") || (u.Port() != "" && u.Port() != "443") {
		return "", false
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return "", false
	}
	if _, err := netip.ParseAddr(host); err == nil || !strings.Contains(host, ".") || strings.ContainsAny(host, ":%") {
		return "", false
	}
	return u.String(), true
}

func (s *server) verifyAPISandboxReceiver(w http.ResponseWriter, r *http.Request) {
	p, partners, hash, ok := s.authorizeSandboxReceiver(w, r, api.APISandboxReceiverInvitePrefix)
	if !ok {
		return
	}
	var input api.APISandboxReceiverURL
	if decodeJSONSized(r, &input, 4096) != nil {
		api.WriteProblem(w, api.ErrValidation("supply a public HTTPS receiver URL"))
		return
	}
	target, valid := sandboxReceiverTarget(input.URL)
	if !valid {
		api.WriteProblem(w, api.ErrValidation("use a public HTTPS hostname on port 443 without credentials, query parameters or fragments"))
		return
	}
	if !s.sandboxReceiverHasProof(r.Context(), target, sandboxReceiverProof(hash)) {
		api.WriteProblem(w, api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation,
			"Receiver ownership could not be verified", "return the supplied proof as the response body to GET on that exact HTTPS URL"))
		return
	}
	secret, _, err := generateInboundWebhookToken()
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not generate a receiver signing secret"))
		return
	}
	p.SigningSecretSealed, _ = sealAppWebhookSecret(secret)
	if len(p.SigningSecretSealed) == 0 {
		api.WriteProblem(w, api.ErrInternal("could not seal the run signing secret"))
		return
	}
	token, tokenHash, err := newSandboxReceiverToken(api.APISandboxReceiverOutcomePrefix)
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not generate an outcome token"))
		return
	}
	p.TargetURL, p.OutcomeTokenHash = target, tokenHash
	if err := partners.RedeemAPISandboxWebhookPartner(r.Context(), p, time.Now().UTC()); err != nil {
		writeSandboxReceiverUpdateError(w, err, "the invite was used or expired")
		return
	}
	writeJSON(w, http.StatusOK, api.APISandboxReceiverCredentials{SigningSecret: secret, OutcomeToken: token, ExpiresAt: p.ExpiresAt})
}

func (s *server) sandboxReceiverHasProof(ctx context.Context, target, proof string) bool {
	client := s.apiSandboxReceiverClient
	if client == nil {
		client = webhookout.GuardedHTTPClient()
	}
	check := *client
	check.Timeout = 5 * time.Second
	check.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return false
	}
	req.Header.Set("Accept", "text/plain")
	res, err := check.Do(req)
	if err != nil {
		return false
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return false
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, 257))
	return err == nil && len(body) <= 256 &&
		subtle.ConstantTimeCompare([]byte(strings.TrimSpace(string(body))), []byte(proof)) == 1
}

func (s *server) readyAPISandboxReceiver(w http.ResponseWriter, r *http.Request) {
	p, partners, hash, ok := s.authorizeSandboxReceiver(w, r, api.APISandboxReceiverOutcomePrefix)
	if !ok {
		return
	}
	if err := partners.ReadyAPISandboxWebhookPartner(r.Context(), p.RunID, hash, time.Now().UTC()); err != nil {
		writeSandboxReceiverUpdateError(w, err, "the receiver setup expired")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) recordAPISandboxReceiverOutcome(w http.ResponseWriter, r *http.Request) {
	p, partners, hash, ok := s.authorizeSandboxReceiver(w, r, api.APISandboxReceiverOutcomePrefix)
	if !ok {
		return
	}
	var input api.APISandboxReceiverOutcome
	if decodeJSONSized(r, &input, 4096) != nil || input.DeliveryID == "" || input.AppliedCount == nil ||
		*input.AppliedCount < 0 || *input.AppliedCount > 1000 {
		api.WriteProblem(w, api.ErrValidation("supply delivery_id and an integer applied_count from 0 to 1000"))
		return
	}
	runs, ok := s.store.(state.APISandboxRunStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("scenario runs are unavailable"))
		return
	}
	run, err := runs.GetAPISandboxRun(r.Context(), p.RunID)
	if err != nil && !errors.Is(err, state.ErrNotFound) {
		api.WriteProblem(w, api.ErrCapacity("could not read the callback run"))
		return
	}
	if !run.PartnerWebhook || run.PartnerOutcomeUntil == nil || sandboxPartnerDeliveryID(run) != input.DeliveryID {
		api.WriteProblem(w, sandboxReceiverConflict("wait for the retry to be recorded and use the delivery ID from that callback"))
		return
	}
	if p.OutcomeAt != nil && p.OutcomeCount == *input.AppliedCount {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err := partners.RecordAPISandboxWebhookPartnerOutcome(r.Context(), run.ID, hash, *input.AppliedCount, time.Now().UTC()); err != nil {
		writeSandboxReceiverUpdateError(w, err, "an outcome was already recorded or the run's evidence window closed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func sandboxPartnerDeliveryID(run state.APISandboxRun) string {
	for _, step := range run.Steps {
		if step.Kind == api.APISandboxStepOutboundAttempt && step.AttemptNumber == 2 && step.FailureCode == "" {
			return step.DeliveryID
		}
	}
	return ""
}

func writeSandboxReceiverUpdateError(w http.ResponseWriter, err error, detail string) {
	if errors.Is(err, state.ErrConflict) {
		api.WriteProblem(w, sandboxReceiverConflict(detail))
		return
	}
	api.WriteProblem(w, api.ErrCapacity("could not update the receiver registration"))
}
