package apidserver

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/internalmail"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) subscriptionEndedRequested() bool {
	return s.subscriptionEndedOutboxRequested != nil && s.subscriptionEndedOutboxRequested()
}

// Only a verified ingress selects this path, before its expiring replay claim.
// Selection cannot fall back to inline mail if configuration or a later gate fails.
func (s *server) handleSubscriptionEndedOutbox(w http.ResponseWriter, r *http.Request, provider string, ev billing.Event, acct state.Account) bool {
	if ev.Type != billing.EventSubscriptionCanceled || !s.subscriptionEndedRequested() || orgBillingSubject(acct) {
		return false
	}
	if err := s.endSubscriptionWithNotice(r.Context(), provider, ev, acct); err != nil {
		s.log.Error("subscription_ended.outbox_transaction_failed", "account_id", acct.ID, "provider", provider)
		api.WriteProblem(w, api.ErrCapacity("billing webhook temporarily unavailable"))
		return true
	}
	w.WriteHeader(http.StatusOK)
	return true
}

func (s *server) endSubscriptionWithNotice(ctx context.Context, provider string, ev billing.Event, acct state.Account) error {
	if s.deletionMailOutbox == nil {
		return internalmail.ErrDisabled
	}
	if provider == "" && s.billingProvider == nil {
		provider = "stripe"
	}
	subscriptionID := ev.SubscriptionID
	if subscriptionID == "" && provider == "stripe" {
		// A Stripe subscription.deleted object carries its own ID rather
		// than an invoice-style `subscription` field. Raw is already signed
		// and byte-bounded by this ingress. Legacy normalization stays intact.
		var payload struct {
			Data struct {
				Object struct {
					ID string `json:"id"`
				} `json:"object"`
			} `json:"data"`
		}
		if json.Unmarshal(ev.Raw, &payload) != nil {
			return internalmail.ErrInvalid
		}
		subscriptionID = payload.Data.Object.ID
	}
	result, err := s.deletionMailOutbox.EndSubscriptionWithConfirmation(ctx, acct.ID, provider, ev.EventID, ev.CustomerID, subscriptionID)
	if err != nil {
		return err
	}
	if result.Applied {
		// Plan invalidation is transactional in the domain. Audit retains
		// its existing best-effort semantics after the business commit.
		s.audit.Emit(ctx, "billing.subscription_ended", &acct.ID, map[string]any{"from_plan": string(result.FromPlan), "subscription_id": subscriptionID, "status": result.Status, "ended_at": result.EndedAt})
	}
	return nil
}
