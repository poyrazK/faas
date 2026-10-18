//go:build metal

// adr: 1287
package e2e

// ADR-1287 Stage 1 for ordinary HTTP Operations: the v1 features through the
// real daemons and a real guest. The base lane already covers results, a lost
// response with daemon restart and recovery, cancellation and revocation;
// these add webhook admission, a deadline cut on a running attempt, and the
// ADR-1285 telemetry read from what the real run left in Postgres.

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/onebox-faas/faas/pkg/api"
	stripex "github.com/onebox-faas/faas/pkg/billing/stripe"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

const customerHTTPProviderSecret = "whsec_native_customer_http"

// defineHTTP registers another definition on the fixture's live deployment,
// sharing the export contract but with its own route, input and limits.
func (f *customerHTTPFixture) defineHTTP(t *testing.T, name, path string, input json.RawMessage, limits *api.OperationLimits) api.OperationDefinitionResponse {
	t.Helper()
	spec := f.definition.Spec
	spec.Name, spec.Path, spec.InputSchema, spec.Limits = name, path, input, limits
	def, err := f.owner.PutOperationDefinition(t.Context(), f.app.Slug, f.deployment.ID, name, spec)
	mustCustomerHTTP(t, err)
	return def
}

func (f *customerHTTPFixture) postProviderEvent(t *testing.T, endpointURL string, body []byte) api.InboundWebhookReceiptResponse {
	t.Helper()
	return postNativeProviderEvent(t, f.h, endpointURL, customerHTTPProviderSecret, body)
}

// postNativeProviderEvent delivers one Stripe-signed event to an inbound
// webhook endpoint, as the provider would, and returns the 202 receipt.
func postNativeProviderEvent(t *testing.T, h *e2etest.Harness, endpointURL, secret string, body []byte) api.InboundWebhookReceiptResponse {
	t.Helper()
	u, err := url.Parse(endpointURL)
	mustCustomerHTTP(t, err)
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, h.APIDURL+u.Path, bytes.NewReader(body))
	mustCustomerHTTP(t, err)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Stripe-Signature", stripex.SignForTest(body, secret, time.Now()))
	response, err := h.HTTPClient().Do(request)
	mustCustomerHTTP(t, err)
	defer func() { _ = response.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 64<<10))
	mustCustomerHTTP(t, err)
	var receipt api.InboundWebhookReceiptResponse
	if response.StatusCode != http.StatusAccepted || json.Unmarshal(raw, &receipt) != nil {
		t.Fatalf("provider event answered HTTP %d: %s", response.StatusCode, raw)
	}
	return receipt
}

func (f *customerHTTPFixture) eventTypes(t *testing.T, id string) []string {
	t.Helper()
	page, err := f.customer.GetPlatformTenantSelfOperationEvents(t.Context(), id, 0)
	mustCustomerHTTP(t, err)
	types := []string{}
	for _, event := range page.Events {
		types = append(types, event.Type)
	}
	return types
}

// ADR-1193: a verified provider event starts one Operation on the real guest;
// the provider's retry is answered from the durable receipt.
func TestCustomerHTTPOperationWebhookAdmissionMetal(t *testing.T) {
	f := newCustomerHTTPFixture(t)
	def := f.defineHTTP(t, "export-webhook", "/exports/webhook",
		json.RawMessage(`{"type":"object","required":["webhook","payload"],"properties":{"webhook":{"type":"object"},"payload":{"type":"object"}},"additionalProperties":false}`), nil)
	endpoint, err := f.owner.CreateInboundWebhookEndpoint(t.Context(), f.app.Slug, api.CreateInboundWebhookEndpointRequest{
		Name: "native-stripe", Provider: "stripe", SigningSecret: customerHTTPProviderSecret, DeliveryPath: "/internal/stripe"})
	mustCustomerHTTP(t, err)
	zero := int64(0)
	binding, err := f.owner.PutOperationWebhookBinding(t.Context(), f.app.Slug, endpoint.ID, api.PutOperationWebhookBindingRequest{
		ExpectedVersion: &zero, Operation: def.Spec.Name, PlatformTenantID: f.tenantID, TakeOverDelivery: true})
	mustCustomerHTTP(t, err)
	if binding.Mode != api.OperationWebhookModeStart {
		t.Fatalf("binding mode %q", binding.Mode)
	}
	body := []byte(`{"id":"evt_native_1","type":"invoice.paid","data":{"object":{"id":"in_native_1"}}}`)
	first := f.postProviderEvent(t, endpoint.EndpointURL, body)
	if first.Status != "accepted" || first.Duplicate || first.ReceiptID == "" {
		t.Fatalf("first delivery %+v", first)
	}
	f.mu.Lock()
	f.operationIDs[first.ReceiptID] = true
	f.mu.Unlock()
	op := f.waitState(t, first.ReceiptID, api.OperationSucceeded)
	var output struct {
		Rows    int    `json:"rows"`
		Version string `json:"version"`
	}
	if err := json.Unmarshal(op.Result, &output); err != nil || output.Rows != 1 || output.Version != "original" {
		t.Fatalf("webhook operation result %s: %v", op.Result, err)
	}
	if retry := f.postProviderEvent(t, endpoint.EndpointURL, body); retry.ReceiptID != first.ReceiptID || !retry.Duplicate {
		t.Fatalf("provider retry %+v, want a duplicate of %s", retry, first.ReceiptID)
	}
	if entries := f.relay.observation(first.ReceiptID).entered; len(entries) != 1 {
		t.Fatalf("the guest handler ran for %d invocations, want 1", len(entries))
	}
	f.assertExecutions(t, first.ReceiptID, 1)
}

