package builderd

import (
	"context"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func deploymentPublicBuildURLs(ctx context.Context, store any, app state.App, dep state.Deployment) (map[string]string, error) {
	reader, ok := store.(interface {
		DeploymentPublicBuildURLs(context.Context, string) (map[string]string, error)
	})
	if !ok {
		if len(app.Manifest.PublicBuildURLs) != 0 {
			return nil, fmt.Errorf("public build configuration reader unavailable")
		}
		return nil, nil
	}
	urls, err := reader.DeploymentPublicBuildURLs(ctx, dep.ID)
	if err != nil {
		return nil, fmt.Errorf("read captured public build configuration: %w", err)
	}
	if api.ValidatePublicBuildURLs(urls) != nil {
		return nil, fmt.Errorf("captured public build configuration is invalid")
	}
	return urls, nil
}
