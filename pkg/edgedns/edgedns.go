// Package edgedns keeps an app host's proxied address records in step with
// the compute nodes whose edges are ready (ADR-20261102081618 §3).
//
// Cloudflare spreads a name's connections over every address record behind
// it, but it does not health-check them: a node that stops answering keeps
// its share until its record goes. The Reconciler adds a node's record once
// its edge has answered several probes in a row, removes it after a few
// misses or as soon as the node is drained, and never removes the last one,
// so losing every probe (or the control plane) leaves traffic flowing.
package edgedns

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"slices"
)

// Defaults for the probe thresholds (ADR-20261102081618 §3).
const (
	DefaultFailAfter    = 3
	DefaultRestoreAfter = 5
)

// Node is one compute node as the registry and this tick's probe see it.
type Node struct {
	Name string
	// Public is the address Cloudflare connects to; invalid when the
	// manifest gives the node none, which keeps it out of DNS.
	Public netip.Addr
	// Active is false while the node is drained in the registry.
	Active bool
	// Ready reports whether the node's edge answered its readiness probe.
	Ready bool
}

// Name is one managed host name and the zone that holds it.
type Name struct {
	ZoneID string
	Host   string
}

// Record is an address record behind a managed name.
type Record struct {
	ID   string
	Addr netip.Addr
}

// DNS reads and writes a zone's address records.
type DNS interface {
	// Records returns name's A and AAAA records, and whether a CNAME holds
	// the name (an address record cannot sit beside one).
	Records(ctx context.Context, name Name) (records []Record, cname bool, err error)
	// Create adds a proxied address record for addr.
	Create(ctx context.Context, name Name, addr netip.Addr) error
	// Delete removes one record.
	Delete(ctx context.Context, name Name, id string) error
}

// Change is one record write a tick makes, or would make in a dry run.
type Change struct {
	Create bool
	Name   Name
	Addr   netip.Addr
	ID     string // the record a deletion removes
}

func (c Change) String() string {
	if c.Create {
		return fmt.Sprintf("create %s -> %s", c.Name.Host, c.Addr)
	}
	return fmt.Sprintf("delete %s -> %s (%s)", c.Name.Host, c.Addr, c.ID)
}

// Plan returns the writes that move a name's records to desired: creations
// first, so the set never empties on the way. With an empty desired set it
// deletes nothing: the last record is never removed (fail static).
func Plan(name Name, current []Record, desired []netip.Addr) []Change {
	var changes []Change
	have := make(map[netip.Addr]bool, len(current))
	for _, r := range current {
		have[r.Addr] = true
	}
	for _, addr := range desired {
		if !have[addr] {
			changes = append(changes, Change{Create: true, Name: name, Addr: addr})
		}
	}
	if len(desired) == 0 {
		return changes
	}
	for _, r := range current {
		if !slices.Contains(desired, r.Addr) {
			changes = append(changes, Change{Name: name, Addr: r.Addr, ID: r.ID})
		}
	}
	return changes
}

// health is one node's probe history.
type health struct {
	in            bool
	fails, passes int
}

// Config tunes a Reconciler.
type Config struct {
	Names []Name
	// FailAfter misses in a row take a node out; RestoreAfter answers in a
	// row put it back. Zero means the default.
	FailAfter, RestoreAfter int
	// DryRun logs and counts the changes without making them.
	DryRun bool
}

// Reconciler moves the managed names' records with the nodes' health. It is
// not safe for concurrent use: one loop calls Step.
type Reconciler struct {
	cfg     Config
	dns     DNS
	metrics *Metrics
	log     *slog.Logger
	nodes   map[string]*health
	seeded  bool
}

// NewReconciler returns a Reconciler. metrics may be nil.
func NewReconciler(cfg Config, dns DNS, metrics *Metrics, log *slog.Logger) (*Reconciler, error) {
	if len(cfg.Names) == 0 {
		return nil, errors.New("edgedns: no names to manage")
	}
	if cfg.FailAfter <= 0 {
		cfg.FailAfter = DefaultFailAfter
	}
	if cfg.RestoreAfter <= 0 {
		cfg.RestoreAfter = DefaultRestoreAfter
	}
	if log == nil {
		log = slog.Default()
	}
	return &Reconciler{cfg: cfg, dns: dns, metrics: metrics, log: log, nodes: map[string]*health{}}, nil
}

