// adr: 20261022184531 — vmmd reports, on Ping, whether it can adopt each
// configured base generation, so schedd can mark a node that cannot degraded.
package vmmdgrpc

import (
	"context"
	"slices"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// BaseGenerationReadiness is the verdict on one configured runtime base.
type BaseGenerationReadiness struct {
	BaseKey    string
	Generation string
	// Adoptable is false only when the node proved it cannot boot
	// Generation (imaged's "stale" verdict, ADR-20261024191249).
	Adoptable bool
	Reason    string
}

// BaseGenerationCheck judges every configured base now. It may read shared
// storage, so Ping never calls it directly.
type BaseGenerationCheck func(context.Context) []BaseGenerationReadiness

// CachedBaseGenerationReadiness serves the latest verdicts without waiting
// and refreshes them in the background once they are older than
// api.BaseGenerationReadinessCheckIntervalSeconds. Ping must stay a cheap
// liveness probe: the first report after startup is empty.
type CachedBaseGenerationReadiness struct {
	check     BaseGenerationCheck
	mu        sync.Mutex
	report    []BaseGenerationReadiness
	checkedAt time.Time
	running   bool
}

// NewCachedBaseGenerationReadiness wraps check. A nil check reports nothing.
func NewCachedBaseGenerationReadiness(check BaseGenerationCheck) *CachedBaseGenerationReadiness {
	return &CachedBaseGenerationReadiness{check: check}
}

// Report returns the latest verdicts and starts one background check when
// they are stale.
func (c *CachedBaseGenerationReadiness) Report(ctx context.Context) []BaseGenerationReadiness {
	if c == nil || c.check == nil {
		return nil
	}
	c.mu.Lock()
	report := slices.Clone(c.report)
	start := !c.running && time.Since(c.checkedAt) >= api.BaseGenerationReadinessCheckIntervalSeconds*time.Second
	if start {
		c.running = true
	}
	c.mu.Unlock()
	if start {
		go func() {
			checkCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), api.BaseGenerationReadinessCheckTimeoutSeconds*time.Second)
			defer cancel()
			next := c.check(checkCtx)
			c.mu.Lock()
			c.report, c.checkedAt, c.running = next, time.Now(), false
			c.mu.Unlock()
		}()
	}
	return report
}

// WithBaseGenerationReadiness makes Ping report base generation readiness.
func (s *Server) WithBaseGenerationReadiness(readiness *CachedBaseGenerationReadiness) *Server {
	s.baseReadiness = readiness
	return s
}
