package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	stripex "github.com/onebox-faas/faas/pkg/billing/stripe"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

const inboundWebhookSecretSealLabel = "inbound_webhook"

var inboundWebhookNamePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)

func inboundWebhookStore(store state.Store) (state.InboundWebhookStore, bool) {
	webhooks, ok := store.(state.InboundWebhookStore)
	return webhooks, ok
}

func generateInboundWebhookToken() (string, []byte, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", nil, err
	}
	token := api.InboundWebhookTokenPrefix + base64.RawURLEncoding.EncodeToString(raw)
	digest := sha256.Sum256([]byte(token))
	return token, digest[:], nil
}

func hashInboundWebhookToken(token string) []byte {
	digest := sha256.Sum256([]byte(token))
	return digest[:]
}

func validateInboundWebhookCreate(req api.CreateInboundWebhookEndpointRequest) *api.Problem {
	if !inboundWebhookNamePattern.MatchString(req.Name) {
		return inboundWebhookProblem(http.StatusBadRequest, "name must match [a-z][a-z0-9-]{0,62}")
	}
	if req.Provider != string(state.InboundWebhookProviderStripe) {
		return inboundWebhookProblem(http.StatusBadRequest, "provider must be stripe")
	}
	if req.SigningSecret == "" || len(req.SigningSecret) > api.InboundWebhookSigningSecretMaxBytes {
		return inboundWebhookProblem(http.StatusBadRequest, "signing_secret is required and must not exceed 256 bytes")
	}
	return validateInboundWebhookDeliveryPath(req.DeliveryPath)
}

func validateInboundWebhookDeliveryPath(deliveryPath string) *api.Problem {
	if deliveryPath == "" {
		return nil
	}
	if !strings.HasPrefix(deliveryPath, "/") || len(deliveryPath) > api.InboundWebhookDeliveryPathMaxBytes || strings.ContainsAny(deliveryPath, "?#") {
		return inboundWebhookProblem(http.StatusBadRequest, "delivery_path must be an absolute path without a query or fragment and must not exceed 256 bytes")
	}
	return nil
}

func inboundWebhookProblem(status int, detail string) *api.Problem {
	return api.NewProblem(status, api.CodeInboundWebhookInvalid, "Invalid inbound webhook", detail)
}

func (s *server) createInboundWebhookEndpoint(w http.ResponseWriter, r *http.Request, acct state.Account) {
	var req api.CreateInboundWebhookEndpointRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, inboundWebhookProblem(http.StatusBadRequest, "invalid JSON body"))
		return
	}
	limits, ok := api.LimitsFor(acct.Plan)
	if !ok || limits.InboundWebhookPerApp == 0 {
		api.WriteProblem(w, api.ErrPlanInboundWebhooksNotAllowed(acct.Plan))
		return
	}
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	if !app.AcceptsRequestInvocations() {
		api.WriteProblem(w, api.ErrInvocationWorkloadClass(string(app.WorkloadClass), app.Manifest.ExecutionMode))
		return
	}
	if problem := validateInboundWebhookCreate(req); problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	s.createInboundWebhookEndpointValidated(w, r, acct, app, req, limits)
}

func (s *server) createInboundWebhookEndpointValidated(w http.ResponseWriter, r *http.Request, acct state.Account, app state.App, req api.CreateInboundWebhookEndpointRequest, limits api.Limits) {
	store, ok := inboundWebhookStore(s.store)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("inbound webhook store unavailable"))
		return
	}
	recipient := setSecretRecipient()
	if recipient == nil {
		api.WriteProblem(w, api.ErrCapacity("host identity not loaded — refusing to seal webhook secret"))
		return
	}
	sealed, err := secretbox.SealBytes(recipient, inboundWebhookSecretSealLabel, []byte(req.SigningSecret), api.InboundWebhookSigningSecretMaxBytes)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not seal inbound webhook secret"))
		return
	}
	token, tokenHash, err := generateInboundWebhookToken()
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not generate inbound webhook token"))
		return
	}
	deliveryPath := req.DeliveryPath
	if deliveryPath == "" {
		deliveryPath = "/"
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	endpoint, err := store.CreateInboundWebhookEndpointIfUnderQuota(r.Context(), state.InboundWebhookEndpoint{
		AppID: app.ID, AccountID: acct.ID, Name: req.Name,
		Provider: state.InboundWebhookProvider(req.Provider), TokenHash: tokenHash,
		SigningSecretSealed: sealed, DeliveryPath: deliveryPath, Enabled: enabled,
	}, limits)
	if err != nil {
		s.writeInboundWebhookCreateError(w, err, acct.Plan)
		return
	}
	response := inboundWebhookEndpointResponse(endpoint)
	response.EndpointURL = s.inboundWebhookURL(token)
	s.audit.Emit(r.Context(), "app.inbound_webhook_created", &acct.ID, map[string]any{"endpoint_id": endpoint.ID, "app_id": app.ID, "provider": endpoint.Provider, "delivery_path": endpoint.DeliveryPath})
	writeJSON(w, http.StatusCreated, response)
}

