package credhash

import (
	"fmt"
	"strconv"
)

// A family's ring and its two rollout switches are unit configuration, like
// the seal-domain write modes: a database writer must not be able to turn
// keying off (ADR 20261029202403). The account family keeps apid's names;
// every other family reads FAAS_CREDENTIAL_<FAMILY>_*.
const (
	EnvAccountKeysFile    = "FAAS_APID_CREDENTIAL_KEYS_FILE"
	EnvAccountWrites      = "FAAS_APID_CREDENTIAL_WRITES"
	EnvAccountPlainLookup = "FAAS_APID_CREDENTIAL_PLAIN_LOOKUP"
)

// EnvNames are the variables family's policy is read from: the ring file,
// the write mode and the plain-lookup switch. They are spelled out so the
// deploy tree and the environment contract can find each one.
func EnvNames(family Family) (keysFile, writes, plainLookup string) {
	switch family {
	case FamilyAccount:
		return EnvAccountKeysFile, EnvAccountWrites, EnvAccountPlainLookup
	case FamilyScheduler:
		return "FAAS_CREDENTIAL_SCHEDULER_KEYS_FILE", "FAAS_CREDENTIAL_SCHEDULER_WRITES", "FAAS_CREDENTIAL_SCHEDULER_PLAIN_LOOKUP"
	case FamilyOutbound:
		return "FAAS_CREDENTIAL_OUTBOUND_KEYS_FILE", "FAAS_CREDENTIAL_OUTBOUND_WRITES", "FAAS_CREDENTIAL_OUTBOUND_PLAIN_LOOKUP"
	case FamilyUploads:
		return "FAAS_CREDENTIAL_UPLOADS_KEYS_FILE", "FAAS_CREDENTIAL_UPLOADS_WRITES", "FAAS_CREDENTIAL_UPLOADS_PLAIN_LOOKUP"
	case FamilyAttach:
		return "FAAS_CREDENTIAL_ATTACH_KEYS_FILE", "FAAS_CREDENTIAL_ATTACH_WRITES", "FAAS_CREDENTIAL_ATTACH_PLAIN_LOOKUP"
	case FamilyDevBridge:
		return "FAAS_CREDENTIAL_DEV_BRIDGE_KEYS_FILE", "FAAS_CREDENTIAL_DEV_BRIDGE_WRITES", "FAAS_CREDENTIAL_DEV_BRIDGE_PLAIN_LOOKUP"
	case FamilyDataAPI:
		return "FAAS_CREDENTIAL_DATA_API_KEYS_FILE", "FAAS_CREDENTIAL_DATA_API_WRITES", "FAAS_CREDENTIAL_DATA_API_PLAIN_LOOKUP"
	}
	return "", "", ""
}

// LoadAccountPolicy reads the account family's policy from getenv.
func LoadAccountPolicy(getenv func(string) string) (*Policy, error) {
	return LoadPolicy(FamilyAccount, getenv)
}

// LoadPolicy reads family's policy from getenv. With nothing configured it
// is the release before: plain writes and lookups.
func LoadPolicy(family Family, getenv func(string) string) (*Policy, error) {
	keysEnv, writesEnv, plainEnv := EnvNames(family)
	if keysEnv == "" {
		return nil, fmt.Errorf("credhash: unknown credential family %q", family)
	}
	p := &Policy{PlainLookup: true}
	if path := getenv(keysEnv); path != "" {
		ring, err := LoadRing(path)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", keysEnv, err)
		}
		p.Ring = ring
	}
	switch v := getenv(writesEnv); v {
	case "", "plain":
	case "keyed":
		p.KeyedWrites = true
	default:
		return nil, fmt.Errorf("%s=%q is not one of plain, keyed", writesEnv, v)
	}
	if v := getenv(plainEnv); v != "" {
		on, err := strconv.ParseBool(v)
		if err != nil {
			return nil, fmt.Errorf("%s must be a boolean (got %q)", plainEnv, v)
		}
		p.PlainLookup = on
	}
	if err := p.Validate(); err != nil {
		return nil, fmt.Errorf("%s credential keys: %w", family, err)
	}
	return p, nil
}

// Install loads and installs the policy of each family a daemon issues or
// checks.
func Install(getenv func(string) string, families ...Family) error {
	for _, family := range families {
		p, err := LoadPolicy(family, getenv)
		if err != nil {
			return err
		}
		Set(family, p)
	}
	return nil
}
