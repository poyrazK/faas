// Package platformflagadmin is apid's read surface for platform flags
// (ADR-1700): the catalog with live values and per-daemon apply receipts, the
// decision inspector, and the per-account view. Writes are not here; they go
// through PATCH /v1/admin/config/{config_key}, which already carries the
// operator allowlist, MFA step-up, reason, optimistic version and audit.
//
// Everything it needs from apid goes through Host, which apid implements by
// reading its own fields at call time.
package platformflagadmin

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/state"
)

// Host is what the platform-flag surface needs from apid.
type Host interface {
	Store() state.Store
	// PlatformFlags is apid's compiled flag set; nil evaluates every flag
	// as off.
	PlatformFlags() *platformflags.Set
	// AdminAllows applies the operator allowlist.
	AdminAllows(acct state.Account) (bool, *api.Problem)
	Now() time.Time
}

// Service serves the platform-flag read endpoints.
type Service struct {
	Host Host
}

// List handles GET /v1/admin/flags.
func (s *Service) List(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.allowed(w, acct) {
		return
	}
	view, err := s.load(r.Context())
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not load platform flags"))
		return
	}
	items := make([]api.OperatorPlatformFlag, 0, len(view.specs))
	for _, spec := range view.specs {
		items = append(items, view.flag(spec))
	}
	httpkit.WriteJSON(w, http.StatusOK, api.OperatorPlatformFlagList{
		Items: items, Groups: view.groups(), GeneratedAt: s.Host.Now().UTC().Format(time.RFC3339),
	})
}

// Get handles GET /v1/admin/flags/{key}.
func (s *Service) Get(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.allowed(w, acct) {
		return
	}
	spec, ok := lookupSpec(w, r)
	if !ok {
		return
	}
	view, err := s.load(r.Context())
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not load platform flags"))
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, view.flag(spec))
}

// Decision handles GET /v1/admin/flags/{key}/decision?account_id=: the
// inspector. It evaluates apid's live snapshot and records nothing.
func (s *Service) Decision(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.allowed(w, acct) {
		return
	}
	spec, ok := lookupSpec(w, r)
	if !ok {
		return
	}
	target, ok := s.loadAccount(w, r, r.URL.Query().Get("account_id"))
	if !ok {
		return
	}
	orgID := strings.TrimSpace(r.URL.Query().Get("org_id"))
	if orgID != "" {
		id, err := uuid.Parse(orgID)
		if err != nil {
			api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Invalid organization ID", "org_id must be an organization UUID"))
			return
		}
		orgID = id.String()
	}
	httpkit.WriteJSON(w, http.StatusOK, decide(s.Host.PlatformFlags().Snapshot(), spec, target, orgID))
}

// AccountFlags handles GET /v1/admin/accounts/{id}/flags: every platform
// flag decided for one account.
func (s *Service) AccountFlags(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.allowed(w, acct) {
		return
	}
	target, ok := s.loadAccount(w, r, r.PathValue("id"))
	if !ok {
		return
	}
	snapshot := s.Host.PlatformFlags().Snapshot()
	specs := platformflags.Catalog()
	items := make([]api.OperatorPlatformFlagDecision, 0, len(specs))
	for _, spec := range specs {
		items = append(items, decide(snapshot, spec, target, ""))
	}
	httpkit.WriteJSON(w, http.StatusOK, api.OperatorAccountPlatformFlags{AccountID: target.ID, Plan: string(target.Plan), Items: items})
}

func (s *Service) allowed(w http.ResponseWriter, acct state.Account) bool {
	if allowed, prob := s.Host.AdminAllows(acct); !allowed {
		api.WriteProblem(w, prob)
		return false
	}
	return true
}

func lookupSpec(w http.ResponseWriter, r *http.Request) (platformflags.Spec, bool) {
	spec, ok := platformflags.Lookup(strings.TrimSpace(r.PathValue("key")))
	if !ok {
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Unknown platform flag", "the key is not in the platform flag catalog"))
	}
	return spec, ok
}

func (s *Service) loadAccount(w http.ResponseWriter, r *http.Request, rawID string) (state.Account, bool) {
	accountID := strings.TrimSpace(rawID)
	if _, err := uuid.Parse(accountID); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Invalid account ID", "account_id must be an account UUID"))
		return state.Account{}, false
	}
	target, err := s.Host.Store().AccountByID(r.Context(), accountID)
	if errors.Is(err, state.ErrNotFound) {
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Account not found", "no account has this ID"))
		return state.Account{}, false
	}
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not load the account"))
		return state.Account{}, false
	}
	return target, true
}

// decide evaluates one flag for an account and adds the plan entitlement of
// the capability it releases.
// decide explains one flag for an account, acting under orgID when it is set
// (the organization a request would carry; ADR 20261021080640).
func decide(snapshot *platformflags.Snapshot, spec platformflags.Spec, target state.Account, orgID string) api.OperatorPlatformFlagDecision {
	d := snapshot.Evaluate(spec.Key, platformflags.Subject{AccountID: target.ID, Plan: string(target.Plan), OrgID: orgID})
	out := api.OperatorPlatformFlagDecision{
		Flag: spec.Key, AccountID: target.ID, OrgID: orgID, Plan: string(target.Plan), Enabled: d.Enabled,
		Reason: d.Reason, RuleID: d.RuleID, Bucket: d.Bucket, Version: d.Version, Available: d.Enabled,
	}
	if spec.Capability != "" {
		entitled, err := api.CapabilityEntitled(spec.Capability, target.Plan)
		entitled = err == nil && entitled
		out.Entitled = &entitled
		out.Available = d.Enabled && entitled
	}
	return out
}

