package state

import (
	"context"
	"slices"
	"sort"
	"strings"
)

// AddIncidentNote implements IncidentNoteStore.
func (m *MemStore) AddIncidentNote(_ context.Context, n IncidentNote, limit int) (IncidentNote, error) {
	n, _, _, err := prepareIncidentNote(n)
	if err != nil {
		return IncidentNote{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.incidentSourceExistsLocked(n.IncidentID) {
		return IncidentNote{}, ErrNotFound
	}
	count := 0
	for _, existing := range m.incidentNotes {
		if existing.AccountID != n.AccountID || existing.IncidentID != n.IncidentID {
			continue
		}
		if n.Kind == IncidentNoteAcknowledged && existing.Kind == IncidentNoteAcknowledged {
			return existing, nil
		}
		count++
	}
	if count >= limit {
		return IncidentNote{}, &IncidentNotesQuotaError{Limit: limit, Observed: count}
	}
	m.incidentNotes = append(m.incidentNotes, n)
	return n, nil
}

// ListIncidentNotes implements IncidentNoteStore.
func (m *MemStore) ListIncidentNotes(_ context.Context, accountID, incidentID string, limit int) ([]IncidentNote, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []IncidentNote{}
	for _, n := range m.incidentNotes {
		if n.AccountID == accountID && n.IncidentID == incidentID && m.incidentSourceExistsLocked(n.IncidentID) {
			out = append(out, n)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.Before(out[j].CreatedAt)
		}
		return out[i].ID < out[j].ID
	})
	return out[:min(len(out), max(limit, 0))], nil
}

// IncidentAcknowledgements implements IncidentNoteStore.
func (m *MemStore) IncidentAcknowledgements(_ context.Context, accountID string, incidentIDs []string) (map[string]IncidentNote, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[string]IncidentNote{}
	for _, n := range m.incidentNotes {
		if n.AccountID == accountID && n.Kind == IncidentNoteAcknowledged && slices.Contains(incidentIDs, n.IncidentID) &&
			m.incidentSourceExistsLocked(n.IncidentID) {
			out[n.IncidentID] = n
		}
	}
	return out, nil
}

// incidentSourceExistsLocked reports whether an incident's source row is
// still there; notes on a missing one are skipped on read, the MemStore's
// stand-in for ON DELETE CASCADE.
func (m *MemStore) incidentSourceExistsLocked(incidentID string) bool {
	if key, ok := strings.CutPrefix(incidentID, "alert-"); ok {
		ep, found := m.alertEpisodes[key]
		_, ruleFound := m.alertRules[ep.RuleID]
		return found && ruleFound
	}
	if key, ok := strings.CutPrefix(incidentID, "route-"); ok {
		for _, incidents := range m.routeMonitorIncidents {
			for _, inc := range incidents {
				if inc.ID == key {
					return true
				}
			}
		}
		return false
	}
	if key, ok := strings.CutPrefix(incidentID, "platform-"); ok {
		for _, ev := range m.statusIncidents {
			if ev.PublicID == key {
				return true
			}
		}
	}
	return false
}
