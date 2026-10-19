// adr: 1193
package operation

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/operations"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *Service) webhookStore(w http.ResponseWriter) (state.OperationWebhookStore, bool) {
	store, ok := s.Host.Store().(state.OperationWebhookStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("operation webhook store unavailable"))
	}
	return store, ok
}

// PutOperationWebhookBinding binds a verified endpoint to a customer operation
// on the scope's live deployment. The caller has authorized the endpoint.
func (s *Service) PutOperationWebhookBinding(w http.ResponseWriter, r *http.Request, acct state.Account, endpoint state.InboundWebhookEndpoint) {
	var req api.PutOperationWebhookBindingRequest
	if !DecodeOperationBody(w, r, &req, api.OperationWebhookBindingMaxBytes) {
		return
	}
	if req.ExpectedVersion == nil || *req.ExpectedVersion < 0 || !req.TakeOverDelivery {
		api.WriteProblem(w, api.ErrValidation("expected_version is required and take_over_delivery must be true"))
		return
	}
	if req.Scope == "" {
		req.Scope = state.DefaultEnvScope
	}
	if limits, ok := api.LimitsFor(acct.Plan); !ok || !limits.Operations.Allowed {
		WriteOperationError(w, state.NewOperationLimitError("plan_admission", 0, 1))
		return
	}
	store, ok := s.webhookStore(w)
	if !ok {
		return
	}
	// Bind only to an operation that exists now; each event resolves again.
	def, _, err := s.resolveWebhookDefinition(r.Context(), state.OperationWebhookBinding{AccountID: acct.ID, AppID: endpoint.AppID, Scope: req.Scope, OperationName: req.Operation})
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Operation not found", "no operation with this name is defined on the environment's live deployment"))
		return
	}
	if req.Mode == api.OperationWebhookModeSignal {
		for _, signal := range req.Signals {
			if reservedSignalEvent(acct.ID, signal.Event) {
				writeReservedSignalEvent(w)
				return
			}
		}
		if problem := s.signalBindingProblem(r.Context(), def, req); problem != nil {
			api.WriteProblem(w, problem)
			return
		}
	}
	binding, err := store.SaveOperationWebhookBinding(r.Context(), state.OperationWebhookBindingOptions{EndpointID: endpoint.ID, AccountID: acct.ID, AppID: endpoint.AppID,
		ExpectedVersion: *req.ExpectedVersion, Scope: req.Scope, OperationName: req.Operation, PlatformTenantID: req.PlatformTenantID, EventTypes: req.EventTypes,
		Mode: req.Mode, Subject: req.Subject, Signals: req.Signals})
	if err != nil {
		writeOperationWebhookBindingError(w, err)
		return
	}
	s.Host.EmitAudit(r.Context(), "app.operation_webhook_bound", &acct.ID, map[string]any{"endpoint_id": endpoint.ID, "operation": binding.OperationName, "scope": binding.Scope, "platform_tenant_id": binding.PlatformTenantID, "version": binding.Version})
	httpkit.WriteJSON(w, http.StatusOK, operationWebhookBindingResponse(binding))
}

func (s *Service) GetOperationWebhookBinding(w http.ResponseWriter, r *http.Request, acct state.Account, endpoint state.InboundWebhookEndpoint) {
	store, ok := s.webhookStore(w)
	if !ok {
		return
	}
	binding, err := store.OperationWebhookBinding(r.Context(), endpoint.ID)
	if err != nil || binding.AccountID != acct.ID {
		writeOperationWebhookBindingError(w, state.ErrNotFound)
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, operationWebhookBindingResponse(binding))
}

func (s *Service) DeleteOperationWebhookBinding(w http.ResponseWriter, r *http.Request, acct state.Account, endpoint state.InboundWebhookEndpoint) {
	version, err := strconv.ParseInt(r.URL.Query().Get("expected_version"), 10, 64)
	if err != nil || version <= 0 {
		api.WriteProblem(w, api.ErrValidation("expected_version must be a positive integer"))
		return
	}
	store, ok := s.webhookStore(w)
	if !ok {
		return
	}
	if err := store.DeleteOperationWebhookBinding(r.Context(), endpoint.ID, acct.ID, version); err != nil {
		writeOperationWebhookBindingError(w, err)
		return
	}
	s.Host.EmitAudit(r.Context(), "app.operation_webhook_unbound", &acct.ID, map[string]any{"endpoint_id": endpoint.ID, "version": version})
	w.WriteHeader(http.StatusNoContent)
}