// Step folds one tick's registry and probe view into each node's health and
// moves every managed name to the nodes that are in. A node already behind a
// name when the Reconciler starts begins in, so a restart changes nothing.
func (r *Reconciler) Step(ctx context.Context, nodes []Node) error {
	current := make(map[Name][]Record, len(r.cfg.Names))
	held := make(map[Name]bool, len(r.cfg.Names))
	for _, name := range r.cfg.Names {
		records, cname, err := r.dns.Records(ctx, name)
		if err != nil {
			r.metrics.errored("read")
			return fmt.Errorf("edgedns: read %s: %w", name.Host, err)
		}
		current[name], held[name] = records, cname
	}
	if !r.seeded {
		r.seed(nodes, current)
	}
	desired := r.observe(nodes)

	var errs []error
	for _, name := range r.cfg.Names {
		if held[name] {
			// The tunnel's CNAME still holds the name; the operator moves it.
			r.log.Warn("edgedns: a CNAME holds the name; leaving it", "name", name.Host)
			r.metrics.setRecords(name.Host, len(current[name]))
			continue
		}
		changes := Plan(name, current[name], desired)
		if len(desired) == 0 && len(current[name]) > 0 {
			r.log.Warn("edgedns: no node is ready; keeping the records", "name", name.Host, "records", len(current[name]))
		}
		count := len(current[name])
		createFailed := false
		for _, c := range changes {
			if !c.Create && createFailed {
				// The replacement is not in place; deleting now could
				// leave the name with nothing behind it.
				continue
			}
			if err := r.apply(ctx, c); err != nil {
				errs = append(errs, err)
				createFailed = createFailed || c.Create
				continue
			}
			if c.Create {
				count++
			} else {
				count--
			}
		}
		r.metrics.setRecords(name.Host, count)
	}
	return errors.Join(errs...)
}

// seed starts the nodes already behind a managed name in.
func (r *Reconciler) seed(nodes []Node, current map[Name][]Record) {
	listed := map[netip.Addr]bool{}
	for _, records := range current {
		for _, rec := range records {
			listed[rec.Addr] = true
		}
	}
	for _, n := range nodes {
		r.nodes[n.Name] = &health{in: n.Public.IsValid() && listed[n.Public]}
	}
	r.seeded = true
}

// observe updates each node's health and returns the addresses of the nodes
// that are in, in node order.
func (r *Reconciler) observe(nodes []Node) []netip.Addr {
	var desired []netip.Addr
	for _, n := range nodes {
		h := r.nodes[n.Name]
		if h == nil {
			h = &health{}
			r.nodes[n.Name] = h
		}
		if n.Ready {
			h.passes, h.fails = h.passes+1, 0
		} else {
			h.passes, h.fails = 0, h.fails+1
			// Only a node that could be behind the names counts: a drained
			// node or one without an address is not expected to answer.
			if n.Active && n.Public.IsValid() {
				r.metrics.probeFailed(n.Name)
			}
		}
		switch {
		case !n.Active || !n.Public.IsValid():
			h.in = false
		case h.in && h.fails >= r.cfg.FailAfter:
			h.in = false
			r.log.Warn("edgedns: node's edge stopped answering; withdrawing it", "node", n.Name, "misses", h.fails)
		case !h.in && h.passes >= r.cfg.RestoreAfter:
			h.in = true
			r.log.Info("edgedns: node's edge is ready; adding it", "node", n.Name)
		}
		r.metrics.setNodeIn(n.Name, h.in)
		if h.in {
			desired = append(desired, n.Public)
		}
	}
	return desired
}

func (r *Reconciler) apply(ctx context.Context, c Change) error {
	action := "delete"
	if c.Create {
		action = "create"
	}
	if r.cfg.DryRun {
		r.log.Info("edgedns: would "+c.String(), "dry_run", true)
		r.metrics.changed(c.Name.Host, action, true)
		return nil
	}
	var err error
	if c.Create {
		err = r.dns.Create(ctx, c.Name, c.Addr)
	} else {
		err = r.dns.Delete(ctx, c.Name, c.ID)
	}
	if err != nil {
		r.metrics.errored(action)
		return fmt.Errorf("edgedns: %s: %w", c, err)
	}
	r.log.Info("edgedns: " + c.String())
	r.metrics.changed(c.Name.Host, action, false)
	return nil
}
