// adr: 20261016100929
package operation

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/operations"
	"github.com/onebox-faas/faas/pkg/state"
)

// signalBindingProblem checks a signal binding against the operation it
// names: a workflow whose subject type matches and whose steps wait for
// every mapped event. Each delivery checks again, so this only catches
// mistakes early.
func (s *Service) signalBindingProblem(ctx context.Context, def state.OperationDefinition, req api.PutOperationWebhookBindingRequest) *api.Problem {
	if req.Subject == nil || def.Spec.Subject == nil || def.Spec.Subject.Type != req.Subject.Type {
		return api.ErrValidation("a signal binding's subject type must match the operation's declared subject")
	}
	if def.Spec.Workflow == "" {
		return api.ErrValidation("only workflow operations wait for signals")
	}
	dep, err := s.Host.Store().DeploymentByID(ctx, def.DeploymentID)
	if err != nil {
		return api.ErrCapacity("could not read the operation's workflow")
	}
	var workflows []api.WorkflowSpec
	if json.Unmarshal(dep.Workflows, &workflows) != nil {
		return api.ErrCapacity("could not read the operation's workflow")
	}
	for _, workflow := range workflows {
		if workflow.Name != def.Spec.Workflow {
			continue
		}
		events := operations.OperationSignalEvents(workflow)
		for _, signal := range req.Signals {
			if !events[signal.Event] {
				return api.ErrValidation("the operation's workflow does not wait for event " + signal.Event)
			}
		}
		return nil
	}
	return api.ErrCapacity("the operation's workflow is missing from its deployment")
}

// webhookSignalReceipt keys a provider event per endpoint. Its signal ID is
// the receipt with a prefix no API caller's UUID carries, so a provider
// retry is the same signal and lands once in each Operation (ADR-1197
// idempotency).
func webhookSignalReceipt(endpointID, eventID string) (receiptID, signalID string) {
	receiptID = uuid.NewSHA1(uuid.NameSpaceURL, []byte("gregale:inbound-webhook-signal:"+endpointID+"\x00"+eventID)).String()
	return receiptID, "webhook-" + receiptID
}

// signalBoundWebhook delivers a verified provider event to the binding
// tenant's active Operations whose subject the payload names. Events the
// binding does not map, or whose payload lacks the subject, are ignored;
// other refusals are non-2xx so the provider keeps the event.
func (s *Service) signalBoundWebhook(w http.ResponseWriter, r *http.Request, binding state.OperationWebhookBinding, endpoint state.InboundWebhookEndpoint, eventID, eventType string, body []byte) {
	receiptID, signalID := webhookSignalReceipt(endpoint.ID, eventID)
	receipt := api.InboundWebhookReceiptResponse{ReceiptID: receiptID, AcceptedAt: s.Host.Now().UTC().Format(time.RFC3339)}
	event := binding.SignalEvent(eventType)
	subject, err := operations.ExtractOperationSubject(binding.Subject, body)
	if event == "" || err != nil || subject == nil {
		receipt.Status = "ignored"
		httpkit.WriteJSON(w, http.StatusAccepted, receipt)
		return
	}
	if len(body) > api.OperationSignalPayloadMaxBytes {
		api.WriteProblem(w, api.ErrRequestBodyTooLarge(api.OperationSignalPayloadMaxBytes, int64(len(body))))
		return
	}
	store, ok := s.Host.Store().(state.OperationSignalStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("operation signal store unavailable"))
		return
	}
	ids, err := store.OperationsForSubject(r.Context(), binding.AccountID, binding.AppID, binding.PlatformTenantID, binding.Scope, binding.OperationName, *subject, api.OperationSignalSubjectTargetMax+1)
	if err != nil {
		writeWebhookOperationError(w, err)
		return
	}
	if len(ids) > api.OperationSignalSubjectTargetMax {
		WriteOperationError(w, state.NewOperationLimitError("signal_subject_targets", api.OperationSignalSubjectTargetMax, int64(len(ids))))
		return
	}
	delivered, duplicate := 0, len(ids) > 0
	for _, id := range ids {
		got, err := store.SignalOperation(r.Context(), state.OperationSignalInput{AccountID: binding.AccountID, OperationID: id, SignalID: signalID, Event: event, Payload: body, Source: api.OperationSignalSourceWebhook})
		// A provider retry fixes neither: the Operation settled meanwhile, or
		// it runs a workflow snapshot that does not wait for this event.
		if errors.Is(err, state.ErrOperationNotActive) || errors.Is(err, state.ErrOperationSignalUnexpected) {
			continue
		}
		if err != nil {
			WriteOperationError(w, err)
			return
		}
		delivered++
		duplicate = duplicate && got.Duplicate
	}
	receipt.Status, receipt.Duplicate = api.InboundWebhookSignaled, delivered > 0 && duplicate
	if delivered == 0 {
		receipt.Status, receipt.Duplicate = api.InboundWebhookUnmatched, false
	}
	httpkit.WriteJSON(w, http.StatusAccepted, receipt)
}