func (s *server) writeInboundWebhookCreateError(w http.ResponseWriter, err error, plan api.Plan) {
	var quota *state.InboundWebhookQuotaError
	switch {
	case errors.As(err, &quota):
		api.WriteProblem(w, api.ErrPlanInboundWebhookQuota(plan, string(quota.Scope), quota.Limit, quota.Observed))
	case errors.Is(err, state.ErrConflict):
		api.WriteProblem(w, inboundWebhookProblem(http.StatusConflict, "an endpoint with this name already exists"))
	default:
		api.WriteProblem(w, api.ErrCapacity("could not create inbound webhook endpoint"))
	}
}

func (s *server) listInboundWebhookEndpoints(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	store, ok := inboundWebhookStore(s.store)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("inbound webhook store unavailable"))
		return
	}
	rows, err := store.ListInboundWebhookEndpointsForApp(r.Context(), app.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not list inbound webhook endpoints"))
		return
	}
	out := make([]api.InboundWebhookEndpointResponse, 0, len(rows))
	for _, row := range rows {
		if row.AccountID == acct.ID {
			out = append(out, inboundWebhookEndpointResponse(row))
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) getInboundWebhookEndpoint(w http.ResponseWriter, r *http.Request, acct state.Account) {
	endpoint, ok := s.loadInboundWebhookEndpoint(w, r, acct)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, inboundWebhookEndpointResponse(endpoint))
}

func (s *server) updateInboundWebhookEndpoint(w http.ResponseWriter, r *http.Request, acct state.Account) {
	var req api.UpdateInboundWebhookEndpointRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, inboundWebhookProblem(http.StatusBadRequest, "invalid JSON body"))
		return
	}
	endpoint, ok := s.loadInboundWebhookEndpoint(w, r, acct)
	if !ok {
		return
	}
	params := state.UpdateInboundWebhookEndpointParams{Enabled: req.Enabled}
	if req.DeliveryPath != nil {
		if problem := validateInboundWebhookDeliveryPath(*req.DeliveryPath); problem != nil || *req.DeliveryPath == "" {
			if problem == nil {
				problem = inboundWebhookProblem(http.StatusBadRequest, "delivery_path cannot be empty")
			}
			api.WriteProblem(w, problem)
			return
		}
		params.DeliveryPath = req.DeliveryPath
	}
	if req.SigningSecret != nil {
		sealed, problem := sealInboundWebhookSecret(*req.SigningSecret)
		if problem != nil {
			api.WriteProblem(w, problem)
			return
		}
		params.SigningSecretSealed = &sealed
	}
	store, _ := inboundWebhookStore(s.store)
	updated, err := store.UpdateInboundWebhookEndpoint(r.Context(), endpoint.ID, params)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not update inbound webhook endpoint"))
		return
	}
	s.audit.Emit(r.Context(), "app.inbound_webhook_updated", &acct.ID, map[string]any{"endpoint_id": updated.ID, "app_id": updated.AppID, "enabled": updated.Enabled, "delivery_path": updated.DeliveryPath})
	writeJSON(w, http.StatusOK, inboundWebhookEndpointResponse(updated))
}

