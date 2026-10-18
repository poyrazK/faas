package product

import (
	"context"
	"errors"
	"math"
	"net/http"
	"net/url"
	"strconv"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *Service) Explain(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, app, ok := s.owned(w, r, acct)
	if !ok {
		return
	}
	after, err := explanationAfter(r.URL.RawQuery)
	if err != nil {
		respond(w, nil, err)
		return
	}
	view, err := explainOperation(r.Context(), store, acct.ID, app.ID, r.PathValue("operation"), after)
	respond(w, view, err)
}

func explanationAfter(raw string) (int, error) {
	query, err := url.ParseQuery(raw)
	if err != nil {
		return 0, state.ErrInvalidArgument
	}
	for key, values := range query {
		if key != "after" || len(values) != 1 || values[0] == "" {
			return 0, state.ErrInvalidArgument
		}
	}
	if !query.Has("after") {
		return 0, nil
	}
	after, err := strconv.Atoi(query.Get("after"))
	if err != nil || after < 0 || after > math.MaxInt32 {
		return 0, state.ErrInvalidArgument
	}
	return after, nil
}

func explainOperation(ctx context.Context, store state.ProductControlStore, account, appID, id string, after int) (api.ProductOperationExplanation, error) {
	snapshot, err := store.GetProductReceipt(ctx, account, appID, id)
	if err != nil {
		return api.ProductOperationExplanation{}, err
	}
	evidence := snapshot.ProductReceiptEvidence
	view := api.ProductOperationExplanation{AppID: appID, Evidence: evidence, EntitlementDecision: "allowed_at_admission",
		Executions: []api.OperationExecution{}, Charge: api.ProductExplainedCharge{State: "unsettled", Provisional: true}}
	operations, ok := store.(state.OperationStore)
	if !ok {
		return view, state.ErrNotFound
	}
	if op := snapshot.Operation; op != nil {
		delivery, err := explanationDelivery(ctx, store, *op)
		if err != nil {
			return view, err
		}
		view.OperationRetained = true
		view.Outcome = &api.ProductExplainedOutcome{State: op.State, Generation: op.Generation, ResultAvailable: len(op.Result) > 0,
			ArtifactCount: len(op.Artifacts), CompletionDelivery: delivery}
		view.Charge.State = productChargeState(op.State)
		page, err := operations.OperationExecutions(ctx, account, id, after, api.OperationHistoryPageDefault)
		if err != nil && !errors.Is(err, state.ErrOperationExpired) && !errors.Is(err, state.ErrNotFound) {
			return view, err
		}
		if err == nil {
			view.Executions, view.NextGeneration = page.Executions, page.NextGeneration
		}
	}
	if evidence.SucceededAt != nil {
		view.Charge.State = "settled"
		view.Charge.AmountMillicents = evidence.Receipt.UnitPriceMillicents
	}
	if evidence.ResolvedState == api.OperationFailed || evidence.ResolvedState == api.OperationCancelled {
		view.Charge.State = "not_billable"
	}
	if evidence.WaivedAt != nil {
		view.Charge.State = "waived"
		view.Charge.AmountMillicents = 0
	}
	return view, nil
}

func explanationDelivery(ctx context.Context, store state.ProductControlStore, op state.Operation) (string, error) {
	if op.CompletionDelivery.DeliveryID == "" {
		return op.CompletionDelivery.State, nil
	}
	deliveries, ok := store.(interface {
		AppWebhookDeliveryByID(context.Context, string) (state.AppWebhookDelivery, error)
	})
	if !ok {
		return "", state.ErrNotFound
	}
	delivery, err := deliveries.AppWebhookDeliveryByID(ctx, op.CompletionDelivery.DeliveryID)
	if errors.Is(err, state.ErrNotFound) {
		return "delivery_expired", nil
	}
	if err != nil {
		return "", err
	}
	if delivery.AccountID != op.AccountID || delivery.AppID != op.AppID {
		return "", state.ErrNotFound
	}
	return string(delivery.Status), nil
}

func productChargeState(state api.OperationState) string {
	switch state {
	case api.OperationFailed, api.OperationCancelled:
		return "not_billable"
	case api.OperationRequiresReconciliation:
		return "awaiting_reconciliation"
	default:
		return "pending"
	}
}
