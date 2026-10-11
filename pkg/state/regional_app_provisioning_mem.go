package state

// adr: 797
import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func cloneAppProvision(p RegionalAppProvision) RegionalAppProvision {
	p.Request = append([]byte(nil), p.Request...)
	return p
}

func (m *MemStore) RegionalAppProvisionReceipt(_ context.Context, cell, account, id string) (RegionalAppProvision, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.regionalAppProvisions[id]
	if !ok || p.CellID != cell || p.AccountID != regionalQuotaCanonicalID(account) {
		return RegionalAppProvision{}, ErrNotFound
	}
	return cloneAppProvision(p), nil
}

func (m *MemStore) CreateRegionalAppProvision(_ context.Context, p RegionalAppProvision, app App, limits api.Limits, entry OrgActivity) (RegionalAppProvision, bool, int64, error) {
	p.AccountID = regionalQuotaCanonicalID(p.AccountID)
	if !p.valid() || p.AccountID != regionalQuotaCanonicalID(app.AccountID) || p.AppSlug != app.Slug || p.Region != app.ComputeRegion {
		return RegionalAppProvision{}, false, 0, ErrInvalidArgument
	}
	request, err := canonicalProvisionRequest(p.Request)
	if err != nil {
		return RegionalAppProvision{}, false, 0, err
	}
	p.Request = request
	m.mu.Lock()
	defer m.mu.Unlock()
	if prior, ok := m.regionalAppProvisions[p.ProvisionID]; ok {
		if !p.matches(prior) {
			return RegionalAppProvision{}, false, 0, ErrConflict
		}
		return cloneAppProvision(prior), false, 0, nil
	}
	for _, prior := range m.regionalAppProvisions {
		if prior.GlobalAppID == p.GlobalAppID && prior.CellID == p.CellID {
			return RegionalAppProvision{}, false, 0, ErrConflict
		}
	}
	if app.ID == "" {
		app.ID = uuid.NewString()
	}
	created, err := m.createAppIfUnderQuotaLocked(app, limits)
	if err != nil {
		return RegionalAppProvision{}, false, 0, err
	}
	if !m.regionalReleasePersonalApp(created) {
		delete(m.apps, created.ID)
		return RegionalAppProvision{}, false, 0, ErrConflict
	}
	var outbox int64
	if created.OrgID != "" || entry.OrgID != uuid.Nil {
		entry, err = bindOrgActivityToApp(entry, created)
		if err != nil {
			delete(m.apps, created.ID)
			return RegionalAppProvision{}, false, 0, err
		}
		outbox = m.enqueueOrgActivityOutboxLocked(entry)
	}
	p.AppID, p.CreatedAt = regionalQuotaCanonicalID(created.ID), time.Now().UTC().Truncate(time.Microsecond)
	if m.regionalAppProvisions == nil {
		m.regionalAppProvisions = make(map[string]RegionalAppProvision)
	}
	m.regionalAppProvisions[p.ProvisionID] = cloneAppProvision(p)
	return cloneAppProvision(p), true, outbox, nil
}

var _ RegionalAppProvisionStore = (*MemStore)(nil)

func (m *MemStore) regionalAppProvisionedLocked(id string) bool {
	for _, p := range m.regionalAppProvisions {
		if p.AppID == id {
			return true
		}
	}
	return false
}

func (m *MemStore) RegionalAppProvisioned(_ context.Context, id string) (bool, error) {
	id = regionalQuotaCanonicalID(id)
	if id == "" {
		return false, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.regionalAppProvisionedLocked(id), nil
}
