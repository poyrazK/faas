package managedpostgres

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync/atomic"

	"github.com/onebox-faas/faas/pkg/api"
)

// LivePolicy is the managed PostgreSQL operator policy apid applies without a
// restart (runtime config key managed_postgres_policy). Every field is
// optional: an omitted field keeps the value from the provider registry file
// (FAAS_MANAGED_POSTGRES_CONFIG) or, for the approval, the
// FAAS_MANAGED_POSTGRES_PRODUCTION_* environment.
//
// What stays in the file: the backends (their identity is each database's
// placement fingerprint), whether usage metering runs and its collection
// timing (the collector, metrics and cap enforcer are built from them at
// startup), and health polling. Those need a restart by design.
type LivePolicy struct {
	ProvisioningEnabled    *bool               `json:"provisioning_enabled,omitempty"`
	MaxDatabasesPerAccount *int                `json:"max_databases_per_account,omitempty"`
	Fleet                  *FleetConfig        `json:"fleet,omitempty"`
	Usage                  *LiveUsageLimits    `json:"usage,omitempty"`
	ProductionApproval     *ProductionApproval `json:"production_approval,omitempty"`
	// SuspendTimeoutSeconds is how long a scale-to-zero database stays up
	// after its last activity (suspend_timeout.go); omitted is
	// DefaultSuspendTimeoutSeconds.
	SuspendTimeoutSeconds *int64 `json:"suspend_timeout_seconds,omitempty"`
}

// LiveUsageLimits are the usage ceilings and provider rates an operator may
// change live. An omitted field keeps the file's value. Metering itself
// (enabled, collection interval, window, staleness) is not here.
type LiveUsageLimits struct {
	MaxMonthlyCostMillicents     *int64 `json:"max_monthly_cost_millicents,omitempty"`
	MaxMonthlyComputeUnitSeconds *int64 `json:"max_monthly_compute_unit_seconds,omitempty"`
	MaxMonthlyStorageByteSeconds *int64 `json:"max_monthly_storage_byte_seconds,omitempty"`
	MaxMonthlyHistoryByteSeconds *int64 `json:"max_monthly_history_byte_seconds,omitempty"`
	MaxMonthlyEgressBytes        *int64 `json:"max_monthly_egress_bytes,omitempty"`
	ComputeUnitHourMillicents    *int64 `json:"compute_unit_hour_millicents,omitempty"`
	StorageGiBHourMillicents     *int64 `json:"storage_gib_hour_millicents,omitempty"`
	HistoryGiBHourMillicents     *int64 `json:"history_gib_hour_millicents,omitempty"`
	EgressGiBMillicents          *int64 `json:"egress_gib_millicents,omitempty"`
}

var liveFingerprint = regexp.MustCompile(`^[a-f0-9]{64}$`)

// ParseLivePolicy decodes and checks a policy on its own: known fields only,
// sane bounds, and a well-formed approval. Whether it fits this registry
// (the approval's backend, the merged usage policy) is ApplyLivePolicy's
// check. null and {} are the empty policy.
func ParseLivePolicy(raw json.RawMessage) (LivePolicy, error) {
	var policy LivePolicy
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		return policy, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&policy); err != nil {
		return LivePolicy{}, fmt.Errorf("managed postgres policy: %w", err)
	}
	if decoder.More() {
		return LivePolicy{}, errors.New("managed postgres policy: trailing data")
	}
	if n := policy.MaxDatabasesPerAccount; n != nil && (*n < 1 || *n > 100) {
		return LivePolicy{}, errors.New("managed postgres policy: max_databases_per_account must be 1..100")
	}
	if n := policy.SuspendTimeoutSeconds; n != nil && !validSuspendTimeout(*n) {
		return LivePolicy{}, fmt.Errorf("managed postgres policy: suspend_timeout_seconds must be %d..%d", MinSuspendTimeoutSeconds, MaxSuspendTimeoutSeconds)
	}
	if policy.Fleet != nil {
		if _, err := policy.Fleet.policy(); err != nil {
			return LivePolicy{}, fmt.Errorf("managed postgres policy: fleet: %w", err)
		}
	}
	if usage := policy.Usage; usage != nil {
		for name, value := range usage.fields() {
			if value != nil && *value < 0 {
				return LivePolicy{}, fmt.Errorf("managed postgres policy: usage.%s must not be negative", name)
			}
		}
	}
	if approval := policy.ProductionApproval; approval != nil {
		switch {
		case strings.TrimSpace(approval.BackendID) == "":
			return LivePolicy{}, errors.New("managed postgres policy: production_approval.backend_id is required")
		case !liveFingerprint.MatchString(approval.Fingerprint):
			return LivePolicy{}, errors.New("managed postgres policy: production_approval.backend_fingerprint must be 64 lowercase hex characters")
		case approval.ApprovedAt.IsZero() || approval.Until.IsZero():
			return LivePolicy{}, errors.New("managed postgres policy: production_approval needs approved_at and until (RFC 3339)")
		case !approval.Until.After(approval.ApprovedAt) || approval.Until.Sub(approval.ApprovedAt) > api.ManagedPostgresProductionApprovalMaxWindow:
			return LivePolicy{}, errors.New("managed postgres policy: production_approval until must be after approved_at by at most 90 days")
		}
	}
	return policy, nil
}

