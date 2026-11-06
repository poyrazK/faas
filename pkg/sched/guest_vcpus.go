// adr: 20261101160231

package sched

import (
	"context"
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
// guest_vcpu_millicores of CPU ceiling instead of their plan's count, and book
// their plan's baseline (ADR-20261101160231). An entry is an app ID or "*"
// for every app; an empty list keeps every app on its plan. Snapshots
// captured at the other count are refused by vmmd at restore, so a listed
// app cold-boots once. schedd sets it from the `guest_vcpu_from_cpu_apps`
// runtime-config key (ADR-20261101215228).
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

// SetCPUShape applies the runtime-config values that shape a listed app's
// CPU (ADR-20261101215228). The zero shape is the compiled one. A wake reads
// the shape once it starts, so instances booted before a change keep theirs
// until they park.
func (e *Engine) SetCPUShape(shape api.CPUShape) {
	e.cpuShape.Store(&shape)
}

func (e *Engine) loadCPUShape() api.CPUShape {
	if shape := e.cpuShape.Load(); shape != nil {
		return *shape
	}
	return api.CPUShape{}
}

// guestVCPUs is the guest vCPU count app boots and books with: the plan's,
// or for a listed app one per guest_vcpu_millicores of its CPU ceiling
// (appCPUMillicores), never more than the plan's.
func (e *Engine) guestVCPUs(app state.App, limits api.Limits) int {
	if !e.guestVCPUApps.Load().includes(app.ID) {
		return limits.VCPU
	}
	return e.loadCPUShape().VCPUs(e.appCPUMillicores(app, limits), limits.VCPU)
}

// appCPUMillicores is the sustained CPU ceiling (cpu.max) app boots, books
// and is policed with. A listed app on the app default gets its plan's burst
// ceiling: as compiled, one core on Free, Hobby and Pro, two on Scale. An app
// that chose less than a core keeps its choice (ADR-20261101160231 decision
// 1), unless the ceiling is lower still. Every other app keeps its configured
// value.
func (e *Engine) appCPUMillicores(app state.App, limits api.Limits) int {
	return listedAppCPUMillicores(e.guestVCPUApps.Load().includes(app.ID), effectiveAppCPUMillicores(app), limits, e.loadCPUShape())
}

// appCPUBookingMillicores is what an instance of app books against a node's
// CPU budget once its startup boost ends, and its share when the node is
// busy. A listed app books the smaller of its ceiling and its plan's
// baseline, 1,000 m per GB of plan RAM: 250 m on Free … 2,000 m on Scale
// (ADR-20261101160231 step 5b). Every other app books its configured value.
func (e *Engine) appCPUBookingMillicores(app state.App, limits api.Limits) int {
	ceiling := e.appCPUMillicores(app, limits)
	if !e.guestVCPUApps.Load().includes(app.ID) || limits.RAMMB <= 0 {
		return ceiling
	}
	return max(1, min(ceiling, e.loadCPUShape().BaselineForPlanRAM(limits.RAMMB), api.AppCPUBookingMaxMillicores))
}

// appCPUBookingStamp is the booking a new instance of app stores on its row:
// appCPUBookingMillicores for a listed app, zero (the app's own value) for
// every other.
func (e *Engine) appCPUBookingStamp(app state.App, limits api.Limits) int {
	if !e.guestVCPUApps.Load().includes(app.ID) {
		return 0
	}
	return e.appCPUBookingMillicores(app, limits)
}

// listedAppCPUMillicores maps an app's configured CPU to its ceiling: an app
// on the default gets the plan ceiling, and a smaller choice is kept unless
// the ceiling is lower.
func listedAppCPUMillicores(listed bool, configured int, limits api.Limits, shape api.CPUShape) int {
	if !listed || limits.RAMMB <= 0 {
		return configured
	}
	ceiling := shape.CeilingForPlanRAM(limits.RAMMB)
	if configured < api.DefaultAppCPUMillicores {
		return min(configured, ceiling)
	}
	return ceiling
}

// policyAppCPUMillicores maps a live CPU policy's configured value to the
// ceiling the app's instances boot with, so the policy push and the wake
// agree. Only a listed app needs its account's plan.
func (e *Engine) policyAppCPUMillicores(ctx context.Context, appID string, configured int) (int, error) {
	if !e.guestVCPUApps.Load().includes(appID) {
		return configured, nil
	}
	app, err := e.store.AppByID(ctx, appID)
	if err != nil {
		return 0, fmt.Errorf("sched: app CPU policy: read app %s: %w", appID, err)
	}
	acct, err := state.AppResourceAccount(ctx, e.store, app)
	if err != nil {
		return 0, fmt.Errorf("sched: app CPU policy: read account of app %s: %w", appID, err)
	}
	limits, ok := api.LimitsFor(acct.Plan)
	if !ok {
		return 0, fmt.Errorf("sched: app CPU policy: app %s has unknown plan %q", appID, acct.Plan)
	}
	return listedAppCPUMillicores(true, configured, limits, e.loadCPUShape()), nil
}
