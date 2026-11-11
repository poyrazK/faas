// Adapters that bridge cmd/apid's concrete types (state.Store,
// *auditor) to the pkg/auth interfaces. Lives alongside
// auth_facade.go so the bridge is one place a future reader can
// audit. ADR-044.
//
// state.Store already has every method pkg/auth.Authenticator
// needs (AuthenticateKey, AccountByID, AppBySlug, TouchKeyLastUsed)
// with the right signatures, so the adapter is a free pass-through
// — no wrapping or translation. We expose it as a named function
// so the call site is searchable ("where do we declare the auth
// interface conformance?") instead of an inline cast.
package apidserver

import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/authz"
	"github.com/onebox-faas/faas/pkg/state"
)

// storeAsAuthenticator returns middleware.Authenticator as a view over a
// state.Store. The view is the same pointer; no copy.
func storeAsAuthenticator(s state.Store) middleware.Authenticator {
	return storeAuthAdapter{s}
}

// storeAuthAdapter is the unexported type that satisfies
// middleware.Authenticator by delegating to a state.Store. Defined here
// (not via a method set on state.Store itself) so pkg/auth doesn't
// force state.Store to know about the auth interface.
type storeAuthAdapter struct{ state.Store }

// The deploy-token methods live on the concrete stores, not on the
// state.Store interface, so the embedded interface does not promote them.
// Without these forwards the middleware's DeployTokenAuthenticator
// assertion fails and every deploy token is rejected with a bare 401.
var _ middleware.DeployTokenAuthenticator = storeAuthAdapter{}

// ProjectByOrgSlug lives on the optional ProjectOwnershipStore, so it is
// forwarded for the middleware's organization project limit
// (ADR-20261104085712).
var _ middleware.OrgProjectResolver = storeAuthAdapter{}

func (a storeAuthAdapter) ProjectByOrgSlug(ctx context.Context, orgID, slug string) (state.Project, error) {
	owners, ok := a.Store.(state.ProjectOwnershipStore)
	if !ok {
		return state.Project{}, state.ErrNotFound
	}
	return owners.ProjectByOrgSlug(ctx, orgID, slug)
}

type deployTokenStore interface {
	middleware.DeployTokenAuthenticator
	TouchDeployTokenLastUsed(ctx context.Context, tokenID string) error
}

func (a storeAuthAdapter) AuthenticateDeployToken(ctx context.Context, hash []byte) (state.Account, state.APIKey, error) {
	d, ok := a.Store.(deployTokenStore)
	if !ok {
		return state.Account{}, state.APIKey{}, state.ErrNotFound
	}
	return d.AuthenticateDeployToken(ctx, hash)
}

func (a storeAuthAdapter) TouchDeployTokenLastUsed(ctx context.Context, tokenID string) error {
	d, ok := a.Store.(deployTokenStore)
	if !ok {
		return nil
	}
	return d.TouchDeployTokenLastUsed(ctx, tokenID)
}

// A deploy token follows the deployments of its own app
// (ADR-20261031162858); the middleware binds those reads by their app.
var _ middleware.DeploymentAppResolver = storeAuthAdapter{}

func (a storeAuthAdapter) DeploymentAppID(ctx context.Context, deploymentID string) (string, error) {
	d, err := a.DeploymentByID(ctx, deploymentID)
	if err != nil {
		return "", err
	}
	return d.AppID, nil
}

// A key limited to projects reaches only their apps, projects and deployments
// (ADR-20261104085712).
var (
	_ middleware.KeyProjectLimiter = storeAuthAdapter{}
	_ middleware.ProjectResolver   = storeAuthAdapter{}
	_ middleware.AppByIDResolver   = storeAuthAdapter{}
)

// APIKeyProjectLimit forwards the optional limit store; a store without it
// limits no key.
func (a storeAuthAdapter) APIKeyProjectLimit(ctx context.Context, keyID string) ([]string, bool, error) {
	store, ok := a.Store.(state.KeyProjectLimitStore)
	if !ok {
		return nil, false, nil
	}
	return store.APIKeyProjectLimit(ctx, keyID)
}

