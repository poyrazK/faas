package mcpreadiness

import (
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/pagerduty"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

func (h *Handler) PagerDutyAcknowledgementWebhook(w http.ResponseWriter, r *http.Request) {
	body, source, receiver, ok := h.verifiedPagerDutyWebhook(w, r)
	if !ok {
		return
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	event, apply, err := pagerduty.ParseAcknowledgement(body, now, receiver.SyncUnacknowledgements)
	if err != nil {
		problem(w, state.ErrInvalidArgument)
		return
	}
	if !apply {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	input := state.MCPReadinessPagerDutyAcknowledgementInput{Action: event.Action, BindingDestination: receiver.Destination, RequireLegacyReceiver: r.PathValue("destination") == "", EventID: event.EventID, IncidentID: event.IncidentID, ProviderIncidentID: event.ProviderIncidentID, ServiceID: event.ServiceID, Region: event.Region, OccurredAt: event.OccurredAt}
	receipt, err := source.ApplyMCPReadinessPagerDutyAcknowledgement(r.Context(), r.PathValue("id"), receiver.Version, input, now)
	if err != nil && !errors.Is(err, state.ErrNotFound) {
		problem(w, err)
		return
	}
	if err == nil && (receipt.Outcome == "acknowledged" || receipt.Outcome == "released") && receipt.ReceivedAt.Equal(now) && h.Audit != nil {
		h.Audit(r.Context(), "mcp.readiness_pagerduty."+receipt.Outcome, &receiver.AccountID, map[string]any{"action": receipt.Action, "destination": receipt.Destination, "app_id": receiver.AppID, "rule_id": r.PathValue("id"), "incident_id": receipt.IncidentID, "provider_event_id": receipt.EventID, "provider_incident_id": receipt.ProviderIncidentID, "version": receipt.AttentionVersion, "source": "pagerduty"})
	}
	w.WriteHeader(http.StatusAccepted)
}
func (h *Handler) verifiedPagerDutyWebhook(w http.ResponseWriter, r *http.Request) ([]byte, state.MCPReadinessPagerDutyAcknowledgementStore, state.MCPReadinessPagerDutyAcknowledgementConfig, bool) {
	w.Header().Set("Cache-Control", "no-store")
	empty := state.MCPReadinessPagerDutyAcknowledgementConfig{}
	if uuid.Validate(r.PathValue("id")) != nil || r.URL.RawQuery != "" || r.Header.Get("Content-Encoding") != "" {
		problem(w, state.ErrInvalidArgument)
		return nil, nil, empty, false
	}
	source, ok := h.Store.(state.MCPReadinessPagerDutyAcknowledgementStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("PagerDuty acknowledgement store unavailable"))
		return nil, nil, empty, false
	}
	destination := r.PathValue("destination")
	if destination != "" && destination != "primary" && destination != "fallback" {
		problem(w, state.ErrInvalidArgument)
		return nil, nil, empty, false
	}
	receiver, err := source.ReadMCPReadinessPagerDutyAcknowledgementReceiver(r.Context(), r.PathValue("id"), destination)
	if err != nil {
		problem(w, err)
		return nil, nil, empty, false
	}
	body, ok := pagerDutyWebhookBody(w, r)
	if !ok {
		return nil, nil, empty, false
	}
	if h.Identities == nil {
		api.WriteProblem(w, api.ErrCapacity("PagerDuty webhook decryption unavailable"))
		return nil, nil, empty, false
	}
	ns, key, err := secretbox.OpenBytesMulti(h.Identities(r.Context()), receiver.SigningSecretSealed)
	if err != nil || ns != api.MCPReadinessPagerDutyWebhookSecretNamespace || !api.ValidMCPReadinessPagerDutySigningSecret(string(key)) {
		api.WriteProblem(w, api.ErrCapacity("PagerDuty webhook decryption unavailable"))
		return nil, nil, empty, false
	}
	if !pagerduty.VerifyWebhook(string(key), body, strings.Join(r.Header.Values("X-PagerDuty-Signature"), ",")) {
		api.WriteProblem(w, api.NewProblem(http.StatusUnauthorized, api.CodeUnauthorized, "Invalid PagerDuty signature", "Supply a valid signed webhook"))
		return nil, nil, empty, false
	}
	return body, source, receiver, true
}
func pagerDutyWebhookBody(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		problem(w, state.ErrInvalidArgument)
		return nil, false
	}
	r.Body = http.MaxBytesReader(w, r.Body, api.MCPReadinessPagerDutyWebhookBodyMaxBytes)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		p := api.ErrValidation("PagerDuty webhook body exceeds its bound")
		p.Status = http.StatusRequestEntityTooLarge
		api.WriteProblem(w, p)
		return nil, false
	}
	return body, true
}
