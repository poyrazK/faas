package state

import (
	"context"
	"encoding/json"
	"strings"
	"unicode"

	"github.com/google/uuid"
)

// Legacy app-only queue methods own production, including when persisted
// stage ownership is damaged. Call only while holding m.mu.
func (m *MemStore) productionInvocationWorkLocked(inv Invocation) bool {
	if inv.EnvironmentID != "" {
		return false
	}
	if _, ok := m.invocationEnvironmentQueueAdmissions[inv.ID]; ok {
		return false
	}
	if _, ok := m.invocationWorkEnvironmentAdmissions[inv.ID]; ok {
		return false
	}
	var headers map[string]json.RawMessage
	if json.Unmarshal(inv.Headers, &headers) != nil {
		return true
	}
	app := m.apps[inv.AppID]
	for key, raw := range headers {
		revision, release := strings.EqualFold(key, "X-Gregale-Revision"), strings.EqualFold(key, "X-Gregale-Release")
		if !revision && !release {
			continue
		}
		value := string(raw)
		_ = json.Unmarshal(raw, &value)
		value = strings.Map(func(r rune) rune {
			if unicode.IsSpace(r) || r == '{' || r == '}' {
				return -1
			}
			return r
		}, strings.ToLower(value))
		value = strings.TrimPrefix(value, "urn:uuid:")
		value = strings.ReplaceAll(value, "-", "")
		parsed, err := uuid.Parse(value)
		if err != nil {
			continue
		}
		if revision {
			for _, dep := range m.deployments {
				owned, err := uuid.Parse(dep.ID)
				if err == nil && owned == parsed && dep.AppID == inv.AppID && invocationStageScope(normalizedDeploymentScope(dep.Scope)) {
					return false
				}
			}
		} else {
			for _, set := range m.projectReleaseSets {
				owned, err := uuid.Parse(set.ID)
				if err == nil && owned == parsed && set.AccountID == app.AccountID && set.ProjectID == app.ProjectID && invocationStageScope(set.EnvironmentSlug) {
					return false
				}
			}
		}
	}
	return true
}

func (m *MemStore) ProductionQueueInvocationByID(_ context.Context, id string) (Invocation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	inv, ok := m.invocations[id]
	if !ok || inv.Source != InvocationQueue || !m.productionInvocationWorkLocked(inv) {
		return Invocation{}, ErrNotFound
	}
	return cloneInvocationWorkEnvelope(inv), nil
}