// An agent key's risky requests wait for a person's approval
// (ADR-20261104140058).
var (
	_ middleware.AgentApprovalGate    = storeAuthAdapter{}
	_ middleware.OrgAgentPolicyReader = storeAuthAdapter{}
)

// errNoApprovalStore fails a gated request closed on a store that cannot
// record approvals.
var errNoApprovalStore = errors.New("store has no agent approvals")

func (a storeAuthAdapter) APIKeyApprovalCategories(ctx context.Context, keyID string) ([]string, bool, error) {
	store, ok := a.Store.(state.AgentApprovalStore)
	if !ok {
		return nil, false, nil
	}
	return store.APIKeyApprovalCategories(ctx, keyID)
}

func (a storeAuthAdapter) ConsumeAgentApproval(ctx context.Context, binding state.AgentApprovalBinding) (string, bool, error) {
	store, ok := a.Store.(state.AgentApprovalStore)
	if !ok {
		return "", false, errNoApprovalStore
	}
	return store.ConsumeAgentApproval(ctx, binding)
}

func (a storeAuthAdapter) RecentAgentApprovalOutcome(ctx context.Context, binding state.AgentApprovalBinding, window time.Duration) (state.AgentApproval, bool, error) {
	store, ok := a.Store.(state.AgentApprovalStore)
	if !ok {
		return state.AgentApproval{}, false, errNoApprovalStore
	}
	return store.RecentAgentApprovalOutcome(ctx, binding, window)
}

func (a storeAuthAdapter) RequestAgentApproval(ctx context.Context, request state.AgentApproval) (state.AgentApproval, error) {
	store, ok := a.Store.(state.AgentApprovalStore)
	if !ok {
		return state.AgentApproval{}, errNoApprovalStore
	}
	return store.RequestAgentApproval(ctx, request)
}

// An agent key's write requests are recorded (ADR-20261107220000).
var _ middleware.AgentActivityRecorder = storeAuthAdapter{}

// RecordAgentActivity forwards to the optional activity store; a store
// without it records nothing.
func (a storeAuthAdapter) RecordAgentActivity(ctx context.Context, row state.AgentActivity) error {
	store, ok := a.Store.(state.AgentActivityStore)
	if !ok {
		return nil
	}
	return store.RecordAgentActivity(ctx, row)
}

var _ state.AgentBudgetStore = storeAuthAdapter{}

// agentBudgets is the optional budget store (ADR-20261111114059).
func (a storeAuthAdapter) agentBudgets() (state.AgentBudgetStore, bool) {
	store, ok := a.Store.(state.AgentBudgetStore)
	return store, ok
}

// AgentKeyBudget forwards the key's budget; a store without budgets has
// none set, so the plan default applies.
func (a storeAuthAdapter) AgentKeyBudget(ctx context.Context, keyID string) (state.AgentKeyBudget, error) {
	if store, ok := a.agentBudgets(); ok {
		return store.AgentKeyBudget(ctx, keyID)
	}
	return state.AgentKeyBudget{}, nil
}

// SetAgentKeyBudget forwards a budget change.
func (a storeAuthAdapter) SetAgentKeyBudget(ctx context.Context, keyID string, budget state.AgentKeyBudget, updatedBy string) error {
	if store, ok := a.agentBudgets(); ok {
		return store.SetAgentKeyBudget(ctx, keyID, budget, updatedBy)
	}
	return state.ErrNotFound
}

// AgentBudgetSpent forwards the key's spend this month.
func (a storeAuthAdapter) AgentBudgetSpent(ctx context.Context, keyID string, at time.Time) (int64, error) {
	if store, ok := a.agentBudgets(); ok {
		return store.AgentBudgetSpent(ctx, keyID, at)
	}
	return 0, nil
}

