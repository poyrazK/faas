package operations

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

var ErrAdmissionClosed = errors.New("new operation admission is disabled")

// PreviewPolicy is operator intent, independent of customer schemas and plans.
// Enabled policies require exact account/app/environment/customer bindings and
// a bounded UTC window. The zero value never permits admission.
type PreviewPolicy struct {
	Version   int             `json:"version"`
	Enabled   bool            `json:"enabled"`
	NotBefore time.Time       `json:"not_before,omitempty"`
	ExpiresAt time.Time       `json:"expires_at,omitempty"`
	Cohorts   []PreviewCohort `json:"cohorts,omitempty"`
}

type PreviewCohort struct {
	AccountID         string   `json:"account_id"`
	AppID             string   `json:"app_id"`
	Scope             string   `json:"scope"`
	PlatformTenantIDs []string `json:"platform_tenant_ids"`
	// Omitted kinds retain the legacy HTTP preview only. Native families
	// require an explicit operator choice in this exact cohort.
	ExecutionKinds []string `json:"execution_kinds,omitempty"`
}

// PreviewAdmission reads a small regular file on every admission. It retains
// no last-known-open state: removal, expiry, malformed replacement or lost read
// permission closes admission. Atomic rename can change policy without restart.
type PreviewAdmission struct {
	Path string
	Now  func() time.Time
}

func NewPreviewAdmission(path string) (*PreviewAdmission, error) {
	gate := &PreviewAdmission{Path: path}
	if path == "" {
		return gate, nil
	}
	if !filepath.IsAbs(path) {
		return nil, fmt.Errorf("operations preview policy path must be absolute")
	}
	// File failures close admission without preventing retained reads/reporting
	// or scheduler recovery after a daemon restart.
	return gate, nil
}

func (g *PreviewAdmission) AllowsDefinition(account, app, scope string) bool {
	return g.AllowsDefinitionKinds(account, app, scope, []string{ExecutionHTTP})
}

func (g *PreviewAdmission) AllowsTenant(account, app, scope, tenant string) bool {
	return g.AllowsTenantKind(account, app, scope, tenant, ExecutionHTTP)
}

// AllowsDefinitionKinds checks a complete registration batch against one
// policy snapshot, so mixed deployments cannot partially widen admission.
func (g *PreviewAdmission) AllowsDefinitionKinds(account, app, scope string, kinds []string) bool {
	if len(kinds) == 0 {
		return false
	}
	observation := g.ObserveCohort(account, app, scope, "")
	for _, kind := range kinds {
		if !observation.ForExecutionKind(kind).Allowed {
			return false
		}
	}
	return true
}

func (g *PreviewAdmission) AllowsTenantKind(account, app, scope, tenant, kind string) bool {
	return tenant != "" && g.ObserveKind(account, app, scope, tenant, kind).Allowed
}

// PreviewObservation exposes only the selected cohort decision, never policy
// contents, other tenant identities or filesystem failure details.
type PreviewObservation struct {
	Allowed        bool
	Code           string
	ObservedAt     time.Time
	executionKinds []string
}

// ForExecutionKind preserves the observation's snapshot and time. Cohort
// membership alone never grants admission for an execution family.
func (o PreviewObservation) ForExecutionKind(kind string) PreviewObservation {
	if !o.Allowed {
		return o
	}
	if !validExecutionKind(kind) {
		o.Allowed, o.Code = false, "preview_execution_kind_invalid"
	} else if !slices.Contains(o.executionKinds, kind) {
		o.Allowed, o.Code = false, "preview_execution_kind_excluded"
	}
	return o
}

func (g *PreviewAdmission) Observe(account, app, scope, tenant string) PreviewObservation {
	return g.ObserveKind(account, app, scope, tenant, ExecutionHTTP)
}

func (g *PreviewAdmission) ObserveKind(account, app, scope, tenant, kind string) PreviewObservation {
	return g.ObserveCohort(account, app, scope, tenant).ForExecutionKind(kind)
}

