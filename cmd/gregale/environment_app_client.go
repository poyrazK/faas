package main

import (
	"context"

	"github.com/onebox-faas/faas/pkg/api"
)

// App configuration merges must read the same environment they will edit.
// The first read pins the revision: a command may read several times while
// merging, and the update must fail if anything changed after its first read.
type environmentAppClient struct {
	*api.Client
	environment string
	revision    *int64
}

func (c environmentAppClient) GetApp(ctx context.Context, slug string) (api.AppResponse, error) {
	if c.environment != "" {
		app, revision, err := c.GetAppInEnvironmentRevision(ctx, slug, c.environment)
		if err == nil && c.revision != nil && *c.revision < 0 {
			*c.revision = revision
		}
		return app, err
	}
	return c.Client.GetApp(ctx, slug)
}

func (c environmentAppClient) UpdateApp(ctx context.Context, slug string, request api.UpdateAppRequest) (api.AppResponse, error) {
	if c.environment != "" {
		var revision int64
		if c.revision != nil && *c.revision >= 0 {
			revision = *c.revision
		} else {
			_, observed, err := c.GetAppInEnvironmentRevision(ctx, slug, c.environment)
			if err != nil {
				return api.AppResponse{}, err
			}
			revision = observed
		}
		return c.UpdateAppInEnvironmentAtRevision(ctx, slug, c.environment, revision, request)
	}
	return c.Client.UpdateApp(ctx, slug, request)
}
