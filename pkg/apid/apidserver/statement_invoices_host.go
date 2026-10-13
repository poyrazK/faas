package apidserver

import (
	"context"
	"errors"
	"net/http"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/apid/statementinvoices"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

// statementInvoicesHost adapts *server to statementinvoices.Host (ADR-982).
// Customer Stripe keys are sealed to apid's own MFA identity, like the other
// secrets only apid reads back.
type statementInvoicesHost struct{ s *server }

var _ statementinvoices.Host = statementInvoicesHost{}

func (h statementInvoicesHost) Store() state.Store { return h.s.store }

func (h statementInvoicesHost) ConsumerFeatureAllowed(w http.ResponseWriter, acct state.Account) bool {
	return h.s.consumerFeatureAllowed(w, acct)
}

func (h statementInvoicesHost) ConsumerStatements(w http.ResponseWriter, r *http.Request, acct state.Account) (state.App, state.APIConsumer, state.APIConsumerUsageStatementStore, bool) {
	return h.s.apiConsumerUsageStatementStore(w, r, acct)
}

func (h statementInvoicesHost) TenantStatements(w http.ResponseWriter, r *http.Request, acct state.Account) (state.PlatformTenant, state.PlatformTenantStatementStore, bool) {
	return h.s.tenantStatementStore(w, r, acct)
}

func (h statementInvoicesHost) Audit(ctx context.Context, event, accountID string, fields map[string]any) {
	h.s.audit.Emit(ctx, event, &accountID, fields)
}

func (statementInvoicesHost) SealSecret(namespace string, plaintext []byte, maxBytes int) ([]byte, error) {
	if mfaRecipient == nil || mfaRecipient() == nil {
		return nil, errors.New("seal secret: recipient unavailable")
	}
	return secretbox.SealBytes(mfaRecipient(), namespace, plaintext, maxBytes)
}

func (statementInvoicesHost) OpenSecret(blob []byte) (string, []byte, error) {
	var identities []*age.X25519Identity
	if mfaIdentities != nil {
		identities = mfaIdentities()
	}
	return secretbox.OpenBytesMulti(identities, blob)
}

func (h statementInvoicesHost) SendEmail(ctx context.Context, to, subject, body, messageID string) error {
	if h.s.mailer == nil || to == "" {
		return errors.New("send email: no mailer or recipient")
	}
	return h.s.mailer.Send(ctx, Message{To: []string{to}, Subject: subject, TextBody: body, MessageID: messageID})
}
