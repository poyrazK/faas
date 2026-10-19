package state

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/api"
)

var ErrMCPReadinessRequired = errors.New("fresh MCP readiness evidence required")

func IsMCPReadinessRequired(err error) bool {
	var pg *pgconn.PgError
	return errors.Is(err, ErrMCPReadinessRequired) || (errors.As(err, &pg) && pg.ConstraintName == "deployment_mcp_readiness_required")
}

type MCPReadinessSubject struct {
	Scope                                          string
	Policy                                         api.MCPReadinessPolicy
	DeploymentID, AppID, AccountID, ArtifactSHA256 string
	CapturedAt                                     time.Time
}
type MCPReadinessStore interface {
	GetMCPReadinessPolicy(context.Context, string, string, ...string) (api.MCPReadinessPolicy, error)
	SetMCPReadinessPolicy(context.Context, string, string, api.SetMCPReadinessPolicyRequest) (api.MCPReadinessPolicy, error)
	MCPReadinessSubject(context.Context, string, string) (MCPReadinessSubject, error)
	RecordMCPReadiness(context.Context, MCPReadinessSubject, api.MCPReadinessEvidence) (api.MCPReadinessEvidence, error)
	ListMCPReadiness(context.Context, string, string, string, ...string) (api.MCPReadinessSnapshot, error)
	CreateMCPReadinessOverride(context.Context, string, string, api.MCPReadinessOverrideRequest) (api.MCPReadinessOverride, error)
}
type memMCPReadiness struct {
	policies  map[string]api.MCPReadinessPolicy
	history   []readinessPolicyHistoryEntry
	evidence  map[string]api.MCPReadinessEvidence
	overrides []api.MCPReadinessOverride
	monitors  map[string]MCPReadinessMonitorJob
}