// ADR-1196: a running attempt is cut at the Operation deadline. A dispatched
// attempt may have taken effect, so the Operation needs reconciliation, and a
// safe_to_retry recovery cannot extend the deadline. The telemetry the
// schedd collector serves then reads the real outcome (ADR-1285).
func TestCustomerHTTPOperationDeadlineCutMetal(t *testing.T) {
	f := newCustomerHTTPFixture(t)
	const deadline = 20 * time.Second
	def := f.defineHTTP(t, "export-bounded", "/exports/bounded", f.definition.Spec.InputSchema, &api.OperationLimits{DeadlineSeconds: int(deadline / time.Second)})
	input, _ := json.Marshal(map[string]any{"mode": "hold"}) // the relay never releases it
	accepted, err := f.customer.StartPlatformTenantSelfOperation(t.Context(), api.OperationStartRequest{DefinitionID: def.ID, Input: input}, "bounded-1")
	mustCustomerHTTP(t, err)
	f.mu.Lock()
	f.operationIDs[accepted.ID] = true
	f.mu.Unlock()
	started := time.Now()
	customerHTTPEventually(t, 2*time.Minute, "the guest entered the bounded handler", func() bool {
		return len(f.relay.observation(accepted.ID).entered) == 1
	})
	op := f.waitState(t, accepted.ID, api.OperationRequiresReconciliation)
	elapsed := time.Since(started)
	if op.DeadlineAt == nil || op.DeadlineAt.Sub(op.CreatedAt) != deadline || elapsed > deadline+90*time.Second {
		t.Fatalf("deadline %v from %v, settled after %s", op.DeadlineAt, op.CreatedAt, elapsed)
	}
	if op.FailureCode != "execution_outcome_unknown" && op.FailureCode != api.OperationFailureDeadlineExceeded {
		t.Fatalf("cut attempt failure code %q", op.FailureCode)
	}
	if types := f.eventTypes(t, op.ID); !strings.Contains(strings.Join(types, ","), "reconciliation_required") {
		t.Fatalf("history %v lacks reconciliation_required", types)
	}
	retry := api.OperationRecoveryRequest{RecoveryID: "retry-after-deadline", ExpectedGeneration: 1, Resolution: "safe_to_retry", Evidence: "native deadline cut"}
	_, err = f.owner.RecoverOperation(t.Context(), f.app.Slug, op.ID, retry)
	var problem *api.APIError
	if !errors.As(err, &problem) || problem.Problem.Status != http.StatusConflict {
		t.Fatalf("recovery extended the deadline: %v", err)
	}

	// The real run's state, read the way schedd's 60 s tick reads it.
	snapshot, err := f.store.OperationTelemetry(t.Context(), time.Now())
	mustCustomerHTTP(t, err)
	unknown := snapshot.Outcomes[state.OperationOutcomeKey{EventType: "reconciliation_required", FailureCode: op.FailureCode}]
	if snapshot.States[api.OperationRequiresReconciliation].Count < 1 || unknown < 1 {
		t.Fatalf("telemetry missed the reconciliation: %+v", snapshot)
	}
	if op.FailureCode == "execution_outcome_unknown" && snapshot.OutcomeUnknownAccounts < 1 {
		t.Fatalf("telemetry missed the unknown-outcome account: %+v", snapshot)
	}
	registry := prometheus.NewRegistry()
	collector := sched.NewOperationTelemetry(registry, "schedd", f.store)
	mustCustomerHTTP(t, collector.Refresh(t.Context(), time.Now()))
	families, err := registry.Gather()
	mustCustomerHTTP(t, err)
	reconciling := 0.0
	for _, family := range families {
		if family.GetName() != "schedd_customer_operations" {
			continue
		}
		for _, metric := range family.GetMetric() {
			for _, label := range metric.GetLabel() {
				if label.GetName() == "state" && label.GetValue() == string(api.OperationRequiresReconciliation) {
					reconciling = metric.GetGauge().GetValue()
				}
			}
		}
	}
	if reconciling < 1 {
		t.Fatalf("schedd_customer_operations{state=requires_reconciliation} = %v", reconciling)
	}

	record := api.OperationRecoveryRequest{RecoveryID: "record-failure", ExpectedGeneration: 1, Resolution: "failed", Evidence: "native deadline cut; nothing exported"}
	settled, err := f.owner.RecoverOperation(t.Context(), f.app.Slug, op.ID, record)
	mustCustomerHTTP(t, err)
	if settled.State != api.OperationFailed {
		t.Fatalf("recorded outcome %s", settled.State)
	}
}
