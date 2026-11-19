//go:build metal && linux

package builderd

import (
	"context"
	"sync"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
)

// pingCapabilityTTL is how long builderd trusts vmmd's last answer to one of
// its Ping capabilities. vmmd still refuses what it cannot do, so a stale yes
// costs one requeue or one inline cache export.
const pingCapabilityTTL = 10 * time.Second

// pingCapability caches one of vmmd's Ping capabilities.
type pingCapability struct {
	mu        sync.Mutex
	supported bool
	at        time.Time
}

// get answers from the cache, or asks vmmd. An unreachable vmmd answers no.
func (c *pingCapability) get(ctx context.Context, cli vmmdpb.VmmdClient, has func(*vmmdpb.PingResponse) bool) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.at.IsZero() && time.Since(c.at) < pingCapabilityTTL {
		return c.supported
	}
	pingCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	resp, err := cli.Ping(pingCtx, &vmmdpb.PingRequest{})
	c.supported = err == nil && has(resp)
	c.at = time.Now()
	return c.supported
}

func (c *pingCapability) forget() {
	c.mu.Lock()
	c.at = time.Time{}
	c.mu.Unlock()
}

// SupportsBorrowedBuilders reports whether the node's vmmd runs builders on
// borrowed slots (ADR-20261118154632): builder_elastic_slots is on there and
// faas-borrowed.slice exists. An unreachable vmmd answers no.
func (d *VMMDriver) SupportsBorrowedBuilders(ctx context.Context) bool {
	if d == nil || d.cli == nil {
		return false
	}
	return d.borrowed.get(ctx, d.cli, (*vmmdpb.PingResponse).GetSupportsBorrowedBuilders)
}

// ForgetBorrowedCapability drops the cached answer, after vmmd refused a
// borrowed builder.
func (d *VMMDriver) ForgetBorrowedCapability() {
	if d == nil {
		return
	}
	d.borrowed.forget()
}

// SupportsCacheExportStaging reports whether the node's vmmd stages a
// deferred cache export run's inputs on the build's drive
// (ADR-20261029171525 amendment 2). A build defers its dependency cache
// export only then.
func (d *VMMDriver) SupportsCacheExportStaging(ctx context.Context) bool {
	if d == nil || d.cli == nil {
		return false
	}
	return d.cacheExportStaging.get(ctx, d.cli, (*vmmdpb.PingResponse).GetSupportsCacheExportStaging)
}
