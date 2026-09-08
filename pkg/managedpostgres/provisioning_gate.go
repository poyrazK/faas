package managedpostgres

import (
	"strconv"
	"strings"
	"time"
)

const (
	EnvironmentEnv                  = "FAAS_ENVIRONMENT"
	QualificationEnv                = "FAAS_MANAGED_POSTGRES_QUALIFIED"
	QualificationBackendEnv         = "FAAS_MANAGED_POSTGRES_QUALIFIED_BACKEND"
	QualificationFingerprintEnv     = "FAAS_MANAGED_POSTGRES_QUALIFIED_FINGERPRINT"
	QualificationUntilEnv           = "FAAS_MANAGED_POSTGRES_QUALIFIED_UNTIL"
	CanaryAccountsEnv               = "FAAS_MANAGED_POSTGRES_CANARY_ACCOUNTS"
	QualificationStagingEnvironment = "staging"
)

// NewStagingProvisioningGate returns a fail-closed rollout gate for customer
// provisioning. Configuration opt-in alone is insufficient: the daemon must
// run in staging, an operator must explicitly approve qualification, the
// approval must be unexpired, and it must name the exact configured backend
// fingerprint. A single qualified default backend is required while the
// preview is being rolled out so an unqualified region cannot receive writes.
func NewStagingProvisioningGate(registry *Registry, getenv func(string) string, now func() time.Time) func() bool {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return func() bool {
		if registry == nil || !registry.ProvisioningEnabled || getenv == nil {
			return false
		}
		if !strings.EqualFold(strings.TrimSpace(getenv(EnvironmentEnv)), QualificationStagingEnvironment) {
			return false
		}
		approved, err := strconv.ParseBool(strings.TrimSpace(getenv(QualificationEnv)))
		if err != nil || !approved {
			return false
		}
		until, err := time.Parse(time.RFC3339, strings.TrimSpace(getenv(QualificationUntilEnv)))
		if err != nil || !until.After(now().UTC()) {
			return false
		}
		regions := registry.Regions()
		if len(regions) != 1 {
			return false
		}
		backend, err := registry.Default(regions[0])
		if err != nil || strings.TrimSpace(getenv(QualificationBackendEnv)) != backend.ID || strings.TrimSpace(getenv(QualificationFingerprintEnv)) != backend.Fingerprint {
			return false
		}
		return true
	}
}

// NewStagingCanaryAccountGate returns the optional per-account rollout gate.
// An empty value keeps the existing staging behavior (all staging accounts
// are eligible). Once populated, the value is a comma-separated exact-match
// allowlist of account IDs. Empty entries and oversized lists fail closed so
// a malformed deployment cannot widen the canary accidentally.
func NewStagingCanaryAccountGate(getenv func(string) string) func(string) bool {
	return func(accountID string) bool {
		if getenv == nil || strings.TrimSpace(accountID) == "" {
			return false
		}
		raw := strings.TrimSpace(getenv(CanaryAccountsEnv))
		if raw == "" {
			return true
		}
		parts := strings.Split(raw, ",")
		if len(parts) > 100 {
			return false
		}
		allowed := false
		for _, part := range parts {
			candidate := strings.TrimSpace(part)
			if candidate == "" || len(candidate) > 255 {
				return false
			}
			if candidate == accountID {
				allowed = true
			}
		}
		return allowed
	}
}