func sealInboundWebhookSecret(plaintext string) ([]byte, *api.Problem) {
	if plaintext == "" || len(plaintext) > api.InboundWebhookSigningSecretMaxBytes {
		return nil, inboundWebhookProblem(http.StatusBadRequest, "signing_secret is required and must not exceed 256 bytes")
	}
	recipient := setSecretRecipient()
	if recipient == nil {
		return nil, api.ErrCapacity("host identity not loaded — refusing to seal webhook secret")
	}
	sealed, err := secretbox.SealBytes(recipient, inboundWebhookSecretSealLabel, []byte(plaintext), api.InboundWebhookSigningSecretMaxBytes)
	if err != nil {
		return nil, api.ErrCapacity("could not seal inbound webhook secret")
	}
	return sealed, nil
}

func (s *server) deleteInboundWebhookEndpoint(w http.ResponseWriter, r *http.Request, acct state.Account) {
	endpoint, ok := s.loadInboundWebhookEndpoint(w, r, acct)
	if !ok {
		return
	}
	store, _ := inboundWebhookStore(s.store)
	if err := store.DeleteInboundWebhookEndpoint(r.Context(), endpoint.ID); err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not delete inbound webhook endpoint"))
		return
	}
	s.audit.Emit(r.Context(), "app.inbound_webhook_deleted", &acct.ID, map[string]any{"endpoint_id": endpoint.ID, "app_id": endpoint.AppID})
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) loadInboundWebhookEndpoint(w http.ResponseWriter, r *http.Request, acct state.Account) (state.InboundWebhookEndpoint, bool) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return state.InboundWebhookEndpoint{}, false
	}
	store, ok := inboundWebhookStore(s.store)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("inbound webhook store unavailable"))
		return state.InboundWebhookEndpoint{}, false
	}
	endpoint, err := store.InboundWebhookEndpointByID(r.Context(), r.PathValue("id"))
	if err != nil || endpoint.AppID != app.ID || endpoint.AccountID != acct.ID {
		s.notFound(w, "inbound webhook endpoint not found")
		return state.InboundWebhookEndpoint{}, false
	}
	return endpoint, true
}

func inboundWebhookEndpointResponse(endpoint state.InboundWebhookEndpoint) api.InboundWebhookEndpointResponse {
	return api.InboundWebhookEndpointResponseFromRow(api.InboundWebhookEndpointRow{
		ID: endpoint.ID, AppID: endpoint.AppID, AccountID: endpoint.AccountID,
		Name: endpoint.Name, Provider: string(endpoint.Provider), DeliveryPath: endpoint.DeliveryPath,
		Enabled: endpoint.Enabled, CreatedAt: endpoint.CreatedAt, UpdatedAt: endpoint.UpdatedAt,
	})
}

func (s *server) inboundWebhookURL(token string) string {
	domain := strings.TrimPrefix(strings.TrimSpace(s.domain), ".")
	return "https://api." + domain + "/v1/hooks/" + token
}

func (s *server) receiveInboundWebhook(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	if !strings.HasPrefix(token, api.InboundWebhookTokenPrefix) || len(token) > 128 {
		http.NotFound(w, r)
		return
	}
	store, ok := inboundWebhookStore(s.store)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("inbound webhook store unavailable"))
		return
	}
	endpoint, err := store.InboundWebhookEndpointByTokenHash(r.Context(), hashInboundWebhookToken(token))
	if err != nil || !endpoint.Enabled {
		http.NotFound(w, r)
		return
	}
	body, problem := readInboundWebhookBody(w, r)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	providerEventID, problem := verifyInboundWebhook(r.Context(), endpoint, r.Header, body)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	app, err := s.store.AppByID(r.Context(), endpoint.AppID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not resolve inbound webhook app"))
		return
	}
	s.acceptInboundWebhook(w, r, endpoint, providerEventID, body, effectiveInvocationRetryPolicy(app, nil))
}

func readInboundWebhookBody(w http.ResponseWriter, r *http.Request) ([]byte, *api.Problem) {
	r.Body = http.MaxBytesReader(w, r.Body, api.WebhookMaxBodyBytes)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return nil, api.ErrInboundWebhookTooLarge()
		}
		return nil, api.ErrCapacity("could not read inbound webhook body")
	}
	if !json.Valid(body) {
		return nil, inboundWebhookProblem(http.StatusBadRequest, "webhook body must be valid JSON")
	}
	return body, nil
}