// AddAgentBudgetSpend forwards recorded spend.
func (a storeAuthAdapter) AddAgentBudgetSpend(ctx context.Context, keyID string, at time.Time, millicents int64) error {
	if store, ok := a.agentBudgets(); ok {
		return store.AddAgentBudgetSpend(ctx, keyID, at, millicents)
	}
	return nil
}

// OrgAgentPolicy forwards the organization's agent policy; a store without
// it has the default policy, with no approval floor.
func (a storeAuthAdapter) OrgAgentPolicy(ctx context.Context, orgID string) (state.OrgAgentPolicy, error) {
	store, ok := a.Store.(state.OrgAgentStore)
	if !ok {
		return state.DefaultOrgAgentPolicy(orgID), nil
	}
	return store.OrgAgentPolicy(ctx, orgID)
}

// AuthenticatePlatformTenantAccessToken forwards this optional credential
// capability without making it part of the broad state.Store contract.
func (a storeAuthAdapter) AuthenticatePlatformTenantAccessToken(ctx context.Context, hash []byte) (state.Account, state.PlatformTenantAccessToken, error) {
	store, ok := a.Store.(state.PlatformTenantAccessStore)
	if !ok {
		return state.Account{}, state.PlatformTenantAccessToken{}, state.ErrNotFound
	}
	return store.AuthenticatePlatformTenantAccessToken(ctx, hash)
}

// storeAsSessionLookup returns middleware.SessionLookup as a view over a
// state.Store. The cookie-branch of pkg/auth.RequireSession uses
// this to cross-check the AEAD-bound envelope against the live
// sessions row (IAM-3 / ADR-039 replay defense) and to stamp
// last_seen_at via TouchSessionLastSeen. Both methods already
// exist on state.Store (pkg/state/{pgstore,memstore}.go); the
// adapter is a free pass-through.
func storeAsSessionLookup(s state.Store) middleware.SessionLookup {
	return storeAuthAdapter{s}
}

// auditorAsAuthAuditor returns middleware.Auditor as a view over an
// *auditor. The auditor type today (cmd/apid/handlers_audit.go)
// already has an Emit method with the right signature; this
// adapter is also a free pass-through.
func auditorAsAuthAuditor(a *auditor) middleware.Auditor {
	return auditorAuthAdapter{a}
}

type auditorAuthAdapter struct{ *auditor }

// auditorAsAuthzAuditor returns authz.AuditEmitter as a view over
// an *auditor. Issue #190 / IAM-6 / ADR-061 PR 4 — LoadOrg
// (cmd/apid/auth_facade.go::loadOrg) emits authz.denied rows when
// the membership lookup fails. The adapter is a free pass-through
// because *auditor.Emit and authz.AuditEmitter.Emit share the same
// (ctx, event, *string, map[string]any) signature.
func auditorAsAuthzAuditor(a *auditor) authz.AuditEmitter {
	return auditorAuthzAdapter{a}
}

type auditorAuthzAdapter struct{ *auditor }

// Compile-time assertions: the adapters must satisfy the exported
// pkg/auth + pkg/authz interfaces. A future method added to either
// interface surfaces as a compile error here.
var (
	_ middleware.Authenticator                          = storeAuthAdapter{}
	_ middleware.SessionLookup                          = storeAuthAdapter{}
	_ middleware.PlatformTenantAccessTokenAuthenticator = storeAuthAdapter{}
	_ middleware.Auditor                                = auditorAuthAdapter{}
	_ authz.AuditEmitter                                = auditorAuthzAdapter{}
)

func (a storeAuthAdapter) AuthenticateHostingAutomationToken(ctx context.Context, hash []byte) (state.Account, state.APIKey, error) {
	store, ok := a.Store.(state.HostingAutomationStore)
	if !ok {
		return state.Account{}, state.APIKey{}, state.ErrNotFound
	}
	return store.AuthenticateHostingAutomationToken(ctx, hash)
}