func (u *LiveUsageLimits) fields() map[string]*int64 {
	return map[string]*int64{
		"max_monthly_cost_millicents":      u.MaxMonthlyCostMillicents,
		"max_monthly_compute_unit_seconds": u.MaxMonthlyComputeUnitSeconds,
		"max_monthly_storage_byte_seconds": u.MaxMonthlyStorageByteSeconds,
		"max_monthly_history_byte_seconds": u.MaxMonthlyHistoryByteSeconds,
		"max_monthly_egress_bytes":         u.MaxMonthlyEgressBytes,
		"compute_unit_hour_millicents":     u.ComputeUnitHourMillicents,
		"storage_gib_hour_millicents":      u.StorageGiBHourMillicents,
		"history_gib_hour_millicents":      u.HistoryGiBHourMillicents,
		"egress_gib_millicents":            u.EgressGiBMillicents,
	}
}

// effectivePolicy is what the registry serves: the file's values with the
// live policy laid over them. It is replaced whole, so a reader never sees
// half of an update.
type effectivePolicy struct {
	provisioningEnabled    bool
	maxDatabasesPerAccount int
	usage                  UsagePolicy
	fleet                  FleetPolicy
	// approval is the live production approval; nil falls back to the
	// environment's.
	approval *ProductionApproval
	// suspendTimeoutSeconds is the scale-to-zero suspend timeout; 0 is
	// DefaultSuspendTimeoutSeconds.
	suspendTimeoutSeconds int64
}

// filePolicy is the registry file's part of the policy, kept so a live
// policy is always laid over the file and never over an earlier live value.
type filePolicy struct {
	provisioningEnabled    bool
	maxDatabasesPerAccount int
	usage                  UsageConfig
	fleet                  FleetConfig
}

type livePolicyState struct {
	file    filePolicy
	current atomic.Pointer[effectivePolicy]
	// changed runs after each applied live policy (OnPolicyChange).
	changed atomic.Pointer[func()]
}

// OnPolicyChange registers f to run after every live policy apid applies:
// apid re-reports production readiness and the approval's end for the
// expiry alerts. A later call replaces the earlier f.
func (r *Registry) OnPolicyChange(f func()) {
	if r == nil || r.policy == nil {
		return
	}
	r.policy.changed.Store(&f)
}

func (r *Registry) effective() *effectivePolicy {
	if r == nil || r.policy == nil {
		return &effectivePolicy{}
	}
	if current := r.policy.current.Load(); current != nil {
		return current
	}
	return &effectivePolicy{}
}

// ApplyLivePolicy lays policy over the registry file's values and installs
// the result for every reader at once. It refuses, leaving the current
// policy in force, a policy whose merged usage policy is invalid or whose
// approval names a backend this registry does not have.
func (r *Registry) ApplyLivePolicy(policy LivePolicy) error {
	next, err := r.mergeLivePolicy(policy)
	if err != nil {
		return err
	}
	r.policy.current.Store(next)
	r.pushSuspendTimeout(r.SuspendTimeoutSeconds())
	if changed := r.policy.changed.Load(); changed != nil && *changed != nil {
		(*changed)()
	}
	return nil
}

// CheckLivePolicyJSON reports whether ApplyLivePolicyJSON would accept raw,
// without changing anything: the write-time check of the runtime config key.
func (r *Registry) CheckLivePolicyJSON(raw json.RawMessage) error {
	policy, err := ParseLivePolicy(raw)
	if err != nil {
		return err
	}
	_, err = r.mergeLivePolicy(policy)
	return err
}

