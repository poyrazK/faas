package state

import (
	"context"
	"maps"

	"github.com/onebox-faas/faas/pkg/api"
)

func (m *MemStore) SetAppPublicBuildURLs(_ context.Context, account, id string, urls map[string]string) error {
	if !validStandardResourceRead(account, id) || api.ValidatePublicBuildURLs(urls) != nil {
		return ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	app, ok := m.apps[id]
	if !ok || app.AccountID != account || app.Status == AppDeleted || app.PreviewOfSlug != "" {
		return ErrNotFound
	}
	app.Manifest.PublicBuildURLs = maps.Clone(urls)
	m.apps[id] = app
	return nil
}

func (m *MemStore) DeploymentPublicBuildURLs(_ context.Context, id string) (map[string]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.deployments[id]; !ok {
		return nil, ErrNotFound
	}
	return maps.Clone(m.publicBuildURLs[id]), nil
}

func (m *MemStore) capturePublicBuildURLsLocked(id string, d Deployment) {
	if m.publicBuildURLs == nil {
		m.publicBuildURLs = map[string]map[string]string{}
	}
	if _, captured := m.publicBuildURLs[id]; captured {
		return
	}
	app := m.apps[d.AppID]
	if (d.Kind == DeploymentKindTarball || d.Kind == DeploymentKindGitHub || d.Kind == DeploymentKindDockerfile) &&
		(d.Scope == "" || d.Scope == api.DefaultEnvScope) && app.PreviewOfSlug == "" {
		m.publicBuildURLs[id] = maps.Clone(app.Manifest.PublicBuildURLs)
	}
}
