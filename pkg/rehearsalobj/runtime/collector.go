package runtime

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/rehearsalobj"
)

// Collector is tied to one private listener incarnation. Replacements may clean
// old namespaces but cannot close their capture window or adopt their evidence.
type Collector struct {
	Catalog rehearsalobj.Catalog
	ID      string
	failed  atomic.Bool
}

func NewCollector(c rehearsalobj.Catalog) *Collector {
	return &Collector{Catalog: c, ID: uuid.NewString()}
}
func (c *Collector) Fail() { c.failed.Store(true) }
func (c *Collector) Step(ctx context.Context, after string) (string, error) {
	resources, e := c.Catalog.ActiveScenarioObjects(ctx, after)
	if e != nil {
		c.Fail()
		return "", e
	}
	if len(resources) == 0 {
		return "", nil
	}
	for _, resource := range resources {
		if resource.CollectorID != "" && resource.CollectorID != c.ID {
			continue
		}
		if resource.CollectorID == c.ID && (!c.failed.Load() || resource.Failed) {
			if resource.Status == "ready" {
				continue
			}
			pending := false
			for _, a := range resource.Attempts {
				pending = pending || a.Pending
			}
			if pending {
				continue
			}
		}
		_, e = c.Catalog.UpdateScenarioObjects(ctx, resource.AccountID, resource.RunID, "", func(r *rehearsalobj.Resource) error {
			if !r.ExpiresAt.After(time.Now()) || (r.Status != "ready" && r.Status != "sealing") {
				return nil
			}
			if r.CollectorID == "" && r.Status == "ready" {
				r.CollectorID = c.ID
			}
			if r.CollectorID != c.ID {
				return nil
			}
			if c.failed.Load() {
				r.Failed = true
			}
			if r.Status == "sealing" && !r.CollectorClosed {
				for _, a := range r.Attempts {
					if a.Pending {
						return nil
					}
				}
				r.CollectorClosed = true
			}
			return nil
		})
		if e != nil {
			c.Fail()
			return "", e
		}
	}
	return resources[len(resources)-1].ID, nil
}
func (c *Collector) Run(ctx context.Context) {
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	cursor := ""
	for {
		select {
		case <-ctx.Done():
			c.Fail()
			return
		case <-ticker.C:
		}
		work, cancel := context.WithTimeout(ctx, 3*time.Second)
		cursor, _ = c.Step(work, cursor)
		cancel()
	}
}
