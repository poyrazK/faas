// Package sched holds schedd's policy core: the admission ledger, the idle
// reaper, and eviction selection (spec §4.3). schedd is the single writer to the
// instances table and a single process, so this in-memory accounting needs no
// distributed locking — just a short-held mutex.
package sched

import (
	"fmt"
	"sync"

	"github.com/onebox-faas/faas/pkg/api"
)

// Ledger is schedd's live RAM/vCPU/concurrency accounting. It is the mechanised
// form of invariants §6.2-1 and §6.2-2: admission is granted only when the box
// still has headroom below the 47,600 MB ceiling, the app is under its
// concurrency cap, and vCPU slots remain. A reservation is taken when an instance
// enters the RAM-counting set (WAKING) and released when it parks; concurrency is
// released earlier, when it enters SNAPSHOTTING (§6.2-1 excludes snapshotting).
type Ledger struct {
	mu          sync.Mutex
	residentRAM int // Σ(ram_mb + 8) over reserved instances
	usedVCPU    int
	perApp      map[string]int // app_id -> instances counting toward concurrency
	entries     map[string]*reservation
}

type reservation struct {
	appID       string
	admissionMB int // ram_mb + PerVMOverheadMB
	vcpu        int
	countsConc  bool // still in {WAKING,COLD_BOOTING,RUNNING}
}

// NewLedger returns an empty ledger.
func NewLedger() *Ledger {
	return &Ledger{perApp: map[string]int{}, entries: map[string]*reservation{}}
}

// Request is an admission request for one instance (a wake or a build).
type Request struct {
	Instance       string
	AppID          string
	Plan           api.Plan
	RAMMB          int // the app's ram_mb (already validated ≤ plan cap)
	VCPU           int // vcpus for this instance
	MaxConcurrency int // the app's configured max (already validated ≤ plan cap)
}

func (r Request) admissionMB() int { return r.RAMMB + api.PerVMOverheadMB }

// Admit reserves resources for one new instance, enforcing the headroom guard
// (spec §4.3). It checks concurrency first (a per-app limit the customer can act
// on) then box capacity. On success it records the reservation; on failure it
// reserves nothing and returns a *api.Problem.
func (l *Ledger) Admit(r Request) error {
	limits, ok := api.LimitsFor(r.Plan)
	if !ok {
		return fmt.Errorf("sched: admit: unknown plan %q", r.Plan)
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	if _, dup := l.entries[r.Instance]; dup {
		return fmt.Errorf("sched: admit: instance %q already admitted", r.Instance)
	}

	// Per-app concurrency (invariant §6.2-1). The app's configured max is capped
	// by the plan; use the tighter of the two defensively.
	maxConc := r.MaxConcurrency
	if maxConc <= 0 || maxConc > limits.MaxConcurrency {
		maxConc = limits.MaxConcurrency
	}
	if have := l.perApp[r.AppID]; have >= maxConc {
		return api.ErrPlanLimitConcurrency(limits, have)
	}

	// Box RAM headroom (invariant §6.2-2): resident + this + overhead ≤ ceiling.
	if l.residentRAM+r.admissionMB() > api.RAMAdmissionCeilingMB {
		return api.ErrCapacity(fmt.Sprintf(
			"RAM headroom: %d MB resident + %d MB requested exceeds the %d MB admission ceiling",
			l.residentRAM, r.admissionMB(), api.RAMAdmissionCeilingMB))
	}

	// vCPU slots (8× overcommit → 160 slots, spec §1).
	if l.usedVCPU+r.VCPU > api.VCPUSlots {
		return api.ErrCapacity(fmt.Sprintf(
			"vCPU slots: %d used + %d requested exceeds %d", l.usedVCPU, r.VCPU, api.VCPUSlots))
	}

	l.entries[r.Instance] = &reservation{appID: r.AppID, admissionMB: r.admissionMB(), vcpu: r.VCPU, countsConc: true}
	l.residentRAM += r.admissionMB()
	l.usedVCPU += r.VCPU
	l.perApp[r.AppID]++
	return nil
}

// BeginSnapshot drops an instance's concurrency contribution while keeping its
// RAM/vCPU reservation (it is still resident during SNAPSHOTTING, §6.2-2 but not
// §6.2-1). Idempotent.
func (l *Ledger) BeginSnapshot(instance string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	e := l.entries[instance]
	if e != nil && e.countsConc {
		e.countsConc = false
		l.perApp[e.appID]--
		l.cleanupApp(e.appID)
	}
}

// Release frees an instance's entire reservation when it parks/stops (§6.2-4).
// Unknown instances are ignored.
func (l *Ledger) Release(instance string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	e := l.entries[instance]
	if e == nil {
		return
	}
	delete(l.entries, instance)
	l.residentRAM -= e.admissionMB
	l.usedVCPU -= e.vcpu
	if e.countsConc {
		l.perApp[e.appID]--
		l.cleanupApp(e.appID)
	}
}

func (l *Ledger) cleanupApp(appID string) {
	if l.perApp[appID] <= 0 {
		delete(l.perApp, appID)
	}
}

// ResidentRAM returns the current Σ(ram+8) in MB.
func (l *Ledger) ResidentRAM() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.residentRAM
}

// HeadroomMB returns MB remaining below the admission ceiling.
func (l *Ledger) HeadroomMB() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return api.RAMAdmissionCeilingMB - l.residentRAM
}

// Concurrency returns the number of instances of appID counting toward its cap.
func (l *Ledger) Concurrency(appID string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.perApp[appID]
}

// UsedVCPU returns reserved vCPU slots.
func (l *Ledger) UsedVCPU() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.usedVCPU
}
