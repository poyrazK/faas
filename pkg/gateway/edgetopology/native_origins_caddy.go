package edgetopology

import (
	"context"
	"net"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
)

func (p *NativeOriginProbe) readNativeCaddy(ctx context.Context) (*caddyGraph, string, error) {
	ctx, cancel := context.WithTimeout(ctx, api.RuntimeUpgradePublicEdgeTopologyTimeout)
	defer cancel()
	transport := &http.Transport{Proxy: nil, DialContext: (&net.Dialer{}).DialContext, DisableKeepAlives: true, MaxResponseHeaderBytes: int64(api.DefaultMaxHeaderBytes)}
	defer transport.CloseIdleConnections()
	body, etag, err := readCaddyConfig(ctx, transport, p.caddy.endpoint, api.RuntimeUpgradePublicEdgeCaddyConfigMaxBytes)
	if err != nil {
		return nil, "", nativeReadError("fresh whole Caddy config")
	}
	graph, err := parseCaddyInventory(body)
	if err != nil {
		return nil, "", nativeReadError("supported whole Caddy graph")
	}
	return graph, etag, nil
}
