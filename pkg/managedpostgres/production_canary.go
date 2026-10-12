package managedpostgres

import (
	"strconv"
	"strings"
	"time"
)

const ProductionCanaryEnv = "FAAS_MANAGED_POSTGRES_PRODUCTION_CANARY"

// NewProvisioningGate preserves staging admission and adds an explicitly
// enabled production test canary. A production canary requires a complete v8
// artifact or a separate two-hour operator test approval, an exact nonempty
// account allowlist, and metering and prices. A qualified approval lasts at
// most 24 hours. Environment-only approval never authorizes production.
func NewProvisioningGate(registry *Registry, getenv func(string) string, now func() time.Time) func() bool {
	staging := NewStagingProvisioningGate(registry, getenv, now)
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	var artifact *QualificationArtifact
	var trial *OperatorTestApproval
	var trialPath string
	var trialErr error
	if getenv != nil {
		if loaded, err := LoadQualificationArtifact(getenv(QualificationApprovalPathEnv)); err == nil {
			artifact = &loaded
		}
		trialPath = strings.TrimSpace(getenv(OperatorTestPathEnv))
		if trialPath != "" {
			trial, trialErr = loadOperatorTestApproval(trialPath)
		}
	}
	return func() bool {
		if !productionEnvironment(getenv) {
			return staging()
		}
		if registry == nil || !registry.ProvisioningEnabled || !productionCanaryEnabled(getenv) {
			return false
		}
		accounts, err := ParseStagingCanaryAccounts(getenv(CanaryAccountsEnv))
		if err != nil || len(accounts) == 0 {
			return false
		}
		policy := registry.UsagePolicy()
		if !policy.Enabled || policy.Validate() != nil || policy.ComputeUnitHourMillicents <= 0 || policy.StorageGiBHourMillicents <= 0 || policy.HistoryGiBHourMillicents <= 0 || policy.EgressGiBMillicents <= 0 || policy.MaxMonthlyHistoryByteSeconds <= 0 {
			return false
		}
		current := now().UTC()
		if trialPath != "" {
			return trialErr == nil && trial.valid(registry, accounts, current)
		}
		if artifact == nil || artifact.Approval == nil {
			return false
		}
		approval := artifact.Approval
		if approval.QualifiedAt.After(current) || approval.ExpiresAt.Sub(approval.QualifiedAt) > 24*time.Hour {
			return false
		}
		return registry.VerifyQualificationArtifact(*artifact, accounts, current).Ready
	}
}

// NewCanaryAccountGate forbids an empty production allowlist. Reads and cleanup
// do not use this gate; it only narrows provisioning and new credentials.
func NewCanaryAccountGate(getenv func(string) string) func(string) bool {
	staging := NewStagingCanaryAccountGate(getenv)
	return func(accountID string) bool {
		if productionEnvironment(getenv) {
			if !productionCanaryEnabled(getenv) {
				return false
			}
			accounts, err := ParseStagingCanaryAccounts(getenv(CanaryAccountsEnv))
			if err != nil || len(accounts) == 0 {
				return false
			}
		}
		return staging(accountID)
	}
}

func productionEnvironment(getenv func(string) string) bool {
	return getenv != nil && strings.EqualFold(strings.TrimSpace(getenv(EnvironmentEnv)), "production")
}

func productionCanaryEnabled(getenv func(string) string) bool {
	if getenv == nil {
		return false
	}
	enabled, err := strconv.ParseBool(strings.TrimSpace(getenv(ProductionCanaryEnv)))
	return err == nil && enabled
}
