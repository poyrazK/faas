package state

import (
	"context"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type memHostingDomain struct {
	Domain api.HostingDomain
	Token  string
	Until  time.Time
}

var _ HostingDomainStore = (*MemStore)(nil)

func (m *MemStore) CreateHostingDomain(_ context.Context, d api.HostingDomain) (api.HostingDomain, error) {
	if !validHostingDomainInput(d) {
		return api.HostingDomain{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	site := m.hostingSites[d.SiteID]
	if site.AccountID != d.AccountID {
		return api.HostingDomain{}, ErrNotFound
	}
	if !site.DurableRouting || site.Status != "active" {
		return api.HostingDomain{}, ErrConflict
	}
	count := 0
	for _, existing := range m.hostingDomains {
		if existing.Domain.SiteID == d.SiteID {
			count++
		}
	}
	if count >= api.HostingDomainsPerSite {
		return api.HostingDomain{}, hostingQuota(api.HostingDomainsPerSite)
	}
	for host, id := range m.hostingDomainClaims {
		if host == d.Hostname || id == d.ID {
			return api.HostingDomain{}, ErrConflict
		}
	}
	if m.hostingDomains == nil {
		m.hostingDomains = make(map[string]memHostingDomain)
		m.hostingDomainClaims = make(map[string]string)
	}
	d.State, d.Revision, d.Generation = "pending_dns", 1, 1
	d.CreatedAt = time.Now().UTC()
	d.NextAttemptAt = d.CreatedAt
	d.Details = api.HostingDomainDetails{TLSStatus: "pending", Records: []api.HostingDNSRecord{}}
	m.hostingDomainClaims[d.Hostname] = d.ID
	m.hostingDomains[d.ID] = memHostingDomain{Domain: d}
	return cloneHostingDomain(d), nil
}

func (m *MemStore) ListHostingDomains(_ context.Context, account, site string) ([]api.HostingDomain, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.hostingSites[site].AccountID != account {
		return nil, ErrNotFound
	}
	out := []api.HostingDomain{}
	for _, row := range m.hostingDomains {
		if row.Domain.SiteID == site {
			out = append(out, cloneHostingDomain(row.Domain))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (m *MemStore) DeleteHostingDomain(_ context.Context, account, site, id string, revision int64) (api.HostingDomain, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.hostingDomains[id]
	if !ok || row.Domain.AccountID != account || row.Domain.SiteID != site || row.Domain.Revision != revision || row.Domain.State == "deleting" {
		return api.HostingDomain{}, ErrConflict
	}
	row.Domain.State = "deleting"
	row.Domain.Revision++
	row.Domain.Generation++
	row.Domain.NextAttemptAt = time.Now().UTC()
	row.Domain.LastErrorCode = ""
	m.hostingDomains[id] = row
	return cloneHostingDomain(row.Domain), nil
}

func (m *MemStore) ClaimHostingDomains(_ context.Context, now time.Time, token string) ([]HostingDomainLease, error) {
	if now.IsZero() || uuid.Validate(token) != nil {
		return nil, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	rows := []memHostingDomain{}
	for _, row := range m.hostingDomains {
		if !row.Domain.NextAttemptAt.After(now) && !row.Until.After(now) {
			rows = append(rows, row)
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Domain.NextAttemptAt.Equal(rows[j].Domain.NextAttemptAt) {
			return rows[i].Domain.ID < rows[j].Domain.ID
		}
		return rows[i].Domain.NextAttemptAt.Before(rows[j].Domain.NextAttemptAt)
	})
	out := []HostingDomainLease{}
	for _, row := range rows {
		if len(out) == api.HostingDomainPageSize {
			break
		}
		row.Token, row.Until = token, now.Add(api.HostingDomainLease)
		m.hostingDomains[row.Domain.ID] = row
		out = append(out, HostingDomainLease{Domain: cloneHostingDomain(row.Domain), Token: token})
	}
	return out, nil
}

func (m *MemStore) SaveHostingDomain(_ context.Context, lease HostingDomainLease, status string, details api.HostingDomainDetails) (HostingDomainLease, error) {
	if !validHostingDomainState(status, details) {
		return HostingDomainLease{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.hostingDomains[lease.Domain.ID]
	if !ok || row.Token != lease.Token || row.Token == "" || row.Domain.Revision != lease.Domain.Revision || row.Domain.Generation != lease.Domain.Generation || row.Domain.State == "deleting" && status != "deleting" {
		return HostingDomainLease{}, ErrConflict
	}
	if row.Domain.State != status || row.Domain.Details.AccessAUD != details.AccessAUD {
		row.Domain.Generation++
	}
	row.Domain.State, row.Domain.Details = status, details
	row.Domain = cloneHostingDomain(row.Domain)
	m.hostingDomains[row.Domain.ID] = row
	return HostingDomainLease{Domain: cloneHostingDomain(row.Domain), Token: lease.Token}, nil
}

func (m *MemStore) AcknowledgeHostingDomain(_ context.Context, lease HostingDomainLease, at time.Time) error {
	if at.IsZero() {
		return ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.hostingDomains[lease.Domain.ID]
	if !ok || row.Token == "" || row.Token != lease.Token || row.Domain.Generation != lease.Domain.Generation {
		return ErrConflict
	}
	if row.Domain.PublishedGeneration < row.Domain.Generation {
		row.Domain.PublishedAt = &at
	}
	row.Domain.PublishedGeneration = row.Domain.Generation
	m.hostingDomains[row.Domain.ID] = row
	return nil
}

func (m *MemStore) FinishHostingDomain(_ context.Context, lease HostingDomainLease, at time.Time, code string) error {
	if at.IsZero() || !validHostingDomainError(code) {
		return ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.hostingDomains[lease.Domain.ID]
	if !ok || row.Token == "" || row.Token != lease.Token || row.Domain.Revision != lease.Domain.Revision || row.Domain.Generation != lease.Domain.Generation {
		return ErrConflict
	}
	row.Domain.NextAttemptAt = hostingDomainNext(row.Domain, at, code)
	row.Domain.Attempts++
	row.Domain.LastErrorCode = code
	row.Token = ""
	row.Until = time.Time{}
	m.hostingDomains[row.Domain.ID] = row
	return nil
}

func (m *MemStore) ReleaseHostingDomainBatch(_ context.Context, token string) error {
	if uuid.Validate(token) != nil {
		return ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, row := range m.hostingDomains {
		if row.Token == token {
			row.Token = ""
			row.Until = time.Time{}
			m.hostingDomains[id] = row
		}
	}
	return nil
}

func (m *MemStore) PurgeHostingDomain(_ context.Context, lease HostingDomainLease, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.hostingDomains[lease.Domain.ID]
	if !ok || row.Token == "" || row.Token != lease.Token || row.Domain.State != "deleting" || row.Domain.Generation != lease.Domain.Generation || row.Domain.PublishedGeneration != row.Domain.Generation || row.Domain.PublishedAt == nil || now.Sub(*row.Domain.PublishedAt) < api.HostingDomainRemovalGrace {
		return ErrConflict
	}
	delete(m.hostingDomains, lease.Domain.ID)
	return nil
}

func (m *MemStore) RecoverHostingDomain(_ context.Context, account, site, id string, req api.RecoverHostingDomainRequest, now time.Time) (api.HostingDomain, error) {
	if uuid.Validate(req.ProviderID) != nil || req.ExpectedRevision < 1 || now.IsZero() {
		return api.HostingDomain{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.hostingDomains[id]
	d := row.Domain
	if !ok || d.AccountID != account || d.SiteID != site || d.Hostname != req.ExpectedHostname || d.Revision != req.ExpectedRevision || d.Details.ProviderID != "" || d.Details.VerifiedAt == nil || d.Details.CreateStartedAt == nil || now.Sub(*d.Details.CreateStartedAt) < api.HostingDomainRecoveryDelay {
		return api.HostingDomain{}, ErrConflict
	}
	row.Domain.Details.ProviderID = req.ProviderID
	row.Domain.Revision++
	row.Domain.NextAttemptAt = now
	row.Domain.LastErrorCode = ""
	row.Token, row.Until = "", time.Time{}
	m.hostingDomains[id] = row
	return cloneHostingDomain(row.Domain), nil
}
