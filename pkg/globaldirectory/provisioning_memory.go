package globaldirectory

// adr: 797
import (
	"context"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func (s *MemoryStore) PlanAppProvisioning(_ context.Context, owner, slug string, specs []AppProvisionSpec) ([]AppProvision, error) {
	canonical, err := canonicalProvisionSpecs(specs)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	a, err := s.app(owner, slug)
	if err != nil {
		return nil, err
	}
	if s.owners[owner].Shared() {
		return nil, ErrOrgTargetsUnavailable
	}
	plans := make([]AppProvision, 0, len(canonical))
	count := len(s.provisions[a.ID])
	for _, spec := range canonical {
		if old, ok := s.provisions[a.ID][spec.Region]; ok {
			if !old.matches(spec) {
				return nil, ErrConflict
			}
			plans = append(plans, cloneAppProvision(old))
			continue
		}
		cell, ok := s.cells[spec.Region]
		if !ok || !cell.Enabled || cell != spec.VerifiedCell {
			return nil, ErrStaleCell
		}
		if _, exists := s.targets[a.ID][spec.Region]; exists {
			return nil, ErrConflict
		}
		key := owner + "/" + cell.ID
		mapped := "account/" + spec.AccountID
		for k, v := range s.accountBindings {
			if (k == key && v != mapped) || (k != key && strings.HasSuffix(k, "/"+cell.ID) && v == mapped) {
				return nil, ErrConflict
			}
		}
		count++
		if count > api.GlobalImageReleaseTargetsMax {
			return nil, &ReleaseLimitError{Kind: "targets", Limit: api.GlobalImageReleaseTargetsMax, Observed: int64(count)}
		}
		plans = append(plans, AppProvision{AppProvisionSpec: spec, ID: uuid.NewString(), GlobalAppID: a.ID, Cell: cell, Status: ProvisionPlanned})
	}
	if s.provisions == nil {
		s.provisions = make(map[string]map[string]AppProvision)
	}
	if s.provisions[a.ID] == nil {
		s.provisions[a.ID] = make(map[string]AppProvision)
	}
	for _, p := range plans {
		s.provisions[a.ID][p.Region] = cloneAppProvision(p)
		s.accountBindings[owner+"/"+p.Cell.ID] = "account/" + p.AccountID
	}
	return plans, nil
}

func (s *MemoryStore) ListAppProvisioning(_ context.Context, owner, slug string) ([]AppProvision, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, err := s.app(owner, slug)
	if err != nil {
		return nil, err
	}
	out := make([]AppProvision, 0, len(s.provisions[a.ID]))
	for _, p := range s.provisions[a.ID] {
		out = append(out, cloneAppProvision(p))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Region < out[j].Region })
	return out, nil
}

func (s *MemoryStore) AttachProvisionTarget(_ context.Context, owner, slug, id string, b Binding) (Target, error) {
	if b.Validate() != nil {
		return Target{}, ErrConflict
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	a, err := s.app(owner, slug)
	if err != nil {
		return Target{}, err
	}
	p, ok := s.provisions[a.ID][b.Region]
	if !ok || p.ID != id || p.Status == ProvisionCancelled || !p.bindingMatches(b) {
		return Target{}, ErrConflict
	}
	t, err := s.attachTarget(a, owner, b)
	if err != nil {
		return Target{}, err
	}
	p.Status = ProvisionAttached
	s.provisions[a.ID][b.Region] = p
	return t, nil
}

var _ AppProvisionStore = (*MemoryStore)(nil)
