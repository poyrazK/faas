//go:build metal && linux

package builderd

import (
	"context"
	"sync"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
)

// borrowedCapabilityTTL is how long builderd trusts vmmd's last answer to
// whether it runs borrowed builders. vmmd still refuses a borrowed cold boot
// it cannot run, so a stale yes costs one requeue.
const borrowedCapabilityTTL = 10 * time.Second

// borrowedCapability caches vmmd's supports_borrowed_builders.
type borrowedCapability struct {
	mu        sync.Mutex
	supported bool
	at        time.Time
}

// SupportsBorrowedBuilders reports whether the node's vmmd runs builders on
// borrowed slots (ADR-20261118154632): builder_elastic_slots is on there and
// faas-borrowed.slice exists. An unreachable vmmd answers no.
func (d *VMMDriver) SupportsBorrowedBuilders(ctx context.Context) bool {
	if d == nil || d.cli == nil {
		return false
	}
	d.borrowed.mu.Lock()
	defer d.borrowed.mu.Unlock()
	if !d.borrowed.at.IsZero() && time.Since(d.borrowed.at) < borrowedCapabilityTTL {
		return d.borrowed.supported
	}
	pingCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	resp, err := d.cli.Ping(pingCtx, &vmmdpb.PingRequest{})
	d.borrowed.supported = err == nil && resp.GetSupportsBorrowedBuilders()
	d.borrowed.at = time.Now()
	return d.borrowed.supported
}

// ForgetBorrowedCapability drops the cached answer, after vmmd refused a
// borrowed builder.
func (d *VMMDriver) ForgetBorrowedCapability() {
	if d == nil {
		return
	}
	d.borrowed.mu.Lock()
	d.borrowed.at = time.Time{}
	d.borrowed.mu.Unlock()
}
