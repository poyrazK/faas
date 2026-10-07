package main

// adr: 656
import (
	"context"
	"fmt"
	"net/http"

	"github.com/onebox-faas/faas/pkg/globalingress"
	"github.com/onebox-faas/faas/pkg/runtimequota"
)

func (c *Config) globalIngressConfig() globalingress.Config {
	return globalingress.Config{DirectoryID: c.GlobalIngressDirectoryID, AppsDomain: c.GlobalIngressAppsDomain, ManifestPath: c.GlobalIngressManifestPath, TrustBundlePath: c.GlobalIngressTrustBundlePath}
}

// Scope is checked against the actual database before installing any handler.
// Regional fleets cannot become a directory ingress frontend by configuration.
func configureGlobalIngress(ctx context.Context, cfg *Config, regionalDomain string, checkScope func(context.Context) error, next http.Handler) (http.Handler, func(), error) {
	c := cfg.globalIngressConfig()
	if err := c.Validate(regionalDomain); err != nil {
		return nil, nil, err
	}
	if !c.Enabled() {
		return next, func() {}, nil
	}
	if checkScope == nil {
		return nil, nil, fmt.Errorf("global ingress requires an unbound database scope")
	}
	if err := checkScope(ctx); err != nil {
		return nil, nil, fmt.Errorf("global ingress database scope: %w", err)
	}
	anchors, err := runtimequota.LoadTrustAnchors(c.TrustBundlePath)
	if err != nil {
		return nil, nil, fmt.Errorf("global ingress trust: %w", err)
	}
	trust, err := runtimequota.NewTrust(anchors)
	if err != nil {
		return nil, nil, err
	}
	gw, err := globalingress.New(c.AppsDomain, c.DirectoryID, trust, next, nil)
	if err != nil {
		return nil, nil, err
	}
	stop, err := gw.Start(ctx, c.ManifestPath)
	if err != nil {
		return nil, nil, fmt.Errorf("global ingress initial projection: %w", err)
	}
	return gw, stop, nil
}
