// adr: 20261104150000
package gateway

import (
	"context"
	"strings"

	"github.com/miekg/dns"
)

// UDP, TCP fallback, and subsequent upstreams share the caller's deadline.
// Partial replies never become successful answers or egress authorizations.
func exchangeServiceDNS(ctx context.Context, req *dns.Msg, upstream string) (*dns.Msg, error) {
	client := &dns.Client{Net: "udp", Timeout: serviceDiscoveryTimeout}
	resp, _, err := client.ExchangeContext(ctx, req, upstream)
	if err == nil && resp != nil && resp.Truncated {
		client.Net = "tcp"
		resp, _, err = client.ExchangeContext(ctx, req, upstream)
	}
	return resp, err
}

func (h *ServiceDiscoveryDNSHandler) writeForwardedDNS(w dns.ResponseWriter, req, resp *dns.Msg, upstream string) {
	if local := w.LocalAddr(); local != nil && strings.HasPrefix(local.Network(), "udp") {
		size := dns.MinMsgSize
		if opt := req.IsEdns0(); opt != nil {
			size = max(size, int(opt.UDPSize()))
		}
		// A complete TCP upstream reply can exceed the guest's UDP buffer.
		// Preserve TC so its TCP retry can retrieve the complete answer.
		resp.Truncate(size)
	}
	if err := w.WriteMsg(resp); err != nil {
		h.log.Debug("forwarded DNS response failed", "upstream", upstream, "err", err)
	}
}