func operationWebhookBindingResponse(b state.OperationWebhookBinding) api.OperationWebhookBindingResponse {
	types := b.EventTypes
	if types == nil {
		types = []string{}
	}
	return api.OperationWebhookBindingResponse{EndpointID: b.EndpointID, AppID: b.AppID, Scope: b.Scope, Operation: b.OperationName, PlatformTenantID: b.PlatformTenantID,
		EventTypes: types, Mode: b.Mode, Subject: b.Subject, Signals: b.Signals, Version: b.Version,
		CreatedAt: b.CreatedAt.UTC().Format(time.RFC3339Nano), UpdatedAt: b.UpdatedAt.UTC().Format(time.RFC3339Nano)}
}

func writeOperationWebhookBindingError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, state.ErrOperationWebhookVersionConflict):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, "operation_webhook_binding_conflict", "Binding changed", "the binding version changed; read it again before writing"))
	case errors.Is(err, state.ErrOperationWebhookRoutingConflict):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, "inbound_webhook_routing_conflict", "Endpoint already routed", "the endpoint already starts an automation or a managed exclusive operation"))
	case errors.Is(err, state.ErrNotFound):
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not found", "the operation binding, platform tenant or its active tenant surface for this app is unavailable"))
	default:
		WriteOperationError(w, err)
	}
}

// resolveWebhookDefinition selects the named definition on the scope's live
// deployment or current project release, as declared operation routes do.
func (s *Service) resolveWebhookDefinition(ctx context.Context, binding state.OperationWebhookBinding) (state.OperationDefinition, string, error) {
	store := s.Host.Store()
	operationStore, ok := store.(state.OperationStore)
	if !ok {
		return state.OperationDefinition{}, "", state.ErrNotFound
	}
	app, err := store.AppByID(ctx, binding.AppID)
	if err != nil || app.AccountID != binding.AccountID || app.Status == state.AppDeleted {
		return state.OperationDefinition{}, "", state.ErrNotFound
	}
	deployment, release := "", ""
	if app.ProjectID != "" && app.PreviewOfSlug == "" {
		releases, ok := store.(interface {
			ResolveProjectRelease(context.Context, string, string, string) (string, string, error)
		})
		if !ok {
			return state.OperationDefinition{}, "", state.ErrNotFound
		}
		release, deployment, err = releases.ResolveProjectRelease(ctx, app.ID, binding.Scope, "")
	} else {
		var dep state.Deployment
		dep, err = store.LiveDeploymentForScope(ctx, app.ID, binding.Scope)
		deployment = dep.ID
	}
	if err != nil {
		return state.OperationDefinition{}, "", err
	}
	def, err := operationStore.OperationDefinitionForDeployment(ctx, binding.AccountID, app.ID, deployment, binding.OperationName)
	if err != nil {
		return state.OperationDefinition{}, "", err
	}
	if def.Scope != binding.Scope {
		return state.OperationDefinition{}, "", state.ErrNotFound
	}
	return def, release, nil
}

// WebhookOperationInput builds the adapter-neutral input for a verified event.
func WebhookOperationInput(endpoint state.InboundWebhookEndpoint, eventID, eventType string, body []byte) ([]byte, error) {
	return json.Marshal(api.OperationWebhookInput{Webhook: api.OperationWebhookEvent{Endpoint: endpoint.Name, Provider: string(endpoint.Provider), EventID: eventID, EventType: eventType}, Payload: body})
}

// webhookIdempotencyKey matches ADR-212's deterministic receipt identity.
func webhookIdempotencyKey(endpointID, eventID string) string {
	return "webhook:" + uuid.NewSHA1(uuid.NameSpaceURL, []byte("gregale:inbound-webhook:"+endpointID+"\x00"+eventID)).String()
}

// ReceiveBoundWebhook admits a verified event for an endpoint bound to a
// customer operation. It returns false when the endpoint has no binding.
// Every refusal is non-2xx, so the provider retains and retries the event.
// Each handled event is counted by binding mode and result (ADR-1285).
func (s *Service) ReceiveBoundWebhook(w http.ResponseWriter, r *http.Request, endpoint state.InboundWebhookEndpoint, eventID, eventType string, body []byte) bool {
	recorder := &webhookOutcomeWriter{ResponseWriter: w}
	mode := api.OperationWebhookModeStart
	handled := s.receiveBoundWebhook(recorder, r, endpoint, eventID, eventType, body, &mode)
	if handled {
		s.Metrics.webhookEvent(mode, recorder.result())
	}
	return handled
}

