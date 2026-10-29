package apidserver

import (
	"context"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/billingsync"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/internalmail"
	"github.com/onebox-faas/faas/pkg/state"
)

// billingSyncHost adapts *server to billingsync.Host (ADR-982). A correction
// takes the path a verified webhook delivery of the same event takes, outbox
// mail selection included (ADR-20261029094512).
type billingSyncHost struct{ s *server }

var _ billingsync.Host = billingSyncHost{}

func (h billingSyncHost) Store() state.Store { return h.s.store }

func (h billingSyncHost) Provider() (string, billing.CustomerSubscriptionLister, bool) {
	lister, ok := h.s.billingProvider.(billing.CustomerSubscriptionLister)
	if !ok {
		return "", nil, false
	}
	return providerName(h.s.billingProvider), lister, true
}

func (h billingSyncHost) Account(ctx context.Context, provider, customerID string) (state.Account, error) {
	return h.s.lookupBillingAccount(ctx, provider, customerID)
}

func (h billingSyncHost) PlanFromProviderID(id string) api.Plan { return billingPlanFromProviderID(id) }

func (h billingSyncHost) Apply(ctx context.Context, ev billing.Event, acct state.Account) error {
	s := h.s
	if ev.Type == billing.EventPaymentFailed && s.paymentFailedOutboxRequested != nil &&
		s.paymentFailedOutboxRequested() && !orgBillingSubject(acct) {
		if s.deletionMailOutbox == nil {
			return internalmail.ErrDisabled
		}
		return s.deletionMailOutbox.RecordPaymentFailure(ctx, acct.ID, providerName(s.billingProvider), ev.EventID, ev.CustomerID, ev.Type)
	}
	return s.handleBillingEventWithOptions(ctx, ev, acct, true)
}

func (h billingSyncHost) Audit(ctx context.Context, event, accountID string, fields map[string]any) {
	h.s.audit.Emit(ctx, event, &accountID, fields)
}
