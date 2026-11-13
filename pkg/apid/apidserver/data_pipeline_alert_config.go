package apidserver

import (
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/datapipeline"
)

// A shared cache bounds config-file reads across the monitor and delivery
// queues. A failed reload replaces the last good scope so it cannot continue
// delivering for accounts the operator may have removed.
type pipelineAlertConfigCache struct {
	mu       sync.Mutex
	load     func() (datapipeline.Config, error)
	now      func() time.Time
	loadedAt time.Time
	value    datapipeline.Config
	err      error
}

func (c *pipelineAlertConfigCache) current() (datapipeline.Config, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	if c.loadedAt.IsZero() || now.Before(c.loadedAt) || now.Sub(c.loadedAt) >= api.DataPipelineAlertWorkerInterval {
		c.value, c.err = c.load()
		c.loadedAt = now
	}
	return c.value, c.err
}
