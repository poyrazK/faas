package receivers

import "context"

// IngestGate bounds concurrent gateway telemetry writes so they cannot take
// every connection in apid's database pool (api.TelemetryIngestDBConcurrency).
// A nil gate admits everything. The gateway publishers are asynchronous and
// drop on failure, so waiting here slows telemetry, never a tenant request.
type IngestGate chan struct{}

func NewIngestGate(n int) IngestGate {
	if n <= 0 {
		return nil
	}
	return make(IngestGate, n)
}

// acquire waits for a slot or the caller's cancellation.
func (g IngestGate) acquire(ctx context.Context) (func(), error) {
	if g == nil {
		return func() {}, nil
	}
	select {
	case g <- struct{}{}:
		return func() { <-g }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