// view joins the catalog, the durable rows, apid's snapshot and the
// receipts for one response.
type view struct {
	specs    []platformflags.Spec
	rows     map[string]state.RuntimeConfig
	receipts map[string][]state.RuntimeConfigApplyReceipt
	snapshot *platformflags.Snapshot
}

func (s *Service) load(ctx context.Context) (view, error) {
	v := view{specs: platformflags.Catalog(), rows: map[string]state.RuntimeConfig{}, receipts: map[string][]state.RuntimeConfigApplyReceipt{}}
	v.snapshot = s.Host.PlatformFlags().Snapshot()
	rows, err := s.Host.Store().ListRuntimeConfigs(ctx, state.RuntimeConfigScopeGlobal, "")
	if err != nil {
		return view{}, err
	}
	for _, row := range rows {
		if platformflags.IsConfigKey(row.Key) {
			v.rows[row.Key] = row
		}
	}
	receipts, err := s.Host.Store().ListRuntimeConfigApplyReceipts(ctx, platformflags.ConfigKeys())
	if err != nil {
		return view{}, err
	}
	for _, receipt := range receipts {
		v.receipts[receipt.Key] = append(v.receipts[receipt.Key], receipt)
	}
	return v, nil
}

func (v view) flag(spec platformflags.Spec) api.OperatorPlatformFlag {
	configKey := platformflags.ConfigKey(spec.Key)
	def, version, _ := v.snapshot.Definition(spec.Key)
	out := api.OperatorPlatformFlag{
		Key: spec.Key, ConfigKey: configKey, Label: spec.Label, Description: spec.Description,
		Capability: spec.Capability, ADR: spec.ADR, Consumers: spec.Consumers, Attributes: attributeNames(spec), LegacyEnv: spec.LegacyEnv,
		RetireBy: spec.RetireBy, Definition: toAPIDefinition(def), Source: "default_or_environment",
		Version: version, Status: string(state.RuntimeConfigApplied),
	}
	if row, ok := v.rows[configKey]; ok {
		out.Source, out.Status, out.LastError = "operator", string(row.Status), row.LastError
		out.UpdatedAt = row.UpdatedAt.UTC().Format(time.RFC3339)
		if row.Version > out.Version {
			// apid has not applied the newest row yet; report the durable
			// version so convergence shows every consumer as pending.
			out.Version = row.Version
		}
	}
	out.Receipts = toAPIReceipts(v.receipts[configKey])
	out.Convergence = convergence(spec.Consumers, v.receipts[configKey], out.Version, out.Source == "operator")
	return out
}

func (v view) groups() api.OperatorPlatformFlagGroups {
	groups, version := v.snapshot.Groups()
	defs := map[string]platformflags.Definition{}
	for _, spec := range v.specs {
		if def, _, ok := v.snapshot.Definition(spec.Key); ok {
			defs[spec.Key] = def
		}
	}
	return api.OperatorPlatformFlagGroups{
		ConfigKey: platformflags.GroupsConfigKey, Groups: groups,
		References: platformflags.GroupReferences(defs), Version: version,
	}
}

// convergence classifies each consumer. Without an operator value there is
// nothing to converge on: every consumer runs its boot fallback.
func convergence(consumers []string, receipts []state.RuntimeConfigApplyReceipt, version int64, durable bool) api.OperatorPlatformFlagConvergence {
	out := api.OperatorPlatformFlagConvergence{Converged: []string{}, Pending: []string{}, Failed: []string{}}
	for _, daemon := range consumers {
		if !durable {
			out.Converged = append(out.Converged, daemon)
			continue
		}
		seen, current, failed := false, true, false
		for _, receipt := range receipts {
			if receipt.Daemon != daemon {
				continue
			}
			seen = true
			if receipt.Version != version {
				current = false
			} else if receipt.Status == state.RuntimeConfigReceiptFailed {
				failed = true
			}
		}
		switch {
		case failed:
			out.Failed = append(out.Failed, daemon)
		case seen && current:
			out.Converged = append(out.Converged, daemon)
		default:
			out.Pending = append(out.Pending, daemon)
		}
	}
	return out
}

// attributeNames lists the subject attributes a flag's rules may use; the
// account is always one of them.
func attributeNames(spec platformflags.Spec) []string {
	out := []string{"account"}
	for _, attr := range spec.Attributes {
		out = append(out, string(attr))
	}
	return out
}

func toAPIDefinition(def platformflags.Definition) api.PlatformFlagDefinition {
	out := api.PlatformFlagDefinition{Enabled: def.Enabled, Default: def.Default}
	for _, rule := range def.Rules {
		out.Rules = append(out.Rules, api.PlatformFlagRule{
			ID: rule.ID, Accounts: slices.Clone(rule.Accounts), Orgs: slices.Clone(rule.Orgs), Group: rule.Group,
			Plans: slices.Clone(rule.Plans), Rollout: rule.Rollout, Value: rule.Value,
		})
	}
	return out
}

func toAPIReceipts(receipts []state.RuntimeConfigApplyReceipt) []api.OperatorRuntimeConfigApplyReceipt {
	out := make([]api.OperatorRuntimeConfigApplyReceipt, 0, len(receipts))
	for _, receipt := range receipts {
		out = append(out, api.OperatorRuntimeConfigApplyReceipt{
			ConfigKey: receipt.Key, Daemon: receipt.Daemon, Instance: receipt.Instance, Version: receipt.Version,
			Status: string(receipt.Status), Error: receipt.Error, ReportedAt: receipt.ReportedAt.UTC().Format(time.RFC3339),
		})
	}
	return out
}