func verifyInboundWebhook(ctx context.Context, endpoint state.InboundWebhookEndpoint, headers http.Header, body []byte) (string, *api.Problem) {
	identities := hostIdentitiesForUnseal(ctx)
	if len(identities) == 0 {
		return "", api.ErrCapacity("host identity not loaded — refusing to verify webhook")
	}
	namespace, secret, err := secretbox.OpenBytesMulti(identities, endpoint.SigningSecretSealed)
	if err != nil || namespace != inboundWebhookSecretSealLabel {
		return "", api.ErrCapacity("could not unseal inbound webhook secret")
	}
	if endpoint.Provider != state.InboundWebhookProviderStripe {
		return "", api.ErrCapacity("inbound webhook provider is unavailable")
	}
	if err := stripex.VerifySignature(body, headers.Get("Stripe-Signature"), string(secret), 5*time.Minute); err != nil {
		return "", api.ErrInboundWebhookBadSignature()
	}
	var envelope struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil || strings.TrimSpace(envelope.ID) == "" || len(envelope.ID) > 256 {
		return "", inboundWebhookProblem(http.StatusBadRequest, "Stripe event id is required and must not exceed 256 bytes")
	}
	return envelope.ID, nil
}

func (s *server) acceptInboundWebhook(w http.ResponseWriter, r *http.Request, endpoint state.InboundWebhookEndpoint, providerEventID string, body, retryPolicy json.RawMessage) {
	now := time.Now().UTC()
	receiptID := uuid.NewSHA1(uuid.NameSpaceURL, []byte("gregale:inbound-webhook:"+endpoint.ID+"\x00"+providerEventID)).String()
	headers, _ := json.Marshal(map[string]string{
		"content-type": r.Header.Get("Content-Type"), "x-gregale-webhook-endpoint-id": endpoint.ID,
		"x-gregale-webhook-event-id": providerEventID, "x-gregale-webhook-provider": string(endpoint.Provider),
	})
	invocation, err := s.store.EnqueueInvocation(r.Context(), state.Invocation{
		ID: receiptID, AppID: endpoint.AppID, AccountID: endpoint.AccountID,
		Source: state.InvocationInboundWebhook, State: state.InvocationPending,
		Method: http.MethodPost, Path: endpoint.DeliveryPath, Payload: body,
		Headers: headers, DueAt: now, CreatedAt: now, RetryPolicyJSON: retryPolicy,
	})
	duplicate := errors.Is(err, state.ErrConflict)
	if err != nil && !duplicate {
		api.WriteProblem(w, api.ErrCapacity("could not durably accept inbound webhook"))
		return
	}
	if duplicate {
		existing, getErr := s.store.InvocationByID(r.Context(), receiptID)
		if getErr != nil || !inboundWebhookReceiptMatches(existing, endpoint, providerEventID) {
			api.WriteProblem(w, api.ErrCapacity("inbound webhook receipt identity conflict"))
			return
		}
		invocation = existing
	} else {
		payload, _ := json.Marshal(map[string]string{"invocation_id": receiptID, "app_id": endpoint.AppID, "source": string(state.InvocationInboundWebhook)})
		_ = s.notif.Notify(r.Context(), db.NotifyInvocationDue, string(payload))
	}
	acceptedAt := invocation.CreatedAt
	if acceptedAt.IsZero() {
		acceptedAt = now
	}
	writeJSON(w, http.StatusAccepted, api.InboundWebhookReceiptResponse{
		ReceiptID: receiptID, Status: "accepted", Duplicate: duplicate, AcceptedAt: acceptedAt.UTC().Format(time.RFC3339),
	})
}

func inboundWebhookReceiptMatches(invocation state.Invocation, endpoint state.InboundWebhookEndpoint, providerEventID string) bool {
	if invocation.AppID != endpoint.AppID || invocation.AccountID != endpoint.AccountID || invocation.Source != state.InvocationInboundWebhook {
		return false
	}
	var headers map[string]string
	if err := json.Unmarshal(invocation.Headers, &headers); err != nil {
		return false
	}
	return headers["x-gregale-webhook-endpoint-id"] == endpoint.ID &&
		headers["x-gregale-webhook-event-id"] == providerEventID &&
		headers["x-gregale-webhook-provider"] == string(endpoint.Provider)
}
