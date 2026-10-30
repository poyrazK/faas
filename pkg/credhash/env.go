package credhash

import (
	"fmt"
	"strconv"
)

// The account family's ring and its two rollout switches are unit
// configuration, like the seal-domain write modes: a database writer must
// not be able to turn keying off (ADR 20261029202403). apid reads them, and
// so does any operator command that writes account credentials directly.
const (
	EnvAccountKeysFile    = "FAAS_APID_CREDENTIAL_KEYS_FILE"
	EnvAccountWrites      = "FAAS_APID_CREDENTIAL_WRITES"
	EnvAccountPlainLookup = "FAAS_APID_CREDENTIAL_PLAIN_LOOKUP"
)

// LoadAccountPolicy reads the account family's policy from getenv. With
// nothing configured it is the release before: plain writes and lookups.
func LoadAccountPolicy(getenv func(string) string) (*Policy, error) {
	p := &Policy{PlainLookup: true}
	if path := getenv(EnvAccountKeysFile); path != "" {
		ring, err := LoadRing(path)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", EnvAccountKeysFile, err)
		}
		p.Ring = ring
	}
	switch v := getenv(EnvAccountWrites); v {
	case "", "plain":
	case "keyed":
		p.KeyedWrites = true
	default:
		return nil, fmt.Errorf("%s=%q is not one of plain, keyed", EnvAccountWrites, v)
	}
	if v := getenv(EnvAccountPlainLookup); v != "" {
		on, err := strconv.ParseBool(v)
		if err != nil {
			return nil, fmt.Errorf("%s must be a boolean (got %q)", EnvAccountPlainLookup, v)
		}
		p.PlainLookup = on
	}
	if err := p.Validate(); err != nil {
		return nil, fmt.Errorf("credential keys: %w", err)
	}
	return p, nil
}
