package apidserver

import (
	"fmt"
	"log/slog"
	"strconv"

	"github.com/onebox-faas/faas/pkg/credhash"
)

// Account credential hashes are keyed by a ring only apid holds (ADR
// 20261029202403). The ring and the two rollout switches are unit
// configuration, like the seal-domain write modes: a database writer must
// not be able to turn keying off.
const (
	envCredentialKeysFile    = "FAAS_APID_CREDENTIAL_KEYS_FILE"
	envCredentialWrites      = "FAAS_APID_CREDENTIAL_WRITES"
	envCredentialPlainLookup = "FAAS_APID_CREDENTIAL_PLAIN_LOOKUP"
)

// loadCredentialPolicy reads the account family's credential policy. With
// nothing configured it is the release before: plain writes and lookups.
func loadCredentialPolicy(getenv func(string) string) (*credhash.Policy, error) {
	p := &credhash.Policy{PlainLookup: true}
	if path := getenv(envCredentialKeysFile); path != "" {
		ring, err := credhash.LoadRing(path)
		if err != nil {
			return nil, fmt.Errorf("apid: %s: %w", envCredentialKeysFile, err)
		}
		p.Ring = ring
	}
	switch v := getenv(envCredentialWrites); v {
	case "", "plain":
	case "keyed":
		p.KeyedWrites = true
	default:
		return nil, fmt.Errorf("apid: %s=%q is not one of plain, keyed", envCredentialWrites, v)
	}
	if v := getenv(envCredentialPlainLookup); v != "" {
		on, err := strconv.ParseBool(v)
		if err != nil {
			return nil, fmt.Errorf("apid: %s must be a boolean (got %q)", envCredentialPlainLookup, v)
		}
		p.PlainLookup = on
	}
	if err := p.Validate(); err != nil {
		return nil, fmt.Errorf("apid: credential keys: %w", err)
	}
	return p, nil
}

// installCredentialPolicy loads the policy and installs it for this
// process's stores.
func installCredentialPolicy(getenv func(string) string, log *slog.Logger) error {
	p, err := loadCredentialPolicy(getenv)
	if err != nil {
		return err
	}
	credhash.SetAccount(p)
	current := 0
	if p.Ring != nil {
		current = int(p.Ring.Current())
	}
	log.Info("credential hashes", "keyed_writes", p.KeyedWrites, "plain_lookup", p.PlainLookup, "current_key", current)
	return nil
}
