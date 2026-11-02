package edgedns

import (
	"context"
	"net/http"
	"time"
)

// ProbeTimeout bounds one readiness probe, shorter than the 2 s tick.
const ProbeTimeout = time.Second

// HTTPProbe asks a compute edge for /readyz on its control port over the
// private network, as faas-check-compute-edges does (ADR-1343 §5).
type HTTPProbe struct {
	Client *http.Client
}

// Ready reports whether url answered 200 within ProbeTimeout.
func (p HTTPProbe) Ready(ctx context.Context, url string) bool {
	client := p.Client
	if client == nil {
		client = http.DefaultClient
	}
	ctx, cancel := context.WithTimeout(ctx, ProbeTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false
	}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}
