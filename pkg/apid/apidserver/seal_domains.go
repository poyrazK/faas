package apidserver

import (
	"errors"
	"fmt"
	"log/slog"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/sealdomain"
	"github.com/onebox-faas/faas/pkg/secretbox"
)

// Seal domains in apid (ADR 20261022111812). apid seals values for other
// daemons but holds only their public recipients: it cannot open an
// outbound credential or, once the runtime domain is in force, a runtime
// secret after it has sealed it.
const (
	envSealDomainOutboundRecipient = "FAAS_SEAL_DOMAIN_OUTBOUND_RECIPIENT_PATH"
	envSealDomainOutboundWrites    = "FAAS_SEAL_DOMAIN_OUTBOUND_WRITES"
	envSealDomainRuntimeRecipient  = "FAAS_SEAL_DOMAIN_RUNTIME_RECIPIENT_PATH"
	envSealDomainRuntimeWrites     = "FAAS_SEAL_DOMAIN_RUNTIME_WRITES"
)

// outboundDomainRecipient is the outbound domain's recipient once the
// seal_domains role stages it; nil until then.
var outboundDomainRecipient func() *age.X25519Recipient

// outboundWriteMode picks the recipients of new outbound credentials.
var outboundWriteMode sealdomain.WriteMode

// runtimeDomainRecipient is the runtime domain's recipient once the
// seal_domains role stages it; nil until then.
var runtimeDomainRecipient func() *age.X25519Recipient

// runtimeWriteMode is fleet (the default) or domain. Runtime secrets are
// sealed to one recipient: their writers span several packages that take
// one, and sealing to the domain and the fleet would protect nothing while
// the fleet identity can still open every row.
var runtimeWriteMode sealdomain.WriteMode

// loadSealDomains reads each wired domain's recipient and write mode. A
// mode that needs a recipient that is not staged fails the boot rather
// than the first customer write.
func loadSealDomains(getenv func(string) string, log *slog.Logger) error {
	var err error
	if outboundDomainRecipient, outboundWriteMode, err = loadDomainWriter(getenv, envSealDomainOutboundRecipient, envSealDomainOutboundWrites); err != nil {
		return err
	}
	recipients, err := outboundCredentialRecipients()
	if err != nil && outboundWriteMode != sealdomain.WriteAuto && outboundWriteMode != sealdomain.WriteFleet {
		return fmt.Errorf("apid: outbound seal domain: %w", err)
	}
	log.Info("outbound seal domain", "write_mode", string(outboundWriteMode), "domain_recipient_staged", outboundDomainRecipient != nil, "recipients", len(recipients))

	if runtimeDomainRecipient, runtimeWriteMode, err = loadDomainWriter(getenv, envSealDomainRuntimeRecipient, envSealDomainRuntimeWrites); err != nil {
		return err
	}
	switch runtimeWriteMode {
	case sealdomain.WriteDual:
		return fmt.Errorf("apid: %s: runtime secrets are sealed to one recipient; use fleet or domain", envSealDomainRuntimeWrites)
	case sealdomain.WriteDomain:
		if runtimeDomainRecipient == nil {
			return fmt.Errorf("apid: %s=domain needs %s", envSealDomainRuntimeWrites, envSealDomainRuntimeRecipient)
		}
	}
	log.Info("runtime seal domain", "write_mode", string(runtimeWriteMode), "domain_recipient_staged", runtimeDomainRecipient != nil)
	return nil
}

func loadDomainWriter(getenv func(string) string, recipientEnv, writesEnv string) (func() *age.X25519Recipient, sealdomain.WriteMode, error) {
	mode, err := sealdomain.ParseWriteMode(getenv(writesEnv))
	if err != nil {
		return nil, "", fmt.Errorf("apid: %s: %w", writesEnv, err)
	}
	path := getenv(recipientEnv)
	if path == "" {
		return nil, mode, nil
	}
	r, err := secretbox.LoadRecipient(path)
	if err != nil {
		return nil, "", fmt.Errorf("apid: load seal domain recipient %q: %w", path, err)
	}
	return func() *age.X25519Recipient { return r }, mode, nil
}

// outboundCredentialRecipients returns who new outbound credentials are
// sealed to: the fleet recipient alone until the domain is staged, then the
// domain and the fleet recipient, then the domain alone.
func outboundCredentialRecipients() ([]*age.X25519Recipient, error) {
	return sealdomain.Recipients(outboundWriteMode, recipientOrNil(outboundDomainRecipient), recipientOrNil(outboundCredentialRecipient))
}

// runtimeSecretRecipient is who app, project, release and binding secrets,
// which vmmd opens at wake, are sealed to: the runtime domain in domain
// mode, the fleet recipient otherwise. Each row's kid records which.
func runtimeSecretRecipient() *age.X25519Recipient {
	if runtimeWriteMode == sealdomain.WriteDomain {
		return recipientOrNil(runtimeDomainRecipient)
	}
	return hostSecretRecipient()
}

// errRuntimeSealDomainOwnsRekey stops apid's rekey runner once runtime
// secrets are sealed to the domain: apid cannot open those rows, and
// re-sealing the rest to the fleet would undo the domain. The re-seal runs
// as gregalectl seal-domain migrate --domain runtime.
var errRuntimeSealDomainOwnsRekey = errors.New("runtime secrets are sealed to the runtime seal domain; re-seal them with gregalectl seal-domain migrate --domain runtime")

func runtimeSealDomainBlocksRekey() error {
	if runtimeWriteMode == sealdomain.WriteDomain {
		return errRuntimeSealDomainOwnsRekey
	}
	return nil
}

func recipientOrNil(f func() *age.X25519Recipient) *age.X25519Recipient {
	if f == nil {
		return nil
	}
	return f()
}
