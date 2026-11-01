// adr: 20261101160231

package sched

import (
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// guestVCPUAppSet is the parsed schedd `guest_vcpu_from_cpu_apps` list: app
// IDs, or every app for "*".
type guestVCPUAppSet struct {
	all bool
	ids map[string]struct{}
}

func (s *guestVCPUAppSet) includes(appID string) bool {
	if s == nil {
		return false
	}
	if s.all {
		return true
	}
	_, ok := s.ids[strings.ToLower(appID)]
	return ok
}

// SetGuestVCPUFromCPUApps makes the listed apps boot one guest vCPU per
// 1,000 sustained millicores instead of their plan's count
// (ADR-20261101160231). An entry is an app ID or "*" for every app; an empty
// list keeps every app on its plan. Snapshots captured at the other count are
// refused by vmmd at restore, so a listed app cold-boots once.
func (e *Engine) SetGuestVCPUFromCPUApps(entries []string) error {
	set := &guestVCPUAppSet{ids: map[string]struct{}{}}
	for _, entry := range entries {
		entry = strings.TrimSpace(entry)
		switch entry {
		case "":
			continue
		case "*":
			set.all = true
		default:
			id, err := uuid.Parse(entry)
			if err != nil {
				return fmt.Errorf("sched: guest_vcpu_from_cpu_apps: %q is not an app ID or *", entry)
			}
			set.ids[id.String()] = struct{}{}
		}
	}
	if !set.all && len(set.ids) == 0 {
		set = nil
	}
	e.guestVCPUApps.Store(set)
	return nil
}

// guestVCPUs is the guest vCPU count app boots and books with: the plan's,
// or for a listed app one per 1,000 sustained millicores.
func (e *Engine) guestVCPUs(app state.App, planVCPUs int) int {
	if !e.guestVCPUApps.Load().includes(app.ID) {
		return planVCPUs
	}
	return api.GuestVCPUsForCPUMillicores(effectiveAppCPUMillicores(app))
}
