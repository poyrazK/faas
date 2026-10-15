package main

import (
	"context"

	"github.com/onebox-faas/faas/pkg/api"
)

// customDomainsRuntimeUnavailable reports the control plane's reason when
// this installation cannot issue certificates for customer hostnames (the
// custom-domains capability is runtime_unavailable: ADR-520 on-demand TLS is
// off). Adding a domain there leaves it pending forever while the CLI promised
// an automatic certificate (H10-64). A capability read failure or a
// plan-restricted capability does not block: the API decides those.
func customDomainsRuntimeUnavailable(ctx context.Context, c interface {
	GetCapabilities(context.Context) (api.CapabilitiesResponse, error)
}) (string, bool) {
	capabilities, err := c.GetCapabilities(ctx)
	if err != nil {
		return "", false
	}
	for _, capability := range capabilities.Capabilities {
		if capability.Key != "custom-domains" || capability.Enabled || capability.UnavailableReason != api.CapabilityUnavailableRuntime {
			continue
		}
		detail := capability.UnavailableDetail
		if detail == "" {
			detail = "Custom domains are not available on this Gregale installation."
		}
		return detail + " A domain added now would never get a certificate.", true
	}
	return "", false
}
