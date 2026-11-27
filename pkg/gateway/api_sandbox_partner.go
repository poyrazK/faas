package gateway

import (
	"context"
	"encoding/hex"
	"errors"
	"net/http"
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type sandboxPartnerStore interface {
	APISandboxPartnerKeyActive(ctx context.Context, appID, keyID string) (bool, error)
	RecordAPISandboxPartnerSuccess(ctx context.Context, appID, keyID string, at time.Time) error
}

func (h *Handler) enforceIsolatedSandboxKey(w http.ResponseWriter, r *http.Request, rec *statusRecorder, app App, consumer ConsumerAuthConsumer, key ConsumerAuthKey) bool {
	if consumer.ExternalRef == api.APISandboxVerificationConsumerRef && slices.Equal(key.Scopes, []string{"read"}) {
		return true
	}
	if app.APISandboxRun && consumer.ExternalRef == api.APISandboxRunConsumerRef {
		if !app.APISandboxPartner || (r.Method == http.MethodGet && slices.Equal(key.Scopes, []string{"read"})) {
			return true
		}
	}
	if app.APISandboxPartner && consumer.ExternalRef == api.APISandboxPartnerConsumerRef {
		store, ok := h.consumerAuthStore.(sandboxPartnerStore)
		if !ok {
			return h.consumerAuthUnavailable(w, r, rec, app)
		}
		active, err := store.APISandboxPartnerKeyActive(r.Context(), app.ID, key.ID)
		if err != nil {
			return h.consumerAuthUnavailable(w, r, rec, app)
		}
		if active {
			rec.sandboxPartnerKeyID = key.ID
			return true
		}
	}
	api.WriteProblem(w, api.ErrConsumerKeyInvalid())
	rec.status = http.StatusUnauthorized
	h.observe(r, rec.status, app.ID, string(app.Plan), false, Target{})
	return false
}

// Only call after proxying to an actual deployment. Platform health,
// readiness probes and failed guest responses are not partner successes.
func (h *Handler) recordSandboxPartnerSuccess(ctx context.Context, appID string, rec *statusRecorder) {
	keyID := rec.sandboxPartnerKeyID
	if keyID == "" || !rec.wroteHeader || rec.status < 200 || rec.status >= 300 || rec.platformResponse || rec.platformProblem {
		return
	}
	store, ok := h.consumerAuthStore.(sandboxPartnerStore)
	if !ok || h.sandboxSuccessTouches == nil {
		return
	}
	recent := h.sandboxSuccessTouches.last
	now := time.Now().UTC()
	if last, seen := recent.Get(keyID); seen && now.Sub(last) < consumerKeyDebounce {
		return
	}
	recent.Put(keyID, now)
	go func(ctx context.Context) {
		ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		if err := store.RecordAPISandboxPartnerSuccess(ctx, appID, keyID, now); err != nil && h.log != nil {
			h.log.Warn("gateway: record first successful sandbox partner request", "app_id", appID, "err", err)
		}
	}(context.WithoutCancel(ctx))
}

type sandboxPartnerRunStore interface {
	ClaimAPISandboxPartnerStep(ctx context.Context, appID, deploymentID, keyID, method, target string, at time.Time) (api.APISandboxPartnerStep, error)
	CompleteAPISandboxPartnerStep(ctx context.Context, permit api.APISandboxPartnerStep, at time.Time) error
}

func (h *Handler) claimSandboxPartnerStep(w http.ResponseWriter, r *http.Request, rec *statusRecorder, app App, target Target) bool {
	if isUpgradeRequest(r) {
		api.WriteProblem(w, api.ErrValidation("partner scenario steps require an HTTP request without a protocol upgrade"))
		return false
	}
	store, ok := h.consumerAuthStore.(sandboxPartnerRunStore)
	if !ok || target.DeploymentID == "" {
		api.WriteProblem(w, api.ErrCapacity("the partner scenario step is unavailable"))
		return false
	}
	start := time.Now().UTC()
	ctx, cancel := context.WithTimeout(r.Context(), time.Second)
	permit, err := store.ClaimAPISandboxPartnerStep(ctx, app.ID, target.DeploymentID, rec.sandboxPartnerKeyID, r.Method, r.URL.RequestURI(), start)
	cancel()
	switch {
	case errors.Is(err, ErrConsumerAuthNotFound):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Partner step not accepted",
			"send the next saved request in order and wait for the previous request to return"))
		return false
	case err != nil:
		api.WriteProblem(w, api.ErrCapacity("could not reserve the next partner scenario step"))
		return false
	}
	rec.sandboxPartnerStepResponse = func(status int) {
		h.completeSandboxPartnerStep(r, rec, target, store, permit, start, status)
	}
	return true
}

func (h *Handler) completeSandboxPartnerStep(r *http.Request, rec *statusRecorder, target Target, store sandboxPartnerRunStore, permit api.APISandboxPartnerStep, start time.Time, status int) {
	at := time.Now().UTC()
	permit.Step.DurationMS = at.Sub(start).Milliseconds()
	// Arbitrary caller-supplied request IDs can carry sensitive text.
	id := requestIDFrom(r)
	var decoded [16]byte
	if len(id) == 32 {
		if _, err := hex.Decode(decoded[:], []byte(id)); err == nil {
			permit.Step.RequestID = id
		}
	}
	switch {
	case rec.platformProblem || rec.platformResponse || status < http.StatusOK:
		permit.Step.FailureCode = "request_unavailable"
	case !slices.Equal(rec.Header().Values(api.RevisionHeader), []string{target.DeploymentID}):
		permit.Step.FailureCode = "deployment_mismatch"
	default:
		permit.Step.HTTPStatus = status
		if status != permit.Step.ExpectedStatus {
			permit.Step.FailureCode = "unexpected_http_status"
		}
	}
	// Keep the lease if persisting fails. The worker then marks the run
	// interrupted without admitting another request on the same copy.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), time.Second)
	defer cancel()
	if err := store.CompleteAPISandboxPartnerStep(ctx, permit, at); err != nil && h.log != nil {
		h.log.WarnContext(ctx, "checkpoint sandbox partner step", "run_id", permit.RunID, "err", err)
	}
}