func (s *Service) receiveBoundWebhook(w http.ResponseWriter, r *http.Request, endpoint state.InboundWebhookEndpoint, eventID, eventType string, body []byte, mode *string) bool {
	store, ok := s.Host.Store().(state.OperationWebhookStore)
	if !ok {
		return false
	}
	digest := sha256.Sum256(body)
	// A provider retry is answered from the durable receipt alone, even after
	// the binding, definition or preview gate changed, or the binding was
	// removed: an accepted event must never also reach plain delivery.
	receipt, prior, err := store.OperationWebhookReceipt(r.Context(), endpoint.ID, eventID)
	if err == nil {
		w.Header().Set("Cache-Control", "no-store")
		if prior != digest {
			WriteOperationError(w, state.ErrOperationInputConflict)
			return true
		}
		writeWebhookOperationReceipt(w, receipt)
		return true
	}
	if !errors.Is(err, state.ErrNotFound) {
		api.WriteProblem(w, api.ErrCapacity("could not read inbound webhook operation receipt"))
		return true
	}
	binding, err := store.OperationWebhookBinding(r.Context(), endpoint.ID)
	if errors.Is(err, state.ErrNotFound) {
		return false
	}
	w.Header().Set("Cache-Control", "no-store")
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not load inbound webhook operation binding"))
		return true
	}
	if binding.Mode == api.OperationWebhookModeSignal {
		*mode = binding.Mode
		s.signalBoundWebhook(w, r, binding, endpoint, eventID, eventType, body)
		return true
	}
	if !binding.Admits(eventType) {
		httpkit.WriteJSON(w, http.StatusAccepted, api.InboundWebhookReceiptResponse{ReceiptID: uuid.NewSHA1(uuid.NameSpaceURL, []byte("gregale:inbound-webhook:"+endpoint.ID+"\x00"+eventID)).String(), Status: "ignored", AcceptedAt: s.Host.Now().UTC().Format(time.RFC3339)})
		return true
	}
	input, err := WebhookOperationInput(endpoint, eventID, eventType, body)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not encode inbound webhook operation input"))
		return true
	}
	if len(input) > api.OperationSubmissionMaxBytes {
		api.WriteProblem(w, api.ErrRequestBodyTooLarge(api.OperationSubmissionMaxBytes, int64(len(input))))
		return true
	}
	// One retry absorbs a concurrent rebinding between resolution and admission.
	for attempt := 0; ; attempt++ {
		receipt, err := s.admitBoundWebhook(r.Context(), store, binding, endpoint.ID, eventID, digest, input)
		if errors.Is(err, state.ErrOperationWebhookBindingChanged) && attempt == 0 {
			if binding, err = store.OperationWebhookBinding(r.Context(), endpoint.ID); err == nil {
				continue
			}
		}
		if err != nil {
			writeWebhookOperationError(w, err)
			return true
		}
		writeWebhookOperationReceipt(w, receipt)
		return true
	}
}

func (s *Service) admitBoundWebhook(ctx context.Context, store state.OperationWebhookStore, binding state.OperationWebhookBinding, endpointID, eventID string, digest [32]byte, input []byte) (state.WebhookOperationReceipt, error) {
	def, release, err := s.resolveWebhookDefinition(ctx, binding)
	if err != nil {
		return state.WebhookOperationReceipt{}, errWebhookOperationUnavailable
	}
	if !s.OperationTenantKindAdmission(def.AccountID, def.AppID, def.Scope, binding.PlatformTenantID, operations.DefinitionExecutionKind(def.Spec)) {
		return state.WebhookOperationReceipt{}, operations.ErrAdmissionClosed
	}
	return store.AdmitWebhookOperation(ctx, state.WebhookOperationAdmission{EndpointID: endpointID, ProviderEventID: eventID, BodySHA256: digest, BindingVersion: binding.Version,
		Admission: state.OperationAdmission{AccountID: def.AccountID, DefinitionID: def.ID, PlatformTenantID: binding.PlatformTenantID, IdempotencyKey: webhookIdempotencyKey(endpointID, eventID), ReleaseID: release, Input: input}})
}

var errWebhookOperationUnavailable = errors.New("operation: bound webhook definition unavailable")

func writeWebhookOperationReceipt(w http.ResponseWriter, receipt state.WebhookOperationReceipt) {
	w.Header().Set(api.OperationIDHeader, receipt.OperationID)
	httpkit.WriteJSON(w, http.StatusAccepted, api.InboundWebhookReceiptResponse{ReceiptID: receipt.OperationID, Status: "accepted", Duplicate: receipt.Duplicate, AcceptedAt: receipt.AcceptedAt.UTC().Format(time.RFC3339)})
}

// Unavailable targets and closed admission ask the provider to retry later.
func writeWebhookOperationError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, operations.ErrAdmissionClosed):
		w.Header().Set("Retry-After", "60")
		api.WriteProblem(w, api.ErrCapacity("new operation admission is disabled for this preview cohort"))
	case errors.Is(err, errWebhookOperationUnavailable), errors.Is(err, state.ErrNotFound), errors.Is(err, state.ErrOperationWebhookBindingChanged), errors.Is(err, state.ErrOperationExpired):
		w.Header().Set("Retry-After", "30")
		api.WriteProblem(w, api.ErrCapacity("the bound operation is unavailable on the environment's live deployment"))
	default:
		WriteOperationError(w, err)
	}
}
