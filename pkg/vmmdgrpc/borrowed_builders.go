package vmmdgrpc

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/builderslots"
)

// BorrowedBuilders is vmmd's half of elastic builder slots
// (ADR-20261118154632). It tells builderd, through Ping, whether this node
// runs builders on borrowed slots, and it destroys the newest borrowed
// builder itself when tenants need the memory and builderd has not given it
// back: the tenant slice plus borrowed builders above BuilderBorrowForcePct
// of the tenant fence, or MemAvailable below BuilderBorrowHostCriticalMB,
// for BuilderBorrowForceSustain.
//
// The force check runs whenever a borrowed builder exists, whatever
// builder_elastic_slots says, so turning the flag off never strands one
// without its give-back. It finds borrowed builders from faas-borrowed.slice
// itself, so it survives a vmmd restart.
type BorrowedBuilders struct {
	reader    builderslots.Reader
	log       *slog.Logger
	enabled   atomic.Bool
	now       func() time.Time
	kills     prometheus.Counter
	borrowedB prometheus.Gauge

	mu        sync.Mutex
	overSince time.Time
	preempted map[string]bool
}

// NewBorrowedBuilders reads memory through reader and registers its metrics
// on reg (nil skips them).
func NewBorrowedBuilders(reader builderslots.Reader, reg prometheus.Registerer, log *slog.Logger) *BorrowedBuilders {
	if log == nil {
		log = slog.Default()
	}
	b := &BorrowedBuilders{
		reader:    reader,
		log:       log,
		now:       time.Now,
		preempted: map[string]bool{},
		kills: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "vmmd_builder_preemptions_total",
			Help: "Borrowed builder VMs vmmd destroyed because tenants needed their memory and builderd had not given it back (ADR-20261118154632).",
		}),
		borrowedB: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "vmmd_builder_borrowed_memory_bytes",
			Help: "faas-borrowed.slice memory.current: memory borrowed builders hold out of the tenant slice's headroom (ADR-20261118154632).",
		}),
	}
	if reg != nil {
		reg.MustRegister(b.kills, b.borrowedB)
	}
	return b
}

// SetEnabled applies builder_elastic_slots.
func (b *BorrowedBuilders) SetEnabled(on bool) {
	if b != nil {
		b.enabled.Store(on)
	}
}

// Supported reports whether this vmmd runs BuildSpec.borrowed builders:
// builder_elastic_slots is on and faas-borrowed.slice exists.
func (b *BorrowedBuilders) Supported() bool {
	return b != nil && b.enabled.Load() && b.reader.BorrowedSliceExists()
}

// TakePreempted reports, once, whether vmmd destroyed instance for tenants.
func (b *BorrowedBuilders) TakePreempted(instance string) bool {
	if b == nil {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	preempted := b.preempted[instance]
	delete(b.preempted, instance)
	return preempted
}

// Run checks memory every BuilderBorrowCheckInterval until ctx ends. kill
// destroys one builder VM.
func (b *BorrowedBuilders) Run(ctx context.Context, kill func(context.Context, string) error) {
	t := time.NewTicker(api.BuilderBorrowCheckInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		b.tick(ctx, kill)
	}
}

func (b *BorrowedBuilders) tick(ctx context.Context, kill func(context.Context, string) error) {
	builders := b.borrowedInstances()
	if len(builders) == 0 {
		b.borrowedB.Set(0)
		b.mu.Lock()
		b.overSince = time.Time{}
		b.mu.Unlock()
		return
	}
	m, err := b.reader.Read()
	if err == nil {
		b.borrowedB.Set(float64(m.BorrowedMB << 20))
	}
	// An unreadable node counts as pressure: the give-back must not depend on
	// a reading succeeding.
	pressured := err != nil || builderslots.MustForce(m)
	now := b.now()
	b.mu.Lock()
	if !pressured {
		b.overSince = time.Time{}
		b.mu.Unlock()
		return
	}
	if b.overSince.IsZero() {
		b.overSince = now
		b.mu.Unlock()
		return
	}
	if now.Sub(b.overSince) < api.BuilderBorrowForceSustain {
		b.mu.Unlock()
		return
	}
	victim := ""
	for _, instance := range builders {
		if !b.preempted[instance] {
			victim = instance
			break
		}
	}
	if victim == "" {
		b.mu.Unlock()
		return
	}
	b.preempted[victim] = true
	// Re-arm: the next kill waits for another sustained reading, so memory
	// the last kill freed is seen before another build goes.
	b.overSince = time.Time{}
	b.mu.Unlock()

	b.log.Warn("vmmd: destroying a borrowed builder; tenants need its memory",
		"instance", victim, "tenant_mb", m.TenantMB, "tenant_max_mb", m.TenantMaxMB,
		"borrowed_mb", m.BorrowedMB, "available_mb", m.AvailableMB, "read_err", err)
	killCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if killErr := kill(killCtx, victim); killErr != nil {
		b.log.Error("vmmd: destroy borrowed builder", "instance", victim, "err", killErr)
		b.mu.Lock()
		delete(b.preempted, victim)
		b.mu.Unlock()
		return
	}
	b.kills.Inc()
}

// borrowedInstances lists the borrowed builder scopes, newest first. A scope
// directory is named for its instance (fcvm.PerInstanceScope); *.slice
// children are systemd fence pins, not VMs.
func (b *BorrowedBuilders) borrowedInstances() []string {
	dir := filepath.Join(b.reader.CgroupRoot, builderslots.BorrowedSlice)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	type scope struct {
		name string
		at   time.Time
	}
	var scopes []scope
	for _, e := range entries {
		if !e.IsDir() || strings.HasSuffix(e.Name(), ".slice") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		scopes = append(scopes, scope{name: e.Name(), at: info.ModTime()})
	}
	for i := 1; i < len(scopes); i++ {
		for j := i; j > 0 && newerScope(scopes[j].at, scopes[j].name, scopes[j-1].at, scopes[j-1].name); j-- {
			scopes[j], scopes[j-1] = scopes[j-1], scopes[j]
		}
	}
	out := make([]string, len(scopes))
	for i, s := range scopes {
		out[i] = s.name
	}
	return out
}

func newerScope(at time.Time, name string, otherAt time.Time, otherName string) bool {
	if !at.Equal(otherAt) {
		return at.After(otherAt)
	}
	return name > otherName
}

// WithBorrowedBuilders wires elastic builder slots (ADR-20261118154632):
// Ping's supports_borrowed_builders, the refusal of a borrowed cold boot this
// node cannot run, and Destroy's preempted_for_tenants.
func (s *Server) WithBorrowedBuilders(b *BorrowedBuilders) *Server {
	s.borrowed = b
	return s
}
