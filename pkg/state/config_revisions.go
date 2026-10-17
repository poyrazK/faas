package state

import (
	"context"
	"encoding/json"
	"maps"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// Config revision sources (app_config_revisions.source, ADR-1451).
const (
	ConfigSourceBaseline     = "baseline"
	ConfigSourceEnvSet       = "env.set"
	ConfigSourceEnvDelete    = "env.delete"
	ConfigSourceSecretSet    = "secret.set"
	ConfigSourceSecretDelete = "secret.delete"
	ConfigSourceSecretRotate = "secret.rotate"
	ConfigSourceRollback     = "config.rollback"
	ConfigSourceGuardRestore = "config.guard_restore"
)

// Config guard states (app_config_guards.status, ADR-1451).
const (
	ConfigGuardWatching   = "watching"
	ConfigGuardClean      = "clean"
	ConfigGuardRestored   = "restored"
	ConfigGuardRegressed  = "regressed"
	ConfigGuardNoTraffic  = "no_traffic"
	ConfigGuardSuperseded = "superseded"
)

// ConfigSecretFingerprint identifies a secret's value without holding it.
type ConfigSecretFingerprint struct {
	Version   int64  `json:"version"`
	ValueHash string `json:"value_hash,omitempty"`
}

// ConfigRevision is one app scope's complete config after a change.
type ConfigRevision struct {
	ID        string
	AccountID string
	AppID     string
	Scope     string
	Revision  int64
	Source    string
	Key       string
	Actor     string
	Variables map[string]string
	Secrets   map[string]ConfigSecretFingerprint
	GuardID   string
	// GuardStatus is the status of GuardID's guard on reads; empty otherwise.
	GuardStatus      string
	RestoredRevision int64
	CreatedAt        time.Time
}

// ConfigGuard watches the first traffic after a scope's config changed.
type ConfigGuard struct {
	ID           string
	AccountID    string
	AppID        string
	Scope        string
	BaseRevision int64
	HeadRevision int64
	Status       string
	Restorable   bool
	OpenedAt     time.Time
	LastChangeAt time.Time
	WindowEndsAt *time.Time
	ClosedAt     *time.Time
	Decision     json.RawMessage
}

// ConfigGuardSecret is the sealed row a guarded secret write replaced.
// Existed=false means the guard added the secret.
type ConfigGuardSecret struct {
	Key         string
	Existed     bool
	Ciphertext  []byte
	Kid         string
	ValueHash   string
	SecretClass string
}

// ConfigChangeStart precedes a customer config write. Guard opens or joins
// the scope's guard; without it, a watching guard is superseded. SecretKey
// retains that key's current sealed row in the guard (first capture wins).
type ConfigChangeStart struct {
	AccountID string
	AppID     string
	Scope     string
	Actor     string
	Guard     bool
	SecretKey string
	Now       time.Time
}

// ConfigChangeTicket ties a write to the guard it joined, if any.
type ConfigChangeTicket struct {
	GuardID      string
	BaseRevision int64
}

// ConfigChangeFinish records the revision after a write landed.
type ConfigChangeFinish struct {
	AccountID        string
	AppID            string
	Scope            string
	Actor            string
	Source           string
	Key              string
	GuardID          string
	RestoredRevision int64
	// Unrestorable marks the guard as unable to restore automatically (a
	// deleted or rotated secret).
	Unrestorable bool
	Now          time.Time
}

// ConfigGuardSample is the guard's old-config and new-config evidence.
type ConfigGuardSample struct {
	NewRequests     int64
	NewServerErrors int64
	OldRequests     int64
	OldServerErrors int64
}

// ConfigGuardTelemetryQuery bounds one guard evaluation.
type ConfigGuardTelemetryQuery struct {
	AppID        string
	Scope        string
	OpenedAt     time.Time
	ChangedAt    time.Time
	BaselineFrom time.Time
	Until        time.Time
}

// ConfigRevisionStore persists config revisions and guards (ADR-1451).
type ConfigRevisionStore interface {
	// ConfigState reads a scope's current variables and secret fingerprints.
	ConfigState(ctx context.Context, accountID, appID, scope string) (map[string]string, map[string]ConfigSecretFingerprint, error)
	BeginConfigChange(ctx context.Context, start ConfigChangeStart) (ConfigChangeTicket, error)
	FinishConfigChange(ctx context.Context, finish ConfigChangeFinish) (ConfigRevision, error)
	// AbandonConfigChange closes a guard that no write landed in.
	AbandonConfigChange(ctx context.Context, guardID string, now time.Time) error
	ListConfigRevisions(ctx context.Context, appID, scope string, limit int) ([]ConfigRevision, error)
	GetConfigRevision(ctx context.Context, appID, scope string, revision int64) (ConfigRevision, error)
	ListWatchingConfigGuards(ctx context.Context, limit int) ([]ConfigGuard, error)
	LatestConfigGuard(ctx context.Context, appID, scope string) (ConfigGuard, error)
	// OpenConfigGuardWindow starts the judging window once, for the change
	// the caller observed traffic for.
	OpenConfigGuardWindow(ctx context.Context, guardID string, lastChangeAt, endsAt time.Time) (bool, error)
	// CloseConfigGuard closes a watching guard and erases its retained
	// secrets. expectHead > 0 requires the guard still to be judging that
	// head revision.
	CloseConfigGuard(ctx context.Context, guardID, status string, decision json.RawMessage, expectHead int64, now time.Time) (bool, error)
	ConfigGuardSecrets(ctx context.Context, guardID string) ([]ConfigGuardSecret, error)
}

// ConfigGuardTelemetryStore reads a guard's evidence from request telemetry
// (Postgres only).
type ConfigGuardTelemetryStore interface {
	ConfigGuardTelemetry(ctx context.Context, q ConfigGuardTelemetryQuery) (ConfigGuardSample, error)
}

var configRevisionSources = map[string]bool{
	ConfigSourceBaseline: true, ConfigSourceEnvSet: true, ConfigSourceEnvDelete: true,
	ConfigSourceSecretSet: true, ConfigSourceSecretDelete: true, ConfigSourceSecretRotate: true,
	ConfigSourceRollback: true, ConfigSourceGuardRestore: true,
}

var configGuardClosedStatuses = map[string]bool{
	ConfigGuardClean: true, ConfigGuardRestored: true, ConfigGuardRegressed: true,
	ConfigGuardNoTraffic: true, ConfigGuardSuperseded: true,
}

// configSourceHasKey reports whether a revision of this source names one key.
func configSourceHasKey(source string) bool {
	return source != ConfigSourceBaseline && source != ConfigSourceRollback && source != ConfigSourceGuardRestore
}

func configSourceRestores(source string) bool {
	return source == ConfigSourceRollback || source == ConfigSourceGuardRestore
}

func validConfigChangeFinish(f ConfigChangeFinish) bool {
	if !configRevisionSources[f.Source] || f.Source == ConfigSourceBaseline || f.AccountID == "" || f.AppID == "" ||
		api.ValidateScope(f.Scope) != nil || len(f.Actor) > 256 {
		return false
	}
	if configSourceHasKey(f.Source) != (f.Key != "") || (f.Key != "" && api.ValidateEnvKey(f.Key) != nil) {
		return false
	}
	return configSourceRestores(f.Source) == (f.RestoredRevision > 0)
}

func validConfigChangeStart(s ConfigChangeStart) bool {
	return s.AccountID != "" && s.AppID != "" && api.ValidateScope(s.Scope) == nil && len(s.Actor) <= 256 &&
		(s.SecretKey == "" || (s.Guard && api.ValidateEnvKey(s.SecretKey) == nil))
}

func configStateEqual(a ConfigRevision, variables map[string]string, secrets map[string]ConfigSecretFingerprint) bool {
	return maps.Equal(a.Variables, variables) && maps.Equal(a.Secrets, secrets)
}

func supersededConfigGuardDecision(summary string) json.RawMessage {
	raw, _ := json.Marshal(map[string]string{"rule": "superseded", "summary": summary})
	return raw
}

const (
	configGuardSupersededByUnguarded = "an unguarded change replaced the config this guard was judging"
	configGuardSupersededOutOfBand   = "the config changed outside the guard before its verdict"
	configGuardAbandoned             = "no change landed"
)

type configScopeKey struct{ AppID, Scope string }

// --- MemStore ---------------------------------------------------------------

var _ ConfigRevisionStore = (*MemStore)(nil)

func (m *MemStore) configStateLocked(accountID, appID, scope string) (map[string]string, map[string]ConfigSecretFingerprint) {
	variables := map[string]string{}
	for k, row := range m.envs {
		if k.AppID == appID && k.Scope == scope && row.AccountID == accountID {
			variables[k.Key] = row.Value
		}
	}
	secrets := map[string]ConfigSecretFingerprint{}
	for k, row := range m.secrets {
		if k.AppID == appID && k.Scope == scope && row.AccountID == accountID {
			secrets[k.Key] = ConfigSecretFingerprint{Version: row.SecretVersion, ValueHash: row.ValueHash}
		}
	}
	return variables, secrets
}

func (m *MemStore) ConfigState(_ context.Context, accountID, appID, scope string) (map[string]string, map[string]ConfigSecretFingerprint, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	variables, secrets := m.configStateLocked(accountID, appID, scope)
	return variables, secrets, nil
}

func (m *MemStore) latestConfigRevisionLocked(appID, scope string) (ConfigRevision, bool) {
	revisions := m.configRevisions[configScopeKey{appID, scope}]
	if len(revisions) == 0 {
		return ConfigRevision{}, false
	}
	return revisions[len(revisions)-1], true
}

func (m *MemStore) watchingConfigGuardLocked(appID, scope string) (ConfigGuard, bool) {
	for _, g := range m.configGuards {
		if g.AppID == appID && g.Scope == scope && g.Status == ConfigGuardWatching {
			return g, true
		}
	}
	return ConfigGuard{}, false
}

func (m *MemStore) appendConfigRevisionLocked(r ConfigRevision) ConfigRevision {
	if m.configRevisions == nil {
		m.configRevisions = map[configScopeKey][]ConfigRevision{}
	}
	key := configScopeKey{r.AppID, r.Scope}
	r.ID = uuid.NewString()
	r.Variables = maps.Clone(r.Variables)
	r.Secrets = maps.Clone(r.Secrets)
	m.configRevisions[key] = append(m.configRevisions[key], r)
	return cloneConfigRevision(r)
}

func (m *MemStore) closeConfigGuardLocked(id, status string, decision json.RawMessage, now time.Time) {
	g := m.configGuards[id]
	g.Status = status
	g.Decision = append(json.RawMessage(nil), decision...)
	closed := now.UTC()
	g.ClosedAt = &closed
	m.configGuards[id] = g
	delete(m.configGuardSecrets, id)
}

func (m *MemStore) BeginConfigChange(_ context.Context, s ConfigChangeStart) (ConfigChangeTicket, error) {
	if !validConfigChangeStart(s) {
		return ConfigChangeTicket{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.configGuards == nil {
		m.configGuards = map[string]ConfigGuard{}
		m.configGuardSecrets = map[string]map[string]ConfigGuardSecret{}
	}
	now := s.Now.UTC()
	variables, secrets := m.configStateLocked(s.AccountID, s.AppID, s.Scope)
	latest, ok := m.latestConfigRevisionLocked(s.AppID, s.Scope)
	guard, watching := m.watchingConfigGuardLocked(s.AppID, s.Scope)
	if !ok || !configStateEqual(latest, variables, secrets) {
		if watching {
			m.closeConfigGuardLocked(guard.ID, ConfigGuardSuperseded, supersededConfigGuardDecision(configGuardSupersededOutOfBand), now)
			watching = false
		}
		latest = m.appendConfigRevisionLocked(ConfigRevision{AccountID: s.AccountID, AppID: s.AppID, Scope: s.Scope,
			Revision: latest.Revision + 1, Source: ConfigSourceBaseline, Actor: s.Actor, Variables: variables,
			Secrets: secrets, CreatedAt: now})
	}
	if !s.Guard {
		if watching {
			m.closeConfigGuardLocked(guard.ID, ConfigGuardSuperseded, supersededConfigGuardDecision(configGuardSupersededByUnguarded), now)
		}
		return ConfigChangeTicket{BaseRevision: latest.Revision}, nil
	}
	if !watching {
		guard = ConfigGuard{ID: uuid.NewString(), AccountID: s.AccountID, AppID: s.AppID, Scope: s.Scope,
			BaseRevision: latest.Revision, HeadRevision: latest.Revision, Status: ConfigGuardWatching,
			Restorable: true, OpenedAt: now, LastChangeAt: now}
		m.configGuards[guard.ID] = guard
	}
	if s.SecretKey != "" {
		retained := m.configGuardSecrets[guard.ID]
		if retained == nil {
			retained = map[string]ConfigGuardSecret{}
			m.configGuardSecrets[guard.ID] = retained
		}
		if _, seen := retained[s.SecretKey]; !seen {
			captured := ConfigGuardSecret{Key: s.SecretKey}
			if row, exists := m.secrets[secretKey{AppID: s.AppID, Scope: s.Scope, Key: s.SecretKey}]; exists && row.AccountID == s.AccountID {
				captured = ConfigGuardSecret{Key: s.SecretKey, Existed: true, Ciphertext: append([]byte(nil), row.Ciphertext...),
					Kid: row.Kid, ValueHash: row.ValueHash, SecretClass: defaultSecretClass(row.SecretClass)}
			}
			retained[s.SecretKey] = captured
		}
	}
	return ConfigChangeTicket{GuardID: guard.ID, BaseRevision: guard.BaseRevision}, nil
}

func (m *MemStore) FinishConfigChange(_ context.Context, f ConfigChangeFinish) (ConfigRevision, error) {
	if !validConfigChangeFinish(f) {
		return ConfigRevision{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := f.Now.UTC()
	variables, secrets := m.configStateLocked(f.AccountID, f.AppID, f.Scope)
	latest, _ := m.latestConfigRevisionLocked(f.AppID, f.Scope)
	revision := m.appendConfigRevisionLocked(ConfigRevision{AccountID: f.AccountID, AppID: f.AppID, Scope: f.Scope,
		Revision: latest.Revision + 1, Source: f.Source, Key: f.Key, Actor: f.Actor, Variables: variables, Secrets: secrets,
		GuardID: f.GuardID, RestoredRevision: f.RestoredRevision, CreatedAt: now})
	if g, ok := m.configGuards[f.GuardID]; ok && g.Status == ConfigGuardWatching {
		g.HeadRevision = revision.Revision
		if now.After(g.LastChangeAt) {
			g.LastChangeAt = now
		}
		g.WindowEndsAt = nil
		g.Restorable = g.Restorable && !f.Unrestorable
		m.configGuards[g.ID] = g
	}
	m.pruneConfigRevisionsLocked(f.AppID, f.Scope, revision.Revision-api.ConfigRevisionsRetained)
	return revision, nil
}

func (m *MemStore) pruneConfigRevisionsLocked(appID, scope string, keepAfter int64) {
	key := configScopeKey{appID, scope}
	kept := m.configRevisions[key][:0]
	for _, r := range m.configRevisions[key] {
		guardBase := false
		for _, g := range m.configGuards {
			if g.AppID == appID && g.Scope == scope && g.Status == ConfigGuardWatching && g.BaseRevision == r.Revision {
				guardBase = true
			}
		}
		if r.Revision > keepAfter || guardBase {
			kept = append(kept, r)
		}
	}
	m.configRevisions[key] = kept
}

func (m *MemStore) AbandonConfigChange(_ context.Context, guardID string, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if g, ok := m.configGuards[guardID]; ok && g.Status == ConfigGuardWatching && g.HeadRevision == g.BaseRevision {
		m.closeConfigGuardLocked(guardID, ConfigGuardSuperseded, supersededConfigGuardDecision(configGuardAbandoned), now)
	}
	return nil
}

func (m *MemStore) ListConfigRevisions(_ context.Context, appID, scope string, limit int) ([]ConfigRevision, error) {
	if limit <= 0 || limit > api.ConfigRevisionsListMax {
		limit = api.ConfigRevisionsListMax
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	revisions := m.configRevisions[configScopeKey{appID, scope}]
	out := []ConfigRevision{}
	for i := len(revisions) - 1; i >= 0 && len(out) < limit; i-- {
		out = append(out, m.withConfigGuardStatusLocked(cloneConfigRevision(revisions[i])))
	}
	return out, nil
}

func (m *MemStore) GetConfigRevision(_ context.Context, appID, scope string, revision int64) (ConfigRevision, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, r := range m.configRevisions[configScopeKey{appID, scope}] {
		if r.Revision == revision {
			return m.withConfigGuardStatusLocked(cloneConfigRevision(r)), nil
		}
	}
	return ConfigRevision{}, ErrNotFound
}

func (m *MemStore) ListWatchingConfigGuards(_ context.Context, limit int) ([]ConfigGuard, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []ConfigGuard{}
	for _, g := range m.configGuards {
		if g.Status == ConfigGuardWatching {
			out = append(out, cloneConfigGuard(g))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].LastChangeAt.Equal(out[j].LastChangeAt) {
			return out[i].LastChangeAt.Before(out[j].LastChangeAt)
		}
		return out[i].ID < out[j].ID
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *MemStore) LatestConfigGuard(_ context.Context, appID, scope string) (ConfigGuard, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var latest ConfigGuard
	found := false
	for _, g := range m.configGuards {
		if g.AppID != appID || g.Scope != scope {
			continue
		}
		if !found || g.OpenedAt.After(latest.OpenedAt) || (g.OpenedAt.Equal(latest.OpenedAt) && g.ID > latest.ID) {
			latest, found = g, true
		}
	}
	if !found {
		return ConfigGuard{}, ErrNotFound
	}
	return cloneConfigGuard(latest), nil
}

func (m *MemStore) OpenConfigGuardWindow(_ context.Context, guardID string, lastChangeAt, endsAt time.Time) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	g, ok := m.configGuards[guardID]
	if !ok || g.Status != ConfigGuardWatching || g.WindowEndsAt != nil || !g.LastChangeAt.Equal(lastChangeAt) {
		return false, nil
	}
	ends := endsAt.UTC()
	g.WindowEndsAt = &ends
	m.configGuards[guardID] = g
	return true, nil
}

func (m *MemStore) CloseConfigGuard(_ context.Context, guardID, status string, decision json.RawMessage, expectHead int64, now time.Time) (bool, error) {
	if !configGuardClosedStatuses[status] || !json.Valid(decision) {
		return false, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	g, ok := m.configGuards[guardID]
	if !ok || g.Status != ConfigGuardWatching || (expectHead > 0 && g.HeadRevision != expectHead) {
		return false, nil
	}
	m.closeConfigGuardLocked(guardID, status, decision, now)
	return true, nil
}

func (m *MemStore) ConfigGuardSecrets(_ context.Context, guardID string) ([]ConfigGuardSecret, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []ConfigGuardSecret{}
	for _, s := range m.configGuardSecrets[guardID] {
		s.Ciphertext = append([]byte(nil), s.Ciphertext...)
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

func (m *MemStore) withConfigGuardStatusLocked(r ConfigRevision) ConfigRevision {
	if g, ok := m.configGuards[r.GuardID]; ok {
		r.GuardStatus = g.Status
	}
	return r
}

func cloneConfigRevision(r ConfigRevision) ConfigRevision {
	r.Variables = maps.Clone(r.Variables)
	r.Secrets = maps.Clone(r.Secrets)
	return r
}

func cloneConfigGuard(g ConfigGuard) ConfigGuard {
	if g.WindowEndsAt != nil {
		v := *g.WindowEndsAt
		g.WindowEndsAt = &v
	}
	if g.ClosedAt != nil {
		v := *g.ClosedAt
		g.ClosedAt = &v
	}
	g.Decision = append(json.RawMessage(nil), g.Decision...)
	return g
}
