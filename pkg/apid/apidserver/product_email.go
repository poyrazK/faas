package apidserver

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"

	"filippo.io/age"
	"github.com/onebox-faas/faas/pkg/apid/product"
	"github.com/onebox-faas/faas/pkg/mail"
	"github.com/onebox-faas/faas/pkg/productemail"
)

// Product email has a separate default-off switch. It never inherits the
// platform's log/noop transport or its nested retry/suppression wrappers.
func (s *server) configureProductEmail(getenv func(string) string) error {
	p := &s.productControl
	p.EmailWebhookSecret = getenv("FAAS_PRODUCT_CONTROL_EMAIL_WEBHOOK_SECRET")
	if p.Enabled && !product.ValidEmailWebhookSecret(p.EmailWebhookSecret) {
		return errors.New("invalid Product Control email webhook signing secret")
	}
	p.EmailTransport = nil
	p.EmailReady = func() bool { return mfaIdentity != nil && mfaIdentity() != nil && len(productEmailIdentities()) > 0 }
	p.EmailSeal = func(id, token string) ([]byte, error) {
		if mfaIdentity == nil {
			return nil, errors.New("product email encryption unavailable")
		}
		return productemail.Seal(mfaIdentity(), id, token)
	}
	p.EmailOpen = func(id string, sealed []byte) (string, error) {
		return productemail.Open(productEmailIdentities(), id, sealed)
	}
	p.EmailOrigin = "https://" + s.platformHost()
	mode := getenv("FAAS_PRODUCT_CONTROL_EMAIL_MODE")
	if !p.Enabled || mode == "" || mode == "disabled" {
		return nil
	}
	var err error
	switch mode {
	case "local":
		p.EmailTransport, err = productemail.NewLocal(getenv("FAAS_PRODUCT_CONTROL_EMAIL_SINK_DIR"))
	case "resend":
		if getenv("FAAS_PRODUCT_CONTROL_EMAIL_SEND_ENABLED") != "1" {
			return errors.New("product email resend requires FAAS_PRODUCT_CONTROL_EMAIL_SEND_ENABLED=1")
		}
		// The shared adapter logs recipient addresses on success. This private
		// stream uses delivery metadata in its ledger instead of address logs.
		p.EmailTransport, err = productemail.NewResend(mail.ResendConfig{APIKey: getenv("FAAS_MAIL_RESEND_API_KEY"), From: getenv("FAAS_MAIL_FROM"), Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	default:
		return errors.New("product email mode must be disabled, local, or resend")
	}
	if err != nil {
		return err
	}
	if platform := strings.TrimSpace(s.platformHost()); platform == "" || platform == domainUnset {
		return errors.New("product email requires the API domain")
	}
	return nil
}
func productEmailIdentities() []*age.X25519Identity {
	if mfaIdentities != nil {
		if ids := mfaIdentities(); len(ids) > 0 {
			return ids
		}
	}
	if mfaIdentity != nil {
		if id := mfaIdentity(); id != nil {
			return []*age.X25519Identity{id}
		}
	}
	return nil
}
func (s *server) runProductEmail(ctx context.Context) {
	s.productControl.RunEmail(ctx, func(error) { s.log.Warn("product.email.cycle_failed") })
}
