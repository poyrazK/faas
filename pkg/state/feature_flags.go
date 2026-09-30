package state

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/flags"
)

type FeatureFlagScope struct{ AccountID, ProjectID, EnvironmentID string }
type FeatureFlagVersion struct {
	flags.Bundle
	Actor        string    `json:"actor"`
	RestoredFrom int64     `json:"restored_from,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}
type FeatureFlagUpdate struct {
	Scope           FeatureFlagScope
	ExpectedVersion int64
	RestoreVersion  int64
	Config          flags.Config
	Actor           string
}
type FeatureFlagStore interface {
	GetFeatureFlags(context.Context, FeatureFlagScope, int64) (FeatureFlagVersion, error)
	ListFeatureFlagVersions(context.Context, FeatureFlagScope, int64) ([]FeatureFlagVersion, error)
	UpdateFeatureFlags(context.Context, FeatureFlagUpdate) (FeatureFlagVersion, error)
}

func emptyFeatureFlags(scope FeatureFlagScope) FeatureFlagVersion {
	return FeatureFlagVersion{Bundle: flags.Bundle{EnvironmentID: scope.EnvironmentID, Config: flags.Config{Flags: []flags.Flag{}, Groups: map[string][]string{}}}}
}
func cloneFeatureFlags(v FeatureFlagVersion) FeatureFlagVersion {
	raw, _ := json.Marshal(v)
	var c FeatureFlagVersion
	_ = json.Unmarshal(raw, &c)
	return c
}
func prepareFeatureFlags(u FeatureFlagUpdate, prior FeatureFlagVersion) (FeatureFlagVersion, error) {
	if u.ExpectedVersion < 0 || u.ExpectedVersion != prior.Version || prior.Version >= api.FlagsMaxConfigVersion {
		return FeatureFlagVersion{}, ErrConflict
	}
	if u.Actor == "" || len(u.Actor) > api.FlagsMaxActorBytes {
		return FeatureFlagVersion{}, ErrInvalidArgument
	}
	c := cloneFeatureFlags(FeatureFlagVersion{Bundle: flags.Bundle{Config: u.Config}}).Config
	if c.Flags == nil {
		c.Flags = []flags.Flag{}
	}
	if c.Groups == nil {
		c.Groups = map[string][]string{}
	}
	seeds := map[string]string{}
	for _, f := range prior.Flags {
		seeds[f.Key] = f.Seed
	}
	for i := range c.Flags {
		f := &c.Flags[i]
		if f.Rules == nil {
			f.Rules = []flags.Rule{}
		}
		if seed := seeds[f.Key]; seed != "" {
			if f.Seed != "" && f.Seed != seed {
				return FeatureFlagVersion{}, fmt.Errorf("flag seed is immutable: %w", ErrInvalidArgument)
			}
			f.Seed = seed
		} else {
			environment, err := uuid.Parse(u.Scope.EnvironmentID)
			if err != nil {
				return FeatureFlagVersion{}, ErrInvalidArgument
			}
			seed := uuid.NewSHA1(environment, []byte(f.Key)).String()
			if f.Seed != "" && f.Seed != seed {
				return FeatureFlagVersion{}, fmt.Errorf("new flag seed is server-owned: %w", ErrInvalidArgument)
			}
			f.Seed = seed
		}
	}
	canonicalIDs := func(ids []string) error {
		for i, id := range ids {
			parsed, err := uuid.Parse(id)
			if err != nil {
				return ErrInvalidArgument
			}
			ids[i] = parsed.String()
		}
		return nil
	}
	for key, ids := range c.Groups {
		if ids == nil {
			c.Groups[key] = []string{}
		}
		if err := canonicalIDs(ids); err != nil {
			return FeatureFlagVersion{}, err
		}
	}
	for _, f := range c.Flags {
		for _, r := range f.Rules {
			if err := canonicalIDs(r.Customers); err != nil {
				return FeatureFlagVersion{}, err
			}
		}
	}
	if err := flags.Validate(c); err != nil {
		return FeatureFlagVersion{}, fmt.Errorf("%s: %w", err, ErrInvalidArgument)
	}
	raw, _ := json.Marshal(c)
	if len(raw) > api.FlagsMaxBundleBytes {
		return FeatureFlagVersion{}, ErrInvalidArgument
	}
	return FeatureFlagVersion{Bundle: flags.Bundle{EnvironmentID: u.Scope.EnvironmentID, Version: prior.Version + 1, Config: c}, Actor: u.Actor, RestoredFrom: u.RestoreVersion, CreatedAt: time.Now().UTC()}, nil
}
func flagCustomerIDs(c flags.Config) []string {
	seen := map[string]bool{}
	var out []string
	add := func(ids []string) {
		for _, id := range ids {
			if !seen[id] {
				seen[id] = true
				out = append(out, id)
			}
		}
	}
	for _, ids := range c.Groups {
		add(ids)
	}
	for _, f := range c.Flags {
		for _, r := range f.Rules {
			add(r.Customers)
		}
	}
	return out
}
func (m *MemStore) featureFlagScopeLocked(s FeatureFlagScope) bool {
	p, ok := m.projects[s.ProjectID]
	if !ok || p.AccountID != s.AccountID {
		return false
	}
	e, ok := m.projectEnvironments[s.EnvironmentID]
	return ok && e.AccountID == s.AccountID && e.ProjectID == s.ProjectID
}
func (m *MemStore) GetFeatureFlags(_ context.Context, s FeatureFlagScope, version int64) (FeatureFlagVersion, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.featureFlagScopeLocked(s) {
		return FeatureFlagVersion{}, ErrNotFound
	}
	rows := m.featureFlagVersions[s.EnvironmentID]
	if len(rows) == 0 && version == 0 {
		return emptyFeatureFlags(s), nil
	}
	for i := len(rows) - 1; i >= 0; i-- {
		if version == 0 || rows[i].Version == version {
			return cloneFeatureFlags(rows[i]), nil
		}
	}
	return FeatureFlagVersion{}, ErrNotFound
}
func (m *MemStore) ListFeatureFlagVersions(_ context.Context, s FeatureFlagScope, before int64) ([]FeatureFlagVersion, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.featureFlagScopeLocked(s) {
		return nil, ErrNotFound
	}
	out := []FeatureFlagVersion{}
	rows := m.featureFlagVersions[s.EnvironmentID]
	for i := len(rows) - 1; i >= 0 && len(out) < api.FlagsMaxHistoryPage; i-- {
		if before == 0 || rows[i].Version < before {
			out = append(out, cloneFeatureFlags(rows[i]))
		}
	}
	return out, nil
}
func (m *MemStore) UpdateFeatureFlags(_ context.Context, u FeatureFlagUpdate) (FeatureFlagVersion, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.featureFlagScopeLocked(u.Scope) {
		return FeatureFlagVersion{}, ErrNotFound
	}
	rows := m.featureFlagVersions[u.Scope.EnvironmentID]
	prior := emptyFeatureFlags(u.Scope)
	if len(rows) > 0 {
		prior = rows[len(rows)-1]
	}
	if u.RestoreVersion > 0 {
		found := false
		for _, v := range rows {
			if v.Version == u.RestoreVersion {
				u.Config = cloneFeatureFlags(v).Config
				found = true
				break
			}
		}
		if !found {
			return FeatureFlagVersion{}, ErrNotFound
		}
	}
	v, err := prepareFeatureFlags(u, prior)
	if err != nil {
		return FeatureFlagVersion{}, err
	}
	for _, id := range flagCustomerIDs(v.Config) {
		t, ok := m.platformTenants[id]
		if !ok || t.AccountID != u.Scope.AccountID {
			return FeatureFlagVersion{}, ErrNotFound
		}
	}
	if m.featureFlagVersions == nil {
		m.featureFlagVersions = map[string][]FeatureFlagVersion{}
	}
	m.featureFlagVersions[u.Scope.EnvironmentID] = append(rows, cloneFeatureFlags(v))
	return v, nil
}