func cloneReadiness[T any](value T) T {
	data, _ := json.Marshal(value)
	var result T
	_ = json.Unmarshal(data, &result)
	return result
}
func defaultMCPReadinessPolicy(scopes ...string) api.MCPReadinessPolicy {
	return api.MCPReadinessPolicy{Scope: readinessScope(scopes), Endpoint: "/mcp", MaxAgeSeconds: 300, Roles: []api.MCPReadinessRole{}}
}
func (m *MemStore) readinessLocked() *memMCPReadiness {
	if m.mcpReadiness == nil {
		m.mcpReadiness = &memMCPReadiness{policies: map[string]api.MCPReadinessPolicy{}, evidence: map[string]api.MCPReadinessEvidence{}}
	}
	return m.mcpReadiness
}
func (m *MemStore) readinessPolicyLocked(app string, scopes ...string) api.MCPReadinessPolicy {
	scope := readinessScope(scopes)
	p, ok := m.readinessLocked().policies[readinessPolicyKey(app, scope)]
	if !ok {
		return defaultMCPReadinessPolicy(scope)
	}
	p.Scope = scope
	return cloneReadiness(p)
}
func (m *MemStore) readinessAppLocked(account, app string) bool {
	a, ok := m.apps[app]
	return ok && a.AccountID == account && a.Status != AppDeleted
}
func (m *MemStore) GetMCPReadinessPolicy(_ context.Context, account, app string, scopes ...string) (api.MCPReadinessPolicy, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.readinessAppLocked(account, app) {
		return api.MCPReadinessPolicy{}, ErrNotFound
	}
	if api.ValidateScope(readinessScope(scopes)) != nil {
		return api.MCPReadinessPolicy{}, ErrInvalidArgument
	}
	return m.readinessPolicyLocked(app, readinessScope(scopes)), nil
}
func (m *MemStore) SetMCPReadinessPolicy(ctx context.Context, account, app string, r api.SetMCPReadinessPolicyRequest) (api.MCPReadinessPolicy, error) {
	return m.setMCPReadinessPolicy(ctx, account, app, r, "")
}
func (m *MemStore) setMCPReadinessPolicy(_ context.Context, account, app string, r api.SetMCPReadinessPolicyRequest, restore string) (api.MCPReadinessPolicy, error) {
	if restore == "" && api.ValidateMCPReadinessPolicy(r) != nil {
		return api.MCPReadinessPolicy{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.readinessAppLocked(account, app) {
		return api.MCPReadinessPolicy{}, ErrNotFound
	}
	if restore != "" {
		old, err := m.readinessHistoryPolicyLocked(app, api.MCPConnectionScope(r.Scope), restore)
		if err != nil {
			return api.MCPReadinessPolicy{}, err
		}
		r = readinessRestoreRequest(old, r.ExpectedRevision, r.Reason)
		if api.ValidateMCPReadinessPolicy(r) != nil {
			return api.MCPReadinessPolicy{}, ErrInvalidArgument
		}
	}
	if m.readinessPolicyLocked(app, r.Scope).Revision != r.ExpectedRevision {
		return api.MCPReadinessPolicy{}, ErrConflict
	}
	root := m.mcpCatalogMonitors[app]
	if m.mcpCatalogMonitorOwners[app] != account {
		root = api.MCPCatalogMonitorSnapshot{}
	}
	if err := validateReadinessOAuthReferences(root, r); err != nil {
		return api.MCPReadinessPolicy{}, err
	}
	p := newMCPReadinessPolicy(account, r)
	p.RestoredFrom = restore
	m.readinessLocked().policies[readinessPolicyKey(app, r.Scope)] = cloneReadiness(p)
	m.mcpReadiness.history = append(m.mcpReadiness.history, readinessPolicyHistoryEntry{AppID: app, Policy: cloneReadiness(p)})
	return p, nil
}
func newMCPReadinessPolicy(account string, r api.SetMCPReadinessPolicyRequest) api.MCPReadinessPolicy {
	return api.MCPReadinessPolicy{Scope: api.MCPConnectionScope(r.Scope), Revision: uuid.NewString(), Enabled: r.Enabled, Endpoint: r.Endpoint, MaxAgeSeconds: r.MaxAgeSeconds, Roles: append([]api.MCPReadinessRole{}, r.Roles...), Reason: r.Reason, UpdatedAt: time.Now().UTC(), UpdatedBy: account}
}
func readinessArtifact(d Deployment, a App, secrets map[secretKey]AppSecret) string {
	data, _ := json.Marshal(d)
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(data, &fields)
	chosen := map[string]any{}
	for _, key := range []string{"ID", "AppID", "ImageDigest", "BuildID", "RootfsPath", "RootfsKey", "SourceSHA256", "CommitSHA", "Scope", "OverrideEntrypoint", "OverrideCmd", "OverrideEnv", "OverrideEnvSecrets", "OverridePort", "OverrideHealthcheck", "OverrideLivenessProbe", "Sidecars"} {
		chosen[key] = fields[key]
	}
	chosen["manifest"] = a.Manifest
	appData, _ := json.Marshal(a)
	var appFields map[string]json.RawMessage
	_ = json.Unmarshal(appData, &appFields)
	for _, key := range []string{"Slug", "Runtime", "StartCommand", "RequireAuthn", "StreamingEnabled", "PublicAuthMode", "PublicAuthBasic", "PublicAuthIPAllowlist", "Visibility", "AppProtocol", "ConsumerAuthMode", "OnlyDeclaredRoutes", "DeclaredRoutes"} {
		chosen["app_"+key] = appFields[key]
	}

	versions := map[string]int64{}
	for _, s := range secrets {
		if s.AppID == d.AppID && normalizedDeploymentScope(s.Scope) == normalizedDeploymentScope(d.Scope) {
			versions[s.Key] = s.DeliveryVersion
		}
	}
	chosen["secret_versions"] = versions
	data, _ = json.Marshal(chosen)
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}
func (m *MemStore) readinessSubjectLocked(account, id string, allowInactive ...bool) (MCPReadinessSubject, error) {
	d, ok := m.deployments[id]
	if !ok {
		for key, candidate := range m.deployments {
			if sameDeploymentID(key, id) {
				d = candidate
				id = key
				ok = true
				break
			}
		}
	}
	if !ok || !m.readinessAppLocked(account, d.AppID) {
		return MCPReadinessSubject{}, ErrNotFound
	}
	if d.Status != DeployLive && (len(allowInactive) == 0 || !allowInactive[0]) {
		return MCPReadinessSubject{}, ErrConflict
	}
	return MCPReadinessSubject{Scope: d.Scope, Policy: m.readinessPolicyLocked(d.AppID, d.Scope), DeploymentID: id, AppID: d.AppID, AccountID: account, ArtifactSHA256: m.readinessArtifactLocked(d), CapturedAt: time.Now().UTC()}, nil
}
func (m *MemStore) MCPReadinessSubject(_ context.Context, account, id string) (MCPReadinessSubject, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.readinessSubjectLocked(account, id)
}
func validateReadinessEvidence(s MCPReadinessSubject, e api.MCPReadinessEvidence) bool {
	if len(e.Checks) > 16 || len(e.Role) > 64 || s.CapturedAt.IsZero() || time.Since(s.CapturedAt) > 45*time.Second || s.CapturedAt.After(time.Now().Add(time.Second)) {
		return false
	}
	for _, r := range s.Policy.Roles {
		if r.Name == e.Role {
			return !e.Passed || (e.ProtocolVersion == r.ProtocolVersion && e.CatalogFormat == r.CatalogFormat && e.CatalogSHA256 == r.CatalogSHA256)
		}
	}
	return false
}
func stampReadinessEvidence(s MCPReadinessSubject, e api.MCPReadinessEvidence) api.MCPReadinessEvidence {
	e.ID = uuid.NewString()
	e.DeploymentID = s.DeploymentID
	e.PolicyRevision = s.Policy.Revision
	e.ArtifactSHA256 = s.ArtifactSHA256
	e.ObservedAt = s.CapturedAt
	return cloneReadiness(e)
}
func (m *MemStore) RecordMCPReadiness(_ context.Context, s MCPReadinessSubject, e api.MCPReadinessEvidence) (api.MCPReadinessEvidence, error) {
	if !validateReadinessEvidence(s, e) {
		return api.MCPReadinessEvidence{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.recordMCPReadinessLocked(s, e)
}
func (m *MemStore) recordMCPReadinessLocked(s MCPReadinessSubject, e api.MCPReadinessEvidence) (api.MCPReadinessEvidence, error) {
	current, err := m.readinessSubjectLocked(s.AccountID, s.DeploymentID)
	if err != nil {
		return api.MCPReadinessEvidence{}, err
	}
	if current.Policy.Revision != s.Policy.Revision || current.ArtifactSHA256 != s.ArtifactSHA256 {
		return api.MCPReadinessEvidence{}, ErrConflict
	}
	if !validateReadinessEvidence(current, e) {
		return api.MCPReadinessEvidence{}, ErrInvalidArgument
	}

	key := s.DeploymentID + "/" + e.Role
	if prior, ok := m.readinessLocked().evidence[key]; ok && (prior.ObservedAt.After(s.CapturedAt) || (prior.ObservedAt.Equal(s.CapturedAt) && e.Passed)) {
		return api.MCPReadinessEvidence{}, ErrConflict
	}
	e = stampReadinessEvidence(s, e)
	m.mcpReadiness.evidence[key] = e
	return cloneReadiness(e), nil
}
func (m *MemStore) ListMCPReadiness(_ context.Context, account, app, id string, scopes ...string) (api.MCPReadinessSnapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.readinessAppLocked(account, app) {
		return api.MCPReadinessSnapshot{}, ErrNotFound
	}
	scope := readinessScope(scopes)
	if api.ValidateScope(scope) != nil {
		return api.MCPReadinessSnapshot{}, ErrInvalidArgument
	}
	if id != "" {
		d, ok := m.deployments[id]
		if !ok || d.AppID != app || len(scopes) > 0 && api.MCPConnectionScope(d.Scope) != scope {
			return api.MCPReadinessSnapshot{}, ErrNotFound
		}
		scope = api.MCPConnectionScope(d.Scope)
	}
	out := api.MCPReadinessSnapshot{Policy: m.readinessPolicyLocked(app, scope), Evidence: []api.MCPReadinessEvidence{}, Overrides: []api.MCPReadinessOverride{}}
	for _, e := range m.readinessLocked().evidence {
		if d, ok := m.deployments[e.DeploymentID]; ok && d.AppID == app && api.MCPConnectionScope(d.Scope) == scope && (id == "" || id == e.DeploymentID) {
			out.Evidence = append(out.Evidence, e)
		}
	}
	sort.Slice(out.Evidence, func(i, j int) bool { return out.Evidence[i].ObservedAt.After(out.Evidence[j].ObservedAt) })
	if len(out.Evidence) > 50 {
		out.Evidence = out.Evidence[:50]
	}
	for i := len(m.mcpReadiness.overrides) - 1; i >= 0 && len(out.Overrides) < 50; i-- {
		o := m.mcpReadiness.overrides[i]
		if d, ok := m.deployments[o.DeploymentID]; ok && d.AppID == app && api.MCPConnectionScope(d.Scope) == scope && (id == "" || id == o.DeploymentID) {
			out.Overrides = append(out.Overrides, o)
		}
	}
	return cloneReadiness(out), nil
}
func (m *MemStore) CreateMCPReadinessOverride(_ context.Context, account, id string, r api.MCPReadinessOverrideRequest) (api.MCPReadinessOverride, error) {
	if len(r.Reason) < 1 || len(r.Reason) > 512 {
		return api.MCPReadinessOverride{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s, err := m.readinessSubjectLocked(account, id, true)
	if err != nil {
		return api.MCPReadinessOverride{}, err
	}
	if !s.Policy.Enabled || s.Policy.Revision != r.ExpectedRevision {
		return api.MCPReadinessOverride{}, ErrConflict
	}
	id = s.DeploymentID
	for _, pending := range m.readinessLocked().overrides {
		if pending.DeploymentID == id && pending.PolicyRevision == s.Policy.Revision && pending.ArtifactSHA256 == s.ArtifactSHA256 && pending.UsedAt == nil && time.Now().Before(pending.ExpiresAt) {
			return api.MCPReadinessOverride{}, ErrConflict
		}
	}
	o := api.MCPReadinessOverride{ID: uuid.NewString(), DeploymentID: id, PolicyRevision: s.Policy.Revision, ArtifactSHA256: s.ArtifactSHA256, Reason: r.Reason, CreatedBy: account, CreatedAt: s.CapturedAt, ExpiresAt: s.CapturedAt.Add(5 * time.Minute)}
	m.readinessLocked().overrides = append(m.mcpReadiness.overrides, o)
	return o, nil
}
func (m *MemStore) checkMCPReadinessLocked(d Deployment, percent int) error {
	if percent <= 0 || (d.Status == DeployLive && percent <= d.TrafficPercent) {
		return nil
	}
	p := m.readinessPolicyLocked(d.AppID, d.Scope)
	if m.readinessMissingPolicyLocked(d, p) {
		return ErrMCPReadinessRequired
	}
	if !p.Enabled {
		return nil
	}
	hash := m.readinessArtifactLocked(d)
	now := time.Now()
	for _, o := range m.readinessLocked().overrides {
		if o.DeploymentID == d.ID && o.PolicyRevision == p.Revision && o.ArtifactSHA256 == hash && o.UsedAt == nil && now.Before(o.ExpiresAt) {
			return nil
		}
	}
	for _, role := range p.Roles {
		e, ok := m.readinessLocked().evidence[d.ID+"/"+role.Name]
		if !ok || !e.Passed || e.PolicyRevision != p.Revision || e.ArtifactSHA256 != hash || now.Sub(e.ObservedAt) > time.Duration(p.MaxAgeSeconds)*time.Second || e.ObservedAt.After(now) {
			return ErrMCPReadinessRequired
		}
	}
	return nil
}
func (m *MemStore) consumeMCPReadinessOverrideLocked(prev, d Deployment) {
	if d.Status != DeployLive || d.TrafficPercent <= 0 || (prev.Status == DeployLive && d.TrafficPercent <= prev.TrafficPercent) || m.mcpReadiness == nil {
		return
	}
	p := m.readinessPolicyLocked(d.AppID, d.Scope)
	hash := m.readinessArtifactLocked(d)
	now := time.Now().UTC()
	for i := len(m.mcpReadiness.overrides) - 1; i >= 0; i-- {
		o := &m.mcpReadiness.overrides[i]
		if o.DeploymentID == d.ID && o.PolicyRevision == p.Revision && o.ArtifactSHA256 == hash && o.UsedAt == nil && now.Before(o.ExpiresAt) {
			o.UsedAt = &now
			return
		}
	}
}

func readinessScope(scopes []string) string {
	if len(scopes) == 0 {
		return DefaultEnvScope
	}
	return api.MCPConnectionScope(scopes[0])
}
func readinessPolicyKey(app, scope string) string {
	scope = api.MCPConnectionScope(scope)
	if scope == DefaultEnvScope {
		return app
	}
	return app + "/" + scope
}
func (m *MemStore) readinessMissingPolicyLocked(d Deployment, p api.MCPReadinessPolicy) bool {
	if api.MCPConnectionScope(d.Scope) == DefaultEnvScope || p.Revision != "" {
		return false
	}
	for key := range m.readinessLocked().policies {
		if key == d.AppID || len(key) > len(d.AppID) && key[:len(d.AppID)+1] == d.AppID+"/" {
			return true
		}
	}
	return false
}
