package apidserver

import (
	"fmt"
	"log/slog"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/sealdomain"
	"github.com/onebox-faas/faas/pkg/secretbox"
)

// Seal domains in apid (ADR 20261022111812). apid seals values for other
// daemons but holds only their public recipients: it cannot open an
// outbound credential once it has sealed it.
const (
	envSealDomainOutboundRecipient = "FAAS_SEAL_DOMAIN_OUTBOUND_RECIPIENT_PATH"
	envSealDomainOutboundWrites    = "FAAS_SEAL_DOMAIN_OUTBOUND_WRITES"
)

// outboundDomainRecipient is the outbound domain's recipient once the
// seal_domains role stages it; nil until then.
var outboundDomainRecipient func() *age.X25519Recipient

// outboundWriteMode picks the recipients of new outbound credentials.
var outboundWriteMode sealdomain.WriteMode

// loadSealDomains reads the outbound domain's recipient and write mode.
// An explicit dual or domain mode without its recipients fails the boot
// rather than the first customer write.
func loadSealDomains(getenv func(string) string, log *slog.Logger) error {
	outboundDomainRecipient = nil
	mode, err := sealdomain.ParseWriteMode(getenv(envSealDomainOutboundWrites))
	if err != nil {
		return fmt.Errorf("apid: %s: %w", envSealDomainOutboundWrites, err)
	}
	outboundWriteMode = mode
	if path := getenv(envSealDomainOutboundRecipient); path != "" {
		r, err := secretbox.LoadRecipient(path)
		if err != nil {
			return fmt.Errorf("apid: load outbound seal domain recipient %q: %w", path, err)
		}
		outboundDomainRecipient = func() *age.X25519Recipient { return r }
	}
	recipients, err := outboundCredentialRecipients()
	if err != nil && mode != sealdomain.WriteAuto && mode != sealdomain.WriteFleet {
		return fmt.Errorf("apid: outbound seal domain: %w", err)
	}
	log.Info("outbound seal domain", "write_mode", string(mode), "domain_recipient_staged", outboundDomainRecipient != nil, "recipients", len(recipients))
	return nil
}

// outboundCredentialRecipients returns who new outbound credentials are
// sealed to: the fleet recipient alone until the domain is staged, then the
// domain and the fleet recipient, then the domain alone.
func outboundCredentialRecipients() ([]*age.X25519Recipient, error) {
	return sealdomain.Recipients(outboundWriteMode, recipientOrNil(outboundDomainRecipient), recipientOrNil(outboundCredentialRecipient))
}

func recipientOrNil(f func() *age.X25519Recipient) *age.X25519Recipient {
	if f == nil {
		return nil
	}
	return f()
}
