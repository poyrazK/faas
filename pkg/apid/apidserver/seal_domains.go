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
	envSealDomainSourceRecipient   = "FAAS_SEAL_DOMAIN_SOURCE_RECIPIENT_PATH"
	envSealDomainSourceWrites      = "FAAS_SEAL_DOMAIN_SOURCE_WRITES"
	envSealDomainBuildRecipient    = "FAAS_SEAL_DOMAIN_BUILD_RECIPIENT_PATH"
	envSealDomainBuildWrites       = "FAAS_SEAL_DOMAIN_BUILD_WRITES"
	envSealDomainEdgeRecipient     = "FAAS_SEAL_DOMAIN_EDGE_RECIPIENT_PATH"
	envSealDomainEdgeWrites        = "FAAS_SEAL_DOMAIN_EDGE_WRITES"
	envSealDomainAlertingRecipient = "FAAS_SEAL_DOMAIN_ALERTING_RECIPIENT_PATH"
	envSealDomainAlertingWrites    = "FAAS_SEAL_DOMAIN_ALERTING_WRITES"
	// envRuntimeSecretBinding turns on account-bound runtime envelopes
	// (ADR-20261023173624): off (the default) or account. Unit
	// configuration, like the write modes: a database writer must not be
	// able to switch the binding off.
	envRuntimeSecretBinding = "FAAS_RUNTIME_SECRET_BINDING"
)

// runtimeSecretsBound seals new runtime secrets bound to their account. Only
// after every reader runs a release that checks bindings and the previous
// release has left the rollback window: an older reader refuses them.
var runtimeSecretsBound bool

// runtimeSecretBinding is the binding a runtime secret of accountID is
// sealed with: the account once binding is on, none before. An empty
// accountID with binding on fails the seal rather than writing it unbound.
func runtimeSecretBinding(accountID string) secretbox.Binding {
	if !runtimeSecretsBound {
		return secretbox.Binding{}
	}
	return secretbox.Binding{AccountID: accountID}
}

func loadRuntimeSecretBinding(getenv func(string) string) (bool, error) {
	switch v := getenv(envRuntimeSecretBinding); v {
	case "", "off":
		return false, nil
	case "account":
		return true, nil
	default:
		return false, fmt.Errorf("apid: %s=%q is not one of off, account", envRuntimeSecretBinding, v)
	}
}

// outboundDomainRecipient is the outbound domain's recipient once the
// seal_domains role stages it; nil until then.
var outboundDomainRecipient func() *age.X25519Recipient

// outboundWriteMode picks the recipients of new outbound credentials.
var outboundWriteMode sealdomain.WriteMode

// sourceDomainRecipient is the source domain's recipient once the
// seal_domains role stages it; nil until then.
var sourceDomainRecipient func() *age.X25519Recipient

// sourceWriteMode picks the recipients of a GitHub webhook secret's sealed
// copy. githubd, which opens it, seals install tokens under the same mode.
var sourceWriteMode sealdomain.WriteMode

// buildDomainRecipient is the build domain's recipient once the
// seal_domains role stages it; nil until then.
var buildDomainRecipient func() *age.X25519Recipient

// buildWriteMode picks the recipients of app and job private-registry
// passwords, which imaged opens.
var buildWriteMode sealdomain.WriteMode

// edgeDomainRecipient is the edge domain's recipient once the seal_domains
// role stages it; nil until then.
var edgeDomainRecipient func() *age.X25519Recipient

// edgeWriteMode picks the recipients of app basic-auth credentials and log
// drain authentication headers, which gatewayd-internal opens.
var edgeWriteMode sealdomain.WriteMode

// alertingDomainRecipient is the alerting domain's recipient once the
// seal_domains role stages it; nil until then.
var alertingDomainRecipient func() *age.X25519Recipient

