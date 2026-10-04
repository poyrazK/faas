package globaldirectory

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// MemoryStore is the same contract as PostgreSQL, with one critical section
// covering registry generation, account mapping and target uniqueness.
type MemoryStore struct {
	mu              sync.Mutex
	owners          map[string]Owner
	orgAuthority    OrgAuthority
	cells           map[string]Cell
	apps            map[string]App
	accountBindings map[string]string
	targets         map[string]map[string]Target
	releases        map[string]map[string]Release
	releaseHeads    map[string]int64
	delegations     map[string]Delegation
	recovery        map[string]memoryRecovery
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{owners: make(map[string]Owner), cells: make(map[string]Cell), apps: make(map[string]App), accountBindings: make(map[string]string), targets: make(map[string]map[string]Target), releases: make(map[string]map[string]Release), releaseHeads: make(map[string]int64), delegations: make(map[string]Delegation), recovery: make(map[string]memoryRecovery)}
}

func (s *MemoryStore) CheckScope(context.Context) error { return nil }

func (s *MemoryStore) PutCell(_ context.Context, c Cell) (Cell, error) {
	if err := c.Validate(); err != nil {
		return Cell{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, old := range s.cells {
		if old.ID == c.ID && old.Region != c.Region || old.Region == c.Region && old.ID != c.ID {
			return Cell{}, ErrConflict
		}
	}
	c.Generation = 1
	if old, ok := s.cells[c.Region]; ok {
		c.Generation = old.Generation
		if old.APIBase != c.APIBase || old.Enabled != c.Enabled {
			c.Generation++
			for _, g := range s.delegations {
				if g.Binding.CellID == c.ID {
					s.retireDelegations(g.GlobalAppID, g.Binding.Region)
				}
			}
		}
	}
	s.cells[c.Region] = c
	return c, nil
}

func (s *MemoryStore) ListCells(context.Context) ([]Cell, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Cell, 0, len(s.cells))
	for _, cell := range s.cells {
		out = append(out, cell)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Region < out[j].Region })
	return out, nil
}

func (s *MemoryStore) CellByRegion(_ context.Context, region string) (Cell, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cell, ok := s.cells[region]
	if !ok {
		return Cell{}, ErrNotFound
	}
	return cell, nil
}

func (s *MemoryStore) CreateApp(_ context.Context, accountID, slug string, limit int) (App, error) {
	if !validOwnerID(accountID) || !ValidSlug(slug) {
		return App{}, ErrConflict
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if o, ok := s.owners[accountID]; ok && o.Shared() {
		return App{}, ErrConflict
	}
	s.owners[accountID] = Owner{ID: accountID, AccountID: accountID}
	return s.createApp(accountID, slug, limit)
}

func (s *MemoryStore) createApp(accountID, slug string, limit int) (App, error) {
	if !validOwnerID(accountID) || !ValidSlug(slug) {
		return App{}, ErrConflict
	}
	if old, ok := s.apps[slug]; ok {
		if old.OwnerID != accountID {
			return App{}, ErrConflict
		}
		return old, nil
	}
	count := 0
	for _, old := range s.apps {
		if old.OwnerID == accountID {
			count++
		}
	}
	if limit < 1 || count >= limit {
		return App{}, ErrLimit
	}
	app := App{ID: uuid.NewString(), OwnerID: accountID, Slug: slug, RoutingPolicy: AllSelectedRegions, CreatedAt: time.Now().UTC()}
	s.apps[slug] = app
	return app, nil
}

func (s *MemoryStore) AppBySlug(_ context.Context, accountID, slug string) (App, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.app(accountID, slug)
}

func (s *MemoryStore) app(accountID, slug string) (App, error) {
	app, ok := s.apps[slug]
	if !ok || app.OwnerID != accountID {
		return App{}, ErrNotFound
	}
	return app, nil
}

func (s *MemoryStore) ListApps(_ context.Context, accountID string) ([]App, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]App, 0)
	for _, app := range s.apps {
		if app.OwnerID == accountID {
			out = append(out, app)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Slug < out[j].Slug })
	return out, nil
}

func (s *MemoryStore) AttachTarget(_ context.Context, accountID, slug string, binding Binding) (Target, error) {
	if err := binding.Validate(); err != nil {
		return Target{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	app, err := s.app(accountID, slug)
	if err != nil {
		return Target{}, err
	}
	if s.owners[accountID].Shared() {
		return Target{}, ErrOrgTargetsUnavailable
	}
	if binding.RegionalOrgID != "" {
		return Target{}, ErrConflict
	}
	return s.attachTarget(app, accountID, binding)
}

func (s *MemoryStore) attachTarget(app App, accountID string, binding Binding) (Target, error) {
	cell, ok := s.cells[binding.Region]
	if !ok || cell.ID != binding.CellID || !cell.Enabled || cell.Generation != binding.CellGeneration {
		return Target{}, ErrStaleCell
	}
	mapKey := accountID + "/" + binding.CellID
	if old, ok := s.accountBindings[mapKey]; ok && old != binding.regionalOwner() {
		return Target{}, ErrConflict
	}
	for key, regionalID := range s.accountBindings {
		if key != mapKey && regionalID == binding.regionalOwner() && strings.HasSuffix(key, "/"+binding.CellID) {
			return Target{}, ErrConflict
		}
	}
	for globalID, targets := range s.targets {
		for region, old := range targets {
			if globalID == app.ID && region == binding.Region && (old.CellID != binding.CellID || old.RegionalAccountID != binding.RegionalAccountID || old.RegionalOrgID != binding.RegionalOrgID || old.RegionalAppID != binding.RegionalAppID || old.RegionalAppSlug != binding.RegionalAppSlug) {
				return Target{}, ErrConflict
			}
			if globalID != app.ID && old.CellID == binding.CellID && old.RegionalAppID == binding.RegionalAppID {
				return Target{}, ErrConflict
			}
		}
	}
	if s.targets[app.ID] == nil {
		s.targets[app.ID] = make(map[string]Target)
	}
	target := Target{Binding: binding, GlobalAppID: app.ID, VerifiedAt: time.Now().UTC(), APIBase: cell.APIBase, CellEnabled: cell.Enabled, RegistryGeneration: cell.Generation}
	s.accountBindings[mapKey] = binding.regionalOwner()
	if old, ok := s.targets[app.ID][binding.Region]; ok && old.Binding != binding {
		s.retireDelegations(app.ID, binding.Region)
	}
	s.targets[app.ID][binding.Region] = target
	return target, nil
}

func (s *MemoryStore) ListTargets(_ context.Context, accountID, slug string) ([]Target, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	app, err := s.app(accountID, slug)
	if err != nil {
		return nil, err
	}
	out := make([]Target, 0, len(s.targets[app.ID]))
	for _, target := range s.targets[app.ID] {
		cell := s.cells[target.Region]
		target.APIBase, target.CellEnabled, target.RegistryGeneration = cell.APIBase, cell.Enabled, cell.Generation
		out = append(out, target)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Region < out[j].Region })
	return out, nil
}

func (s *MemoryStore) DetachTarget(_ context.Context, accountID, slug, region string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	app, err := s.app(accountID, slug)
	if err != nil {
		return err
	}
	if s.owners[accountID].Shared() {
		return ErrOrgTargetsUnavailable
	}
	return s.detachTarget(app, region)
}

func (s *MemoryStore) detachTarget(app App, region string) error {
	if _, ok := s.targets[app.ID][region]; !ok {
		return ErrNotFound
	}
	s.retireDelegations(app.ID, region)
	delete(s.targets[app.ID], region)
	return nil
}

var _ Store = (*MemoryStore)(nil)
