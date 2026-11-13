package meter

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// SuspensionCommands delegates the billing/outbox transaction to apid. Meterd
// retains its scheduler/notifier follow-up and never receives decryption keys.
type SuspensionCommands interface {
	Suspend(context.Context, string, time.Time) (api.InternalMailSuspensionResponse, error)
	Complete(context.Context, string, string, string) error
}

// Billing review 2026-11-13 (R9-1): the dunning ladder no longer suspends an
// account for non-payment; it moves it to the Free plan
// (moveToFreeForNonpayment), so the durable suspension command is not issued
// from meterd any more. The interface stays for the apid and maild side of
// the internal-mail outbox, which is off by default.

// orgDunningSubject reports whether a dunning row is a company organization's
// billing subject. apid's suspension and non-payment deletion commands lock,
// transition and mail account rows only, so an organization never left
// past_due or suspended there (Hunt 17 H17-3). Organizations take the direct
// MarkDunningStep transition, which resolves organization subjects.
func orgDunningSubject(acct state.Account) bool {
	return acct.ResourceOrgID != ""
}