func (r *Registry) mergeLivePolicy(policy LivePolicy) (*effectivePolicy, error) {
	if r == nil || r.policy == nil {
		return nil, ErrUnavailable
	}
	file := r.policy.file
	next := effectivePolicy{provisioningEnabled: file.provisioningEnabled, maxDatabasesPerAccount: file.maxDatabasesPerAccount,
		suspendTimeoutSeconds: DefaultSuspendTimeoutSeconds}
	if policy.SuspendTimeoutSeconds != nil {
		next.suspendTimeoutSeconds = *policy.SuspendTimeoutSeconds
	}
	if policy.ProvisioningEnabled != nil {
		next.provisioningEnabled = *policy.ProvisioningEnabled
	}
	if policy.MaxDatabasesPerAccount != nil {
		next.maxDatabasesPerAccount = *policy.MaxDatabasesPerAccount
	}
	if next.maxDatabasesPerAccount < 1 || next.maxDatabasesPerAccount > 100 {
		return nil, errors.New("managed postgres policy: max_databases_per_account must be 1..100")
	}
	usage := file.usage
	if live := policy.Usage; live != nil {
		overlay := func(dst *int64, src *int64) {
			if src != nil {
				*dst = *src
			}
		}
		overlay(&usage.MaxMonthlyCostMillicents, live.MaxMonthlyCostMillicents)
		overlay(&usage.MaxMonthlyComputeUnitSeconds, live.MaxMonthlyComputeUnitSeconds)
		overlay(&usage.MaxMonthlyStorageByteSeconds, live.MaxMonthlyStorageByteSeconds)
		overlay(&usage.MaxMonthlyHistoryByteSeconds, live.MaxMonthlyHistoryByteSeconds)
		overlay(&usage.MaxMonthlyEgressBytes, live.MaxMonthlyEgressBytes)
		overlay(&usage.ComputeUnitHourMillicents, live.ComputeUnitHourMillicents)
		overlay(&usage.StorageGiBHourMillicents, live.StorageGiBHourMillicents)
		overlay(&usage.HistoryGiBHourMillicents, live.HistoryGiBHourMillicents)
		overlay(&usage.EgressGiBMillicents, live.EgressGiBMillicents)
	}
	usagePolicy, err := usage.policy()
	if err != nil {
		return nil, fmt.Errorf("managed postgres policy: usage: %w", err)
	}
	next.usage = usagePolicy
	fleet := file.fleet
	if policy.Fleet != nil {
		fleet = *policy.Fleet
	}
	if next.fleet, err = fleet.policy(); err != nil {
		return nil, fmt.Errorf("managed postgres policy: fleet: %w", err)
	}
	if approval := policy.ProductionApproval; approval != nil {
		if _, ok := r.backends[approval.BackendID]; !ok {
			return nil, fmt.Errorf("managed postgres policy: production_approval names unknown backend %s", approval.BackendID)
		}
		copied := *approval
		copied.ApprovedAt, copied.Until = copied.ApprovedAt.UTC(), copied.Until.UTC()
		next.approval = &copied
	}
	return &next, nil
}

// ApplyLivePolicyJSON parses and applies a runtime config value.
func (r *Registry) ApplyLivePolicyJSON(raw json.RawMessage) error {
	policy, err := ParseLivePolicy(raw)
	if err != nil {
		return err
	}
	return r.ApplyLivePolicy(policy)
}

// ProvisioningEnabled is the operator's switch for new databases and work on
// existing ones (NewOperationsGate).
func (r *Registry) ProvisioningEnabled() bool {
	return r.effective().provisioningEnabled
}

// MaxDatabasesPerAccount is the operator's per-account database limit;
// admission takes the lower of it and the plan's.
func (r *Registry) MaxDatabasesPerAccount() int {
	return r.effective().maxDatabasesPerAccount
}

// ProductionApproval is the live approval when the operator set one, else
// fallback (the FAAS_MANAGED_POSTGRES_PRODUCTION_* environment).
func (r *Registry) ProductionApproval(fallback ProductionApproval) ProductionApproval {
	if approval := r.effective().approval; approval != nil {
		return *approval
	}
	return fallback
}
