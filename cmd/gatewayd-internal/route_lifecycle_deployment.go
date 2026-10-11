package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/routelifecycle"
	"github.com/onebox-faas/faas/pkg/state"
)

type deploymentLifecycleKey struct{ accountID, appID, deploymentID string }

func (m *declaredRoutesMatcher) ResolveDeploymentRouteLifecycle(ctx context.Context, app gateway.App, deploymentID, path, method string) (routelifecycle.Metadata, error) {
	if deploymentID == "" {
		return routelifecycle.Metadata{}, nil
	}
	reader, ok := m.store.(interface {
		GetDeploymentOpenAPIDoc(context.Context, string, string) ([]byte, state.OpenAPIDocMeta, error)
	})
	if !ok {
		return routelifecycle.Metadata{}, nil
	}
	key := deploymentLifecycleKey{app.AccountID, app.ID, deploymentID}
	now := m.now()
	m.mu.RLock()
	policy, found := m.deploymentEntries[key]
	generation := m.generations[app.ID]
	m.mu.RUnlock()
	if !found || !now.Before(policy.expires) {
		doc, meta, err := reader.GetDeploymentOpenAPIDoc(ctx, deploymentID, app.AccountID)
		if errors.Is(err, state.ErrNotFound) {
			policy = declaredRoutePolicy{missing: true}
		} else if err != nil {
			return routelifecycle.Metadata{}, err
		} else {
			if meta.AppID != app.ID || meta.AccountID != app.AccountID || meta.DeploymentID != deploymentID || meta.Truncated || len(meta.DocSHA256) != 32 || (meta.Source != "cold_boot" && meta.Source != "manual_upload") {
				return routelifecycle.Metadata{}, fmt.Errorf("deployment lifecycle capture identity or completeness mismatch")
			}
			policy, err = compileOpenAPIDocument(doc)
			if err != nil {
				return routelifecycle.Metadata{}, err
			}
		}
		policy.expires = now.Add(m.ttl)
		m.mu.Lock()
		if m.generations[app.ID] == generation {
			m.deploymentEntries[key] = policy
		}
		m.mu.Unlock()
	}
	if policy.missing {
		return routelifecycle.Metadata{}, nil
	}
	template, matched := matchingDeclaredTemplate(policy.routes, path, method)
	if !matched {
		return routelifecycle.Metadata{}, nil
	}
	metadata, found := policy.lifecycle[strings.ToUpper(method)+" "+template]
	if !found && strings.EqualFold(method, "HEAD") {
		metadata = policy.lifecycle["GET "+template]
	}
	return metadata, nil
}
