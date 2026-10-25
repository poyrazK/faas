// adr: 1138
package main

import (
	"context"
	"net"
	"net/netip"
	"strings"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
)

// newServiceTraceInference asks the local vmmd for the traceparent of the one
// request the instance behind a guest service call's source address is
// serving (ADR-1138). vmmd resolves the caller from its own live identity, so
// no cached address mapping is involved. Any failure, a timeout or an older
// vmmd (Unimplemented) means no link.
func newServiceTraceInference(clients vmmdClientLookup, localNode *localNodeID, timeout time.Duration) func(context.Context, string) string {
	return func(ctx context.Context, remoteAddr string) string {
		host := strings.TrimSpace(remoteAddr)
		if parsed, _, err := net.SplitHostPort(host); err == nil {
			host = parsed
		}
		address, err := netip.ParseAddr(host)
		if err != nil || !address.Is4() {
			return ""
		}
		ctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		nodeID, err := localNode.Get(ctx)
		if err != nil {
			return ""
		}
		client, closer, ok := clients.ClientFor(ctx, nodeID)
		if !ok {
			return ""
		}
		defer func() { _ = closer.Close() }()
		resp, err := client.InflightRequestTrace(ctx, &vmmdpb.InflightRequestTraceRequest{SourceIp: address.String()})
		if err != nil {
			return ""
		}
		return resp.GetTraceparent()
	}
}
