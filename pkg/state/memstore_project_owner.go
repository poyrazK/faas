package state

import (
	"context"
	"sort"
)

func (m *MemStore) ProjectByOrgSlug(_ context.Context, orgID, slug string) (Project, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, project := range m.projects {
		if project.OrgID == orgID && project.Slug == slug {
			return project, nil
		}
	}
	return Project{}, ErrNotFound
}

func (m *MemStore) ListProjectsForOrg(_ context.Context, orgID string) ([]Project, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	projects := make([]Project, 0)
	for _, project := range m.projects {
		if project.OrgID == orgID {
			projects = append(projects, project)
		}
	}
	sort.Slice(projects, func(i, j int) bool {
		if projects[i].CreatedAt.Equal(projects[j].CreatedAt) {
			return projects[i].ID < projects[j].ID
		}
		return projects[i].CreatedAt.After(projects[j].CreatedAt)
	})
	return projects, nil
}

func (m *MemStore) resolveProjectOwnerLocked(p *Project) error {
	if p.OrgID == "" {
		app := App{AccountID: p.AccountID}
		m.ensureAppOrgLocked(&app)
		p.OrgID = app.OrgID
	}
	if p.OrgID == "" {
		if _, exists := m.accounts[p.AccountID]; !exists {
			return ErrNotFound
		}
		return nil // Compatibility for lightweight account-only fixtures.
	}
	org, exists := m.orgs[p.OrgID]
	if !exists || org.Personal && (org.PersonalOwnerAccountID == nil || *org.PersonalOwnerAccountID != p.AccountID) {
		return ErrNotFound
	}
	if p.AccountID != org.ID {
		_, namespaceExists := m.accounts[p.AccountID]
		for _, existing := range m.projects {
			namespaceExists = namespaceExists || existing.AccountID == p.AccountID
		}
		for _, existing := range m.apps {
			namespaceExists = namespaceExists || existing.AccountID == p.AccountID
		}
		if !namespaceExists {
			return ErrNotFound
		}
	}
	for _, existing := range m.projects {
		if existing.OrgID == p.OrgID && existing.Slug == p.Slug && existing.ID != p.ID {
			return ErrConflict
		}
	}
	return nil
}

func (m *MemStore) checkProjectWorkloadOwnerLocked(app App) error {
	if app.ProjectID == "" {
		return nil
	}
	project, exists := m.projects[app.ProjectID]
	if !exists || project.AccountID != app.AccountID || project.OrgID != app.OrgID {
		return ErrConflict
	}
	return nil
}

func (m *MemStore) resourceNamespaceExistsLocked(id string) bool {
	if _, ok := m.accounts[id]; ok {
		return true
	}
	if _, ok := m.orgs[id]; ok {
		return true
	}
	for _, project := range m.projects {
		if project.AccountID == id {
			return true
		}
	}
	for _, app := range m.apps {
		if app.AccountID == id {
			return true
		}
	}
	return false
}
