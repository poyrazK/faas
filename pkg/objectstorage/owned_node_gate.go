package objectstorage

import (
	"fmt"
	"strings"
)

// These startup-only operator gates are separate from the customer s3_enabled
// runtime flag. Every private capability requires the master gate as well.
const (
	OwnedNodeEnabledEnv              = "FAAS_OBJECT_NODE_ENABLED"
	OwnedNodeServerEnabledEnv        = "FAAS_OBJECT_NODE_SERVER_ENABLED"
	OwnedNodeBackendsEnabledEnv      = "FAAS_OBJECT_NODE_BACKENDS_ENABLED"
	OwnedNodeSettlementsEnabledEnv   = "FAAS_OBJECT_NODE_SETTLEMENTS_ENABLED"
	OwnedNodeUsageCountersEnabledEnv = "FAAS_OBJECT_NODE_USAGE_COUNTERS_ENABLED"
)

var ErrOwnedNodeDisabled = fmt.Errorf("%w: private development object storage feature disabled", ErrConfiguration)

func OwnedNodeEnabled(getenv func(string) string) bool {
	return ownedNodeFlagEnabled(getenv(OwnedNodeEnabledEnv))
}

func ownedNodeFlagEnabled(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// IsOwnedNodeFeatureEnv identifies private gates when isolating qualification
// child environments. Credentials are filtered separately.
func IsOwnedNodeFeatureEnv(name string) bool {
	switch name {
	case OwnedNodeEnabledEnv, OwnedNodeServerEnabledEnv, OwnedNodeBackendsEnabledEnv,
		OwnedNodeSettlementsEnabledEnv, OwnedNodeUsageCountersEnabledEnv:
		return true
	default:
		return false
	}
}

// RequireOwnedNodeServer must run before opening the node's data directory.
func RequireOwnedNodeServer(getenv func(string) string) error {
	return requireOwnedNodeFlags(getenv, OwnedNodeEnabledEnv, OwnedNodeServerEnabledEnv)
}

func requireOwnedNodeFlags(getenv func(string) string, names ...string) error {
	for _, name := range names {
		if !ownedNodeFlagEnabled(getenv(name)) {
			return fmt.Errorf("%w: requires %s", ErrOwnedNodeDisabled, name)
		}
	}
	return nil
}

// All production daemons load their registry through Load. Programmatic
// registries remain available to isolated tests and qualification fixtures.
func validateLoadedOwnedNodes(c Config, getenv func(string) string) error {
	for _, b := range c.Backends {
		if !b.OwnedNodeReceipts && !b.OwnedNodeSettlements && !b.OwnedNodeUsageCounters {
			continue
		}
		if err := requireOwnedNodeFlags(getenv, OwnedNodeEnabledEnv, OwnedNodeBackendsEnabledEnv); err != nil {
			return err
		}
		if b.OwnedNodeSettlements {
			if err := requireOwnedNodeFlags(getenv, OwnedNodeSettlementsEnabledEnv); err != nil {
				return err
			}
		}
		if b.OwnedNodeUsageCounters {
			if err := requireOwnedNodeFlags(getenv, OwnedNodeUsageCountersEnabledEnv); err != nil {
				return err
			}
		}
	}
	return nil
}
