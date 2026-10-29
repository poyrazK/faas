// adr: 20261028103115 — production provisioning is decided by an operator
// backend pin, the managed-postgres capability flag and fleet safety ceilings,
// not by a provider qualification artifact.
package managedpostgres

import (
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const (
	ProductionEnvironment    = "production"
	ProductionBackendEnv     = "FAAS_MANAGED_POSTGRES_PRODUCTION_BACKEND"
	ProductionFingerprintEnv = "FAAS_MANAGED_POSTGRES_PRODUCTION_FINGERPRINT"
	ProductionApprovedAtEnv  = "FAAS_MANAGED_POSTGRES_PRODUCTION_APPROVED_AT"
	ProductionUntilEnv       = "FAAS_MANAGED_POSTGRES_PRODUCTION_UNTIL"
)

// ProductionApproval is the operator's production pin: one backend's ID and
// placement fingerprint, approved for a bounded window. Changing the backend
// configuration changes its fingerprint and closes provisioning until an
// operator approves it again.
type ProductionApproval struct {
	BackendID   string    `json:"backend_id"`
	Fingerprint string    `json:"backend_fingerprint"`
	ApprovedAt  time.Time `json:"approved_at"`
	Until       time.Time `json:"until"`
}

// ProductionEnvironmentActive reports whether the daemon runs in production.
func ProductionEnvironmentActive(getenv func(string) string) bool {
	return getenv != nil && strings.EqualFold(strings.TrimSpace(getenv(EnvironmentEnv)), ProductionEnvironment)
}

// LoadProductionApproval reads the pin from the environment. A missing or
// malformed value yields a zero field, which ProductionReadiness refuses.
func LoadProductionApproval(getenv func(string) string) ProductionApproval {
	if getenv == nil {
		return ProductionApproval{}
	}
	parse := func(name string) time.Time {
		value, err := time.Parse(time.RFC3339, strings.TrimSpace(getenv(name)))
		if err != nil {
			return time.Time{}
		}
		return value.UTC()
	}
	return ProductionApproval{
		BackendID:   strings.TrimSpace(getenv(ProductionBackendEnv)),
		Fingerprint: strings.TrimSpace(getenv(ProductionFingerprintEnv)),
		ApprovedAt:  parse(ProductionApprovedAtEnv),
		Until:       parse(ProductionUntilEnv),
	}
}

// ProductionReadiness checks the production gate's preconditions without
// provider IO: provisioning switched on, usage metering on, both fleet
// ceilings set, a single default backend matching the pin, and an approval
// window that has started, has not ended and spans at most the approval
// maximum. Reasons are stable codes for operators.
func (r *Registry) ProductionReadiness(approval ProductionApproval, now time.Time) QualificationReadiness {
	var reasons []string
	add := func(reason string) { reasons = append(reasons, reason) }
	if r == nil {
		return QualificationReadiness{Reasons: []string{"registry_unavailable"}}
	}
	if !r.ProvisioningEnabled {
		add("provisioning_disabled")
	}
	if !r.UsagePolicy().Enabled {
		add("usage_policy_disabled")
	}
	if !r.Fleet().Complete() {
		add("fleet_policy_incomplete")
	}
	regions := r.Regions()
	if len(regions) != 1 {
		add("default_backend_count_invalid")
	} else if backend, err := r.Default(regions[0]); err != nil {
		add("default_backend_unavailable")
	} else {
		if approval.BackendID == "" || approval.BackendID != backend.ID {
			add("backend_mismatch")
		}
		if approval.Fingerprint == "" || approval.Fingerprint != backend.Fingerprint {
			add("backend_fingerprint_mismatch")
		}
	}
	switch {
	case approval.ApprovedAt.IsZero() || approval.Until.IsZero():
		add("approval_missing")
	case !approval.Until.After(approval.ApprovedAt) || approval.Until.Sub(approval.ApprovedAt) > api.ManagedPostgresProductionApprovalMaxWindow:
		add("approval_window_invalid")
	case now.Before(approval.ApprovedAt):
		add("approval_not_started")
	case !now.Before(approval.Until):
		add("approval_expired")
	}
	return QualificationReadiness{Ready: len(reasons) == 0, Reasons: reasons}
}

// NewProvisioningGate is the rollout gate for customer provisioning. In
// production it opens only while ProductionReadiness holds, so an approval
// that expires closes new provisioning without touching existing databases;
// every other environment keeps the staging qualification gate.
func NewProvisioningGate(registry *Registry, getenv func(string) string, now func() time.Time) func() bool {
	if !ProductionEnvironmentActive(getenv) {
		return NewStagingProvisioningGate(registry, getenv, now)
	}
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	approval := LoadProductionApproval(getenv)
	return func() bool {
		return registry.ProductionReadiness(approval, now().UTC()).Ready
	}
}
