package meter

import (
	"context"
	"errors"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/mail"
	"github.com/onebox-faas/faas/pkg/state"
)

func (l *Loop) deliverSpendCapNotices(ctx context.Context) {
	outbox, ok := l.store.(state.SpendCapNoticeStore)
	_, noop := l.mailer.(noopDunningSender)
	if !ok || l.mailer == nil || noop {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, api.FinancialBudgetAlertLease)
	defer cancel()
	for range api.FinancialBudgetAlertBatch {
		if ctx.Err() != nil {
			return
		}
		n, err := outbox.ClaimSpendCapNotice(ctx, l.now().UTC())
		if errors.Is(err, state.ErrNotFound) {
			return
		}
		if err != nil {
			l.log.Warn("meter: claim spend cap notice failed", "err", err)
			return
		}
		acct, err := state.BillingResourceAccount(ctx, l.store, n.AccountID)
		if err == nil && acct.Email == "" {
			err = errors.New("spend cap notice has no billing contact")
		}
		if err == nil {
			sendCtx, cancelSend := context.WithTimeout(ctx, api.FinancialBudgetAlertSendTimeout)
			subject, body := mail.SpendCapReachedBody(acct.Email, string(acct.Plan), n.CapCents, n.ObservedCents, n.ReachedAt)
			err = l.mailer.Send(sendCtx, mail.Message{To: []string{acct.Email}, Subject: subject, TextBody: body,
				MessageID: fmt.Sprintf("%s:spend_cap_reached:%s:%d", n.AccountID, n.Month.Format("2006-01"), n.CapCents)})
			cancelSend()
		}
		if settleErr := outbox.SettleSpendCapNotice(ctx, n.ID, n.LeaseToken, l.now().UTC(), err == nil); settleErr != nil {
			l.log.Warn("meter: settle spend cap notice failed", "account", n.AccountID, "err", settleErr)
		}
		if err != nil {
			l.log.Warn("meter: spend cap notice delivery failed", "account", n.AccountID)
		}
	}
}
