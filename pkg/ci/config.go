package ci

import (
	"slices"
	"strings"

	"github.com/google/uuid"
)

const (
	InternalEnv        = "FAAS_CI_INTERNAL_ENABLED"
	AccountsEnv        = "FAAS_CI_INTERNAL_ACCOUNTS"
	GitHubEnv          = "FAAS_CI_GITHUB_ENABLED"
	GitHubChecksEnv    = "FAAS_CI_GITHUB_CHECKS_ENABLED"
	AutomationEnv      = "FAAS_CI_AUTOMATION_ENABLED"
	ExecutionChecksEnv = "FAAS_CI_EXECUTION_CHECKS_ENABLED"
	DispatchEnv        = "FAAS_CI_DISPATCH_ENABLED"
	WorkerEnv          = "FAAS_CI_WORKER_ENABLED"
	TransportEnv       = "FAAS_CI_TRANSPORT_ENABLED"
	RetentionEnv       = "FAAS_CI_RETENTION_ENABLED"
	StorageBucketEnv   = "FAAS_CI_GCS_BUCKET"
)

// Config is boot-time, fail-closed internal admission policy. Dispatch is an
// additional gate for explicitly constructed Jobs adapters, never a replay gate.
type Config struct {
	Enabled                bool
	GitHubEnabled          bool
	GitHubChecksEnabled    bool
	AutomationEnabled      bool
	ExecutionChecksEnabled bool
	DispatchEnabled        bool
	WorkerEnabled          bool
	TransportEnabled       bool
	RetentionEnabled       bool
	accounts               map[string]bool
}

func ConfigFromEnv(getenv func(string) string) Config {
	cfg := Config{Enabled: getenv(InternalEnv) == "1", accounts: make(map[string]bool)}
	cfg.GitHubEnabled = getenv(GitHubEnv) == "1"
	cfg.GitHubChecksEnabled = getenv(GitHubChecksEnv) == "1"
	cfg.AutomationEnabled = getenv(AutomationEnv) == "1"
	cfg.ExecutionChecksEnabled = getenv(ExecutionChecksEnv) == "1"
	cfg.DispatchEnabled = getenv(DispatchEnv) == "1"
	cfg.WorkerEnabled = getenv(WorkerEnv) == "1"
	cfg.TransportEnabled = getenv(TransportEnv) == "1"
	cfg.RetentionEnabled = getenv(RetentionEnv) == "1"
	for _, value := range strings.Split(getenv(AccountsEnv), ",") {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		id, err := uuid.Parse(value)
		if err != nil {
			return Config{}
		}
		cfg.accounts[id.String()] = true
	}
	return cfg
}

func (c Config) GitHubAllows(accountID string) bool { return c.GitHubEnabled && c.Allows(accountID) }

// HealthMonitoringEnabled preserves the dormant all-flags-off service while
// allowing diagnostics for a worker draining previously admitted work.
func (c Config) HealthMonitoringEnabled() bool {
	return c.Enabled || c.WorkerEnabled || c.RetentionEnabled || c.GitHubChecksEnabled
}

// WorkerConfiguration reports only workers owned by the named daemon. These
// are boot-time intent, independent of dependency readiness or heartbeat age.
func (c Config) WorkerConfiguration(daemon string) map[WorkerKind]bool {
	github := len(c.GitHubAccounts()) > 0
	switch daemon {
	case "apid":
		return map[WorkerKind]bool{
			WorkerDispatcher: c.WorkerEnabled, WorkerRetention: c.RetentionEnabled,
			WorkerMetadataAdmission:   github,
			WorkerAutomationAdmission: github && c.AutomationEnabled && c.WorkerEnabled && c.TransportEnabled && c.DispatchEnabled,
		}
	case "githubd":
		checks := github && c.GitHubChecksEnabled
		return map[WorkerKind]bool{WorkerMetadataChecks: checks, WorkerExecutionChecks: checks && c.ExecutionChecksEnabled}
	default:
		return nil
	}
}

func (c Config) AutomationAllows(accountID string) bool {
	return c.AutomationEnabled && c.WorkerEnabled && c.TransportEnabled && c.DispatchAllows(accountID)
}

func (c Config) DispatchAllows(accountID string) bool {
	return c.DispatchEnabled && c.GitHubAllows(accountID)
}

// GitHubAccounts returns only explicitly opted-in canonical account UUIDs.
func (c Config) GitHubAccounts() []string {
	if !c.Enabled || !c.GitHubEnabled {
		return nil
	}
	ids := make([]string, 0, len(c.accounts))
	for id := range c.accounts {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
}

func (c Config) Allows(accountID string) bool {
	id, err := uuid.Parse(accountID)
	return c.Enabled && err == nil && c.accounts[id.String()]
}

func ValidID(value string) bool {
	id, err := uuid.Parse(value)
	return err == nil && id.String() == value
}