// alertingWriteMode picks the recipients of alert rule webhook secrets,
// notification channel destinations and MCP readiness provider keys, which
// meterd opens.
var alertingWriteMode sealdomain.WriteMode

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

	if sourceDomainRecipient, sourceWriteMode, err = loadDomainWriter(getenv, envSealDomainSourceRecipient, envSealDomainSourceWrites); err != nil {
		return err
	}
	sourceRecipients, err := githubWebhookSecretRecipients()
	if err != nil && sourceWriteMode != sealdomain.WriteAuto && sourceWriteMode != sealdomain.WriteFleet {
		return fmt.Errorf("apid: source seal domain: %w", err)
	}
	log.Info("source seal domain", "write_mode", string(sourceWriteMode), "domain_recipient_staged", sourceDomainRecipient != nil, "recipients", len(sourceRecipients))

	if buildDomainRecipient, buildWriteMode, err = loadDomainWriter(getenv, envSealDomainBuildRecipient, envSealDomainBuildWrites); err != nil {
		return err
	}
	buildRecipients, err := registryCredentialRecipients()
	if err != nil && buildWriteMode != sealdomain.WriteAuto && buildWriteMode != sealdomain.WriteFleet {
		return fmt.Errorf("apid: build seal domain: %w", err)
	}
	log.Info("build seal domain", "write_mode", string(buildWriteMode), "domain_recipient_staged", buildDomainRecipient != nil, "recipients", len(buildRecipients))

	if edgeDomainRecipient, edgeWriteMode, err = loadDomainWriter(getenv, envSealDomainEdgeRecipient, envSealDomainEdgeWrites); err != nil {
		return err
	}
	edgeRecipients, err := edgeCredentialRecipients()
	if err != nil && edgeWriteMode != sealdomain.WriteAuto && edgeWriteMode != sealdomain.WriteFleet {
		return fmt.Errorf("apid: edge seal domain: %w", err)
	}
	log.Info("edge seal domain", "write_mode", string(edgeWriteMode), "domain_recipient_staged", edgeDomainRecipient != nil, "recipients", len(edgeRecipients))

	if alertingDomainRecipient, alertingWriteMode, err = loadDomainWriter(getenv, envSealDomainAlertingRecipient, envSealDomainAlertingWrites); err != nil {
		return err
	}
	alertingRecipients, err := alertingCredentialRecipients()
	if err != nil && alertingWriteMode != sealdomain.WriteAuto && alertingWriteMode != sealdomain.WriteFleet {
		return fmt.Errorf("apid: alerting seal domain: %w", err)
	}
	log.Info("alerting seal domain", "write_mode", string(alertingWriteMode), "domain_recipient_staged", alertingDomainRecipient != nil, "recipients", len(alertingRecipients))

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
	if runtimeSecretsBound, err = loadRuntimeSecretBinding(getenv); err != nil {
		return err
	}
	log.Info("runtime seal domain", "write_mode", string(runtimeWriteMode), "domain_recipient_staged", runtimeDomainRecipient != nil, "account_binding", runtimeSecretsBound)
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

// githubWebhookSecretRecipients returns who a GitHub webhook secret's sealed
// copy is sealed to: the fleet recipient alone until the source domain is
// staged, then the domain and the fleet recipient, then the domain alone.
func githubWebhookSecretRecipients() ([]*age.X25519Recipient, error) {
	return sealdomain.Recipients(sourceWriteMode, recipientOrNil(sourceDomainRecipient), hostSecretRecipient())
}

// registryCredentialRecipients returns who an app or job private-registry
// password is sealed to: the fleet recipient alone until the build domain is
// staged, then the domain and the fleet recipient, then the domain alone.
func registryCredentialRecipients() ([]*age.X25519Recipient, error) {
	return sealdomain.Recipients(buildWriteMode, recipientOrNil(buildDomainRecipient), hostSecretRecipient())
}

// edgeCredentialRecipients returns who an app's basic-auth credential or a
// log drain's authentication header is sealed to: the fleet recipient alone
// until the edge domain is staged, then the domain and the fleet recipient,
// then the domain alone.
func edgeCredentialRecipients() ([]*age.X25519Recipient, error) {
	return sealdomain.Recipients(edgeWriteMode, recipientOrNil(edgeDomainRecipient), hostSecretRecipient())
}

// alertingCredentialRecipients returns who an alert rule's webhook secret, a
// notification channel's destination or an MCP readiness provider key is
// sealed to: the fleet recipient alone until the alerting domain is staged,
// then the domain and the fleet recipient, then the domain alone. apid sends
// a test it cannot open through meterd (alert_test_relay.go).
func alertingCredentialRecipients() ([]*age.X25519Recipient, error) {
	return sealdomain.Recipients(alertingWriteMode, recipientOrNil(alertingDomainRecipient), hostSecretRecipient())
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
