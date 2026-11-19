package fcvm

import (
	"context"
	"net/http"
)

// Readiness probes name the app's public host (ADR-20261119140000).
//
// The readiness and prime GETs dial the guest at its host address
// (<HostIP>:8080). Without a Host override they also send that address as
// Host, which an app that allow-lists its host names (Django ALLOWED_HOSTS,
// Rails config.hosts) rejects with 400, failing readiness for an app that is
// configured correctly for its traffic. schedd sends the app's public host
// as AppSpec.probe_host; the probe then sends that host and the
// X-Forwarded-Proto public traffic carries, so the app answers it as it
// answers a customer. The dial target is unchanged.

type probeHostKey struct{}

// withProbeHost scopes host to the readiness and prime GETs made with ctx.
// An empty host leaves ctx unchanged, keeping the legacy probe.
func withProbeHost(ctx context.Context, host string) context.Context {
	if host == "" {
		return ctx
	}
	return context.WithValue(ctx, probeHostKey{}, host)
}

func probeHostFrom(ctx context.Context) string {
	host, _ := ctx.Value(probeHostKey{}).(string)
	return host
}

// applyProbeHost makes req look like public traffic for the app when its
// context carries a probe host.
func applyProbeHost(req *http.Request) {
	host := probeHostFrom(req.Context())
	if host == "" {
		return
	}
	req.Host = host
	req.Header.Set("X-Forwarded-Proto", "https")
}
