package apidserver

import (
	"context"
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
	envSealDomainStorageRecipient  = "FAAS_SEAL_DOMAIN_STORAGE_RECIPIENT_PATH"
	envSealDomainStorageWrites     = "FAAS_SEAL_DOMAIN_STORAGE_WRITES"
	envSealDomainDeliveryRecipient = "FAAS_SEAL_DOMAIN_DELIVERY_RECIPIENT_PATH"
	envSealDomainDeliveryWrites    = "FAAS_SEAL_DOMAIN_DELIVERY_WRITES"
	// apid reads the account domain itself, so it holds the identity, not
	// a recipient file: the recipient is the identity's.
	envSealDomainAccountIdentity = "FAAS_SEAL_DOMAIN_ACCOUNT_IDENTITY_PATH"
	envSealDomainAccountWrites   = "FAAS_SEAL_DOMAIN_ACCOUNT_WRITES"
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

// storageDomainRecipient is the storage domain's recipient once the
// seal_domains role stages it; nil until then.
var storageDomainRecipient func() *age.X25519Recipient

// storageWriteMode picks the recipients of Gregale S3 secret access keys,
// which s3-gatewayd opens.
var storageWriteMode sealdomain.WriteMode

// deliveryDomainRecipient is the delivery domain's recipient once the
// seal_domains role stages it; nil until then.
var deliveryDomainRecipient func() *age.X25519Recipient

// deliveryWriteMode picks the recipients of app webhook signing secrets,
// commit-source connections and execution payloads, which schedd opens.
var deliveryWriteMode sealdomain.WriteMode

// accountDomainIdentities are the account domain's identity and its
// previous twin once the seal_domains role stages them; nil until then.
// apid is the account domain's reader: it opens MFA secrets, inbound
// webhook signing secrets, billing connection keys, managed workflow plans,
// PagerDuty acknowledgement secrets, GitLab, Slack and Linear credentials,
// product email values and managed realtime credentials with them.
var accountDomainIdentities []*age.X25519Identity

// accountWriteMode picks the recipients of account values.
var accountWriteMode sealdomain.WriteMode

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

	if storageDomainRecipient, storageWriteMode, err = loadDomainWriter(getenv, envSealDomainStorageRecipient, envSealDomainStorageWrites); err != nil {
		return err
	}
	storageRecipients, _, err := storageCredentialRecipients()
	if err != nil && storageWriteMode != sealdomain.WriteAuto && storageWriteMode != sealdomain.WriteFleet {
		return fmt.Errorf("apid: storage seal domain: %w", err)
	}
	log.Info("storage seal domain", "write_mode", string(storageWriteMode), "domain_recipient_staged", storageDomainRecipient != nil, "recipients", len(storageRecipients))

	if deliveryDomainRecipient, deliveryWriteMode, err = loadDomainWriter(getenv, envSealDomainDeliveryRecipient, envSealDomainDeliveryWrites); err != nil {
		return err
	}
	deliveryRecipients, err := deliveryValueRecipients()
	if err != nil && deliveryWriteMode != sealdomain.WriteAuto && deliveryWriteMode != sealdomain.WriteFleet {
		return fmt.Errorf("apid: delivery seal domain: %w", err)
	}
	log.Info("delivery seal domain", "write_mode", string(deliveryWriteMode), "domain_recipient_staged", deliveryDomainRecipient != nil, "recipients", len(deliveryRecipients))

	if err := loadAccountDomain(getenv, log); err != nil {
		return err
	}

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

// storageCredentialRecipients returns who a Gregale S3 secret access key is
// sealed to (the fleet recipient alone until the storage domain is staged,
// then the domain and the fleet recipient, then the domain alone) and the
// key id to store with it. s3-gatewayd's startup rekey re-seals a row whose
// key id is not its current key, so the id stays the fleet recipient while
// the value opens with it, and becomes the domain's once only the domain
// opens it.
func storageCredentialRecipients() ([]*age.X25519Recipient, string, error) {
	fleet := hostSecretRecipient()
	recipients, err := sealdomain.Recipients(storageWriteMode, recipientOrNil(storageDomainRecipient), fleet)
	if err != nil {
		return nil, "", err
	}
	return recipients, sealdomain.KeyID(recipients, fleet), nil
}

// deliveryValueRecipients returns who an app webhook signing secret is sealed
// to: the fleet recipient alone until the delivery domain is staged, then
// the domain and the fleet recipient, then the domain alone.
func deliveryValueRecipients() ([]*age.X25519Recipient, error) {
	return sealdomain.Recipients(deliveryWriteMode, recipientOrNil(deliveryDomainRecipient), hostSecretRecipient())
}

// commitConnectionRecipients is deliveryValueRecipients for a commit
// source's database connection. Its fleet recipient must be the shared one
// (FAAS_FLEET_AGE_RECIPIENT_PATH), never a host's own: schedd on any node
// opens it.
func commitConnectionRecipients() ([]*age.X25519Recipient, error) {
	return sealdomain.Recipients(deliveryWriteMode, recipientOrNil(deliveryDomainRecipient), recipientOrNil(outboundCredentialRecipient))
}

// executionPayloadRecipients returns who an execution payload is sealed to,
// as deliveryValueRecipients does, and the key id stored beside it. schedd
// opens a payload only with the identity its key id names, so the id stays
// the fleet recipient while the payload opens with it, and becomes the
// domain's once only the domain opens it.
func executionPayloadRecipients() ([]*age.X25519Recipient, string, error) {
	fleet := hostSecretRecipient()
	recipients, err := sealdomain.Recipients(deliveryWriteMode, recipientOrNil(deliveryDomainRecipient), fleet)
	if err != nil {
		return nil, "", err
	}
	return recipients, sealdomain.KeyID(recipients, fleet), nil
}

// loadAccountDomain loads the account identity and write mode. A configured
// identity that does not load, or domain mode without one, fails the boot.
func loadAccountDomain(getenv func(string) string, log *slog.Logger) error {
	mode, err := sealdomain.ParseWriteMode(getenv(envSealDomainAccountWrites))
	if err != nil {
		return fmt.Errorf("apid: %s: %w", envSealDomainAccountWrites, err)
	}
	var ids []*age.X25519Identity
	if path := getenv(envSealDomainAccountIdentity); path != "" {
		if ids, err = sealdomain.LoadReaderIdentities(path); err != nil {
			return fmt.Errorf("apid: load account seal-domain identity: %w", err)
		}
	}
	if mode == sealdomain.WriteDomain && len(ids) == 0 {
		return fmt.Errorf("apid: %s=domain needs %s", envSealDomainAccountWrites, envSealDomainAccountIdentity)
	}
	accountDomainIdentities, accountWriteMode = ids, mode
	log.Info("account seal domain", "write_mode", string(mode), "domain_identity", len(ids) > 0)
	return nil
}

// accountDomainRecipient is the current account identity's recipient, nil
// until the domain is staged.
func accountDomainRecipient() *age.X25519Recipient {
	if len(accountDomainIdentities) == 0 {
		return nil
	}
	return accountDomainIdentities[0].Recipient()
}

// accountRecipients returns who an account value is sealed to: legacy (the
// recipient the value's writer sealed to before the domain) alone until the
// domain is staged, then the domain and legacy, then the domain alone.
func accountRecipients(legacy *age.X25519Recipient) ([]*age.X25519Recipient, error) {
	return sealdomain.Recipients(accountWriteMode, accountDomainRecipient(), legacy)
}

// accountIdentities opens account values: the account domain first, then
// the fleet and host identities for values sealed before the domain. It is
// for opening only; the fleet identity stays first in mfaIdentities, which
// other domains' writers derive their current recipient from.
func accountIdentities() []*age.X25519Identity {
	var legacy []*age.X25519Identity
	if mfaIdentities != nil {
		legacy = mfaIdentities()
	}
	if len(legacy) == 0 && mfaIdentity != nil {
		if id := mfaIdentity(); id != nil {
			legacy = []*age.X25519Identity{id}
		}
	}
	return accountIdentitiesWith(legacy)
}

// accountIdentitiesWith is accountIdentities for a reader that has its own
// legacy identities: the account domain first, then legacy, each once.
func accountIdentitiesWith(legacy []*age.X25519Identity) []*age.X25519Identity {
	out := make([]*age.X25519Identity, 0, len(accountDomainIdentities)+len(legacy))
	seen := map[string]bool{}
	for _, id := range append(append([]*age.X25519Identity(nil), accountDomainIdentities...), legacy...) {
		if id == nil || seen[id.Recipient().String()] {
			continue
		}
		seen[id.Recipient().String()] = true
		out = append(out, id)
	}
	return out
}

// workflowPlanRecipients returns who a managed workflow's plan is sealed to,
// as accountRecipients does, and the key id stored beside it. apid's
// workflow worker opens a plan only with the identity its key id names, so
// the id stays the fleet recipient while the plan opens with it, and becomes
// the domain's once only the domain opens it.
func workflowPlanRecipients() ([]*age.X25519Recipient, string, error) {
	legacy := hostSecretRecipient()
	recipients, err := accountRecipients(legacy)
	if err != nil {
		return nil, "", err
	}
	return recipients, sealdomain.KeyID(recipients, legacy), nil
}

// holdsIdentityFor reports whether ids holds the identity of recipient kid.
func holdsIdentityFor(ids []*age.X25519Identity, kid string) bool {
	for _, id := range ids {
		if id != nil && id.Recipient().String() == kid {
			return true
		}
	}
	return false
}

// cloneSealRecipient is who project environment clone values are sealed to
// (ADR 20261022111812). They take one recipient, recorded as each row's
// key_id: clone records bind each other by ciphertext hash and open only
// with the identity their key id names, so they are never re-sealed in
// place. The fleet recipient until the account domain is domain-only, the
// domain's after; rows sealed before stay on the fleet key until their
// clone finishes.
func cloneSealRecipient() *age.X25519Recipient {
	if accountWriteMode == sealdomain.WriteDomain {
		return accountDomainRecipient()
	}
	return hostSecretRecipient()
}

// accountIdentitiesFor is accountIdentities for a handler that takes the
// request context.
func accountIdentitiesFor(context.Context) []*age.X25519Identity { return accountIdentities() }

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