// ObserveCohort reads once for diagnostics that inspect several definitions.
// Call ForExecutionKind before treating this as execution eligibility.
func (g *PreviewAdmission) ObserveCohort(account, app, scope, tenant string) PreviewObservation {
	now := time.Now().UTC()
	if g != nil && g.Now != nil {
		now = g.Now().UTC()
	}
	result := PreviewObservation{Code: "preview_not_configured", ObservedAt: now}
	if g == nil || g.Path == "" {
		return result
	}
	policy, err := g.read()
	if err != nil {
		result.Code = "preview_policy_unavailable"
		return result
	}
	if !policy.Enabled {
		result.Code = "preview_disabled"
		return result
	}
	// Observe time after the read, as admission did before diagnostics existed.
	now = time.Now().UTC()
	if g.Now != nil {
		now = g.Now().UTC()
	}
	result.ObservedAt = now
	if now.Before(policy.NotBefore) {
		result.Code = "preview_not_started"
		return result
	}
	if !now.Before(policy.ExpiresAt) {
		result.Code = "preview_expired"
		return result
	}
	result.Code = "preview_cohort_excluded"
	for _, cohort := range policy.Cohorts {
		if !storedUUIDMatches(cohort.AccountID, account) || !storedUUIDMatches(cohort.AppID, app) || cohort.Scope != scope {
			continue
		}
		if tenant == "" {
			result.Allowed, result.Code = true, "preview_cohort_observed"
			result.executionKinds = cohort.executionKinds()
			return result
		}
		for _, id := range cohort.PlatformTenantIDs {
			if storedUUIDMatches(id, tenant) {
				result.Allowed, result.Code = true, "preview_cohort_observed"
				result.executionKinds = cohort.executionKinds()
				return result
			}
		}
	}
	return result
}

func (g *PreviewAdmission) read() (PreviewPolicy, error) {
	// NONBLOCK prevents a misconfigured FIFO from hanging an admission worker.
	f, err := os.OpenFile(g.Path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return PreviewPolicy{}, err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0022 != 0 || info.Size() > api.OperationPreviewPolicyMaxBytes {
		return PreviewPolicy{}, fmt.Errorf("preview policy must be a bounded regular file without group/world write permission")
	}
	raw, err := io.ReadAll(io.LimitReader(f, api.OperationPreviewPolicyMaxBytes+1))
	if err != nil || len(raw) > api.OperationPreviewPolicyMaxBytes {
		return PreviewPolicy{}, fmt.Errorf("read bounded preview policy")
	}
	canonical, err := CanonicalJSON(raw)
	if err != nil {
		return PreviewPolicy{}, fmt.Errorf("preview policy JSON is invalid")
	}
	var policy PreviewPolicy
	decoder := json.NewDecoder(bytes.NewReader(canonical))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&policy) != nil {
		return PreviewPolicy{}, fmt.Errorf("preview policy must match its closed contract")
	}
	return policy, policy.validate()
}

func (p PreviewPolicy) validate() error {
	if p.Version != 1 || len(p.Cohorts) > api.OperationPreviewCohortsMax {
		return fmt.Errorf("preview policy version or cohort count is invalid")
	}
	if !p.Enabled {
		return nil
	}
	_, startOffset := p.NotBefore.Zone()
	_, endOffset := p.ExpiresAt.Zone()
	window := p.ExpiresAt.Sub(p.NotBefore)
	if p.NotBefore.IsZero() || p.ExpiresAt.IsZero() || startOffset != 0 || endOffset != 0 || window <= 0 || window > api.OperationPreviewWindowMax || len(p.Cohorts) == 0 {
		return fmt.Errorf("enabled preview requires a bounded UTC window and explicit cohorts")
	}
	seen := map[string]bool{}
	for _, cohort := range p.Cohorts {
		key := cohort.AccountID + "/" + cohort.AppID + "/" + cohort.Scope
		if !canonicalUUID(cohort.AccountID) || !canonicalUUID(cohort.AppID) || api.ValidateScope(cohort.Scope) != nil || seen[key] || len(cohort.PlatformTenantIDs) == 0 || len(cohort.PlatformTenantIDs) > api.OperationPreviewTenantsPerCohortMax {
			return fmt.Errorf("preview cohorts require unique exact account/app/scope bindings and bounded customer lists")
		}
		seen[key] = true
		if err := cohort.validateExecutionKinds(); err != nil {
			return err
		}
		tenants := map[string]bool{}
		for _, tenant := range cohort.PlatformTenantIDs {
			if !canonicalUUID(tenant) || tenants[tenant] {
				return fmt.Errorf("preview customer IDs must be unique canonical UUIDs")
			}
			tenants[tenant] = true
		}
	}
	return nil
}

func canonicalUUID(value string) bool {
	id, err := uuid.Parse(value)
	return err == nil && id != uuid.Nil && id.String() == value
}

// Store-owned IDs have the same UUID identity in PostgreSQL's canonical form
// and MemStore's compact encoding. Policy input itself remains canonical only.
func storedUUIDMatches(canonical, stored string) bool {
	id, err := uuid.Parse(stored)
	return err == nil && id.String() == canonical
}
