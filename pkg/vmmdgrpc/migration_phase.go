package vmmdgrpc

// adr: 1545. Cancel/ack/expiry serialize; the pending capture reservation fences prepare.

import "context"

func (t *migrationTracker) beginPhase(ctx context.Context, instance string) (func(), error) {
	if t == nil {
		return func() {}, nil
	}
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		t.mu.Lock()
		if t.flights == nil {
			t.flights = make(map[string]chan struct{})
		}
		previous := t.flights[instance]
		if previous == nil {
			done := make(chan struct{})
			t.flights[instance] = done
			t.mu.Unlock()
			return func() { t.mu.Lock(); delete(t.flights, instance); close(done); t.mu.Unlock() }, nil
		}
		t.mu.Unlock()
		select {
		case <-previous:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}
