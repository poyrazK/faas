package durable

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/durableentity"
	entitysocket "github.com/onebox-faas/faas/pkg/durableentity/socket"
)

func (s *Service) RunDurableEntityRegistryCleanup(ctx context.Context) {
	if s.Host == nil || !s.RegistryCleanupWorkerEnabled || !s.RegistryCleanupEnabled || s.TenantQuotas == nil {
		return
	}
	s.registryHealth.start(time.Now())
	defer s.registryHealth.stop()
	indexed, roots := "", ""
	indexFailures, rootFailures := 0, 0
	var indexDue, rootDue time.Time
	timer := time.NewTimer(0)
	defer timer.Stop()
	allow := func(id durableentity.ID) bool { return s.Apps[id.AppID] }
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		// Root repair runs independently of index availability, including at startup.
		// Both lanes share one bounded cycle; a broken index cannot suppress repair.
		cycle, stop := context.WithTimeout(ctx, 45*time.Second)
		if !time.Now().Before(rootDue) {
			page, err := s.TenantQuotas.SweepRegistryCleanups(cycle, roots, allow)
			delay := entitysocket.RegistryCleanupReconcileInterval
			if err != nil {
				roots = ""
				rootFailures = min(6, rootFailures+1)
				delay = min(5*time.Minute, delay*time.Duration(1<<rootFailures))
				if ctx.Err() == nil {
					s.Host.Logger().Warn("durable registry cleanup reconciliation deferred")
				}
			} else {
				roots = page.NextCursor
				rootFailures = 0
				if page.Failed > 0 && ctx.Err() == nil {
					s.Host.Logger().Warn("durable registry cleanup reconciliation scopes deferred", "scopes", page.Failed)
				}
			}
			rootDue = time.Now().Add(delay)
			s.registryHealth.observe(false, time.Now(), rootDue, err)
			s.logRegistryCleanupPage("reconciliation", page)
		}
		if cycle.Err() == nil && !time.Now().Before(indexDue) {
			page, err := s.TenantQuotas.SweepIndexedRegistryCleanups(cycle, indexed, allow)
			delay := entitysocket.RegistryCleanupWorkerPoll
			if err != nil {
				indexed = ""
				indexFailures = min(6, indexFailures+1)
				delay = min(5*time.Minute, delay*time.Duration(1<<indexFailures))
				if ctx.Err() == nil {
					s.Host.Logger().Warn("durable registry cleanup index discovery deferred")
				}
			} else {
				indexed = page.NextCursor
				indexFailures = 0
				if page.Failed > 0 && ctx.Err() == nil {
					s.Host.Logger().Warn("durable registry cleanup indexed scopes deferred", "scopes", page.Failed)
				}
			}
			indexDue = time.Now().Add(delay)
			s.registryHealth.observe(true, time.Now(), indexDue, err)
			s.logRegistryCleanupPage("index", page)
		}
		stop()
		if ctx.Err() != nil {
			return
		}
		timer.Reset(entitysocket.RegistryCleanupWorkerPoll)
	}
}

func (s *Service) logRegistryCleanupPage(lane string, p entitysocket.RegistryCleanupWorkerPage) {
	s.Host.Logger().Debug("durable registry cleanup page observations", "lane", lane, "scanned", p.Scanned, "active_observed", p.Active, "oldest_active_at", p.OldestActiveAt, "attempted", p.Attempted, "failed", p.Failed, "hints_pruned", p.Pruned, "hint_repair_failures", p.HintRepairFailures)
}
