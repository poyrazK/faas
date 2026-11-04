package gateway

import (
	"context"
	"time"
)

// concurrencyQueueLeaseAcquires and concurrencyQueueLeaseReleases cap this
// gateway's in-flight fleet permit calls. An acquire takes the app's advisory
// lock in gateway_try_acquire_concurrency_queue_lease, so acquires for a hot
// app run one at a time however many wait, and every waiter held one of
// gatewayd-internal's eight pool connections (pkg/db). In a 2026-10-31 prod
// burst the leases held all of them: ADR-570 placement and readiness reads
// missed their one-second deadline, the app's targets became unroutable, and
// requests its running instances could serve were refused. A release takes no
// advisory lock and has its own slot, so a permit is not leaked waiting
// behind queued acquires.
const (
	concurrencyQueueLeaseAcquires = 2
	concurrencyQueueLeaseReleases = 1
)

// slottedQueueAdmission bounds concurrent calls into a shared permit backend.
// A call that cannot get a slot before its context ends returns the
// context's error, which the caller already reports as a queue wait timeout.
type slottedQueueAdmission struct {
	next     ConcurrencyQueueAdmission
	acquires chan struct{}
	releases chan struct{}
}

func boundQueueAdmission(next ConcurrencyQueueAdmission) ConcurrencyQueueAdmission {
	if next == nil {
		return nil
	}
	return &slottedQueueAdmission{
		next:     next,
		acquires: make(chan struct{}, concurrencyQueueLeaseAcquires),
		releases: make(chan struct{}, concurrencyQueueLeaseReleases),
	}
}

func (a *slottedQueueAdmission) TryAcquireConcurrencyQueueLease(ctx context.Context, appID string, limit int, ttl time.Duration) (string, int, bool, error) {
	if err := takeAdmissionSlot(ctx, a.acquires); err != nil {
		return "", 0, false, err
	}
	defer func() { <-a.acquires }()
	return a.next.TryAcquireConcurrencyQueueLease(ctx, appID, limit, ttl)
}

func (a *slottedQueueAdmission) ReleaseConcurrencyQueueLease(ctx context.Context, appID, leaseID string) error {
	if err := takeAdmissionSlot(ctx, a.releases); err != nil {
		return err
	}
	defer func() { <-a.releases }()
	return a.next.ReleaseConcurrencyQueueLease(ctx, appID, leaseID)
}

func takeAdmissionSlot(ctx context.Context, slots chan struct{}) error {
	select {
	case slots <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
