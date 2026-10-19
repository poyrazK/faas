package managedpostgres

import (
	"context"
	"sort"
	"time"
)

var _ CutoverPreflightStore = (*MemoryStore)(nil)

func cloneCutoverPreflight(p CutoverPreflight) CutoverPreflight {
	p.BindingIDs = append([]string{}, p.BindingIDs...)
	p.Blockers = append([]string{}, p.Blockers...)
	if p.SourceSessions != nil {
		view := *p.SourceSessions
		if view.WriterIsolation != nil {
			writer := *view.WriterIsolation
			view.WriterIsolation = &writer
		}
		view.Blockers = append([]string{}, view.Blockers...)
		p.SourceSessions = &view
	}
	if p.RuntimeDrain != nil {
		runtime := *p.RuntimeDrain
		runtime.Blockers = append([]string{}, runtime.Blockers...)
		runtime.Members = append([]CutoverRuntimeDrainMember{}, runtime.Members...)
		p.RuntimeDrain = &runtime
	}
	return p
}

func (s *MemoryStore) RequestCutoverPreflight(_ context.Context, account, id string, now time.Time) (CutoverPreflight, error) {
	if now.IsZero() {
		return CutoverPreflight{}, ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.cutovers[id]
	if !ok || c.AccountID != account {
		return CutoverPreflight{}, ErrNotFound
	}
	if c.State == CutoverCancelling || c.State == CutoverCancelled {
		return CutoverPreflight{}, ErrConflict
	}
	p, ok := s.preflights[id]
	if ok && p.State == "pending" {
		return cloneCutoverPreflight(p), nil
	}
	p = CutoverPreflight{CutoverID: id, State: "pending", Revision: p.Revision + 1, CreatedAt: p.CreatedAt, UpdatedAt: now, BindingIDs: []string{}, Blockers: []string{}}
	if !ok {
		p.CreatedAt = now
	}
	s.preflights[id] = p
	return cloneCutoverPreflight(p), nil
}

func (s *MemoryStore) GetCutoverPreflight(_ context.Context, account, id string) (CutoverPreflight, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.cutovers[id]
	p, exists := s.preflights[id]
	if !ok || c.AccountID != account || !exists {
		return CutoverPreflight{}, ErrNotFound
	}
	return cloneCutoverPreflight(p), nil
}

func (s *MemoryStore) EvaluateCutoverPreflight(_ context.Context, account, id string, now time.Time) (CutoverPreflight, error) {
	if now.IsZero() {
		return CutoverPreflight{}, ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.cutovers[id]
	old, exists := s.preflights[id]
	if !ok || c.AccountID != account || !exists {
		return CutoverPreflight{}, ErrNotFound
	}
	if old.State != "pending" {
		return cloneCutoverPreflight(old), nil
	}
	var bindings []Binding
	targetBound := false
	for _, b := range s.bindings {
		if b.State == BindingStateDeleted {
			continue
		}
		if b.DatabaseID == c.Target.ID {
			targetBound = true
		}
		if b.DatabaseID == c.Source.ID && b.AppID == c.AppID && b.Scope == c.Scope {
			bindings = append(bindings, b)
		}
	}
	p := evaluateCutoverPreflight(c, s.databases[c.Source.ID], s.databases[c.Target.ID], bindings, targetBound, now)
	// The in-memory catalog has no authoritative scheduler retirement ledger.
	p.RuntimeDrain = &CutoverRuntimeDrain{State: "unavailable", Blockers: []string{"runtime_observation_unavailable"}, Members: []CutoverRuntimeDrainMember{}}
	p.SourceSessions = &CutoverSourceSessions{State: "unavailable", Blockers: []string{"source_fence_observation_unavailable"}}
	p.Revision, p.CreatedAt, p.UpdatedAt = old.Revision, old.CreatedAt, now
	s.preflights[id] = p
	return cloneCutoverPreflight(p), nil
}

func (s *MemoryStore) PendingCutoverPreflights(_ context.Context, limit int) ([]Cutover, error) {
	if limit < 1 || limit > 100 {
		return nil, ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var pending []CutoverPreflight
	for _, p := range s.preflights {
		if p.State == "pending" {
			pending = append(pending, p)
		}
	}
	sort.Slice(pending, func(i, j int) bool {
		if pending[i].UpdatedAt.Equal(pending[j].UpdatedAt) {
			return pending[i].CutoverID < pending[j].CutoverID
		}
		return pending[i].UpdatedAt.Before(pending[j].UpdatedAt)
	})
	rows := []Cutover{}
	for _, p := range pending[:min(limit, len(pending))] {
		rows = append(rows, cloneCutover(s.cutovers[p.CutoverID]))
	}
	return rows, nil
}
