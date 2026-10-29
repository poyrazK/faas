// Package capgate releases apid routes by product capability
// (ADR-20261023115741). Every route belongs to one capability in
// pkg/productcap, and every capability has a platform flag: a route whose
// flag is off for a request answers exactly like a route that does not exist.
//
// The gate decides twice. Before authentication it checks the flag's kill
// switch, so a capability switched off platform-wide is hidden from everyone,
// unauthenticated callers included. After authentication it decides the flag
// for the account, plan and organization, so a preview released to some
// accounts stays hidden from the rest.
package capgate

import (
	"strings"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/onebox-faas/faas/pkg/capflags"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/productcap"
)

// operatorRoutes are apid paths outside the product catalog. Operator and
// internal routes carry their own admin authentication and env gates; health
// and metrics are platform plumbing. They are never capability-gated.
var operatorRoutes = []string{
	"/v1/admin",
	"/v1/internal",
	"/v1/compute-nodes",
	// Admin-only and schedd-to-gateway routes that live beside customer ones.
	"/v1/invoices/{}/consume-credits",
	"/v1/invocations:dispatch_batch",
	"/healthz",
	"/readyz",
	"/metrics",
}

// Gate maps routes to capabilities and decides their flags.
type Gate struct {
	root     *node
	decider  *capflags.Decider
	settings map[string]string
	names    map[string]string

	hidden   *prometheus.CounterVec
	unmapped prometheus.Counter
}

type node struct {
	literal map[string]*node
	wild    *node
	// owner is the capability that owns this path and everything below it.
	owner string
	// exact is the capability that owns only this path.
	exact string
}

const operatorOwner = "\x00operator"

// New builds a gate from the product catalog. flags returns the current
// platform-flag snapshot; a nil snapshot, or one without a capability's flag,
// decides by the capability's tier default.
func New(catalog productcap.Catalog, flags func() *platformflags.Snapshot) *Gate {
	g := &Gate{
		root:     &node{},
		decider:  capflags.New(catalog, flags),
		settings: map[string]string{},
		names:    map[string]string{},
		hidden: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "apid_capability_hidden_requests_total",
			Help: "Requests answered as a missing route because their capability's platform flag is off.",
		}, []string{"capability", "stage"}),
		unmapped: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "apid_capability_unmapped_requests_total",
			Help: "Requests to an apid route that no product capability owns; allowed, and a catalog gap.",
		}),
	}
	for _, capability := range catalog.Capabilities {
		g.names[capability.ID] = capability.Name
		for _, setting := range capability.Settings {
			g.settings[setting] = capability.ID
		}
		for _, route := range capability.Routes {
			exact := strings.HasPrefix(route, "=")
			g.insert(strings.TrimPrefix(route, "="), capability.ID, exact)
		}
	}
	for _, route := range operatorRoutes {
		g.insert(route, operatorOwner, false)
	}
	return g
}

// Collectors returns the gate's metrics for registration.
func (g *Gate) Collectors() []prometheus.Collector {
	return []prometheus.Collector{g.hidden, g.unmapped}
}

func (g *Gate) insert(path, owner string, exact bool) {
	n := g.root
	for _, segment := range segments(path) {
		if segment == "{}" {
			if n.wild == nil {
				n.wild = &node{}
			}
			n = n.wild
			continue
		}
		if n.literal == nil {
			n.literal = map[string]*node{}
		}
		child, ok := n.literal[segment]
		if !ok {
			child = &node{}
			n.literal[segment] = child
		}
		n = child
	}
	if exact {
		n.exact = owner
	} else {
		n.owner = owner
	}
}

// Capability returns the capability that owns a ServeMux pattern
// ("GET /v1/apps/{slug}/realtime/endpoints") or a request path. ok is false
// for operator routes and for routes no capability owns.
func (g *Gate) Capability(patternOrPath string) (string, bool) {
	owner := g.owner(patternOrPath)
	if owner == "" || owner == operatorOwner {
		return "", false
	}
	return owner, true
}

// ReleasedByDefault reports whether a capability, and its ancestors, are on
// for an account no rule names.
func (g *Gate) ReleasedByDefault(capability string) bool {
	return g.decider.ReleasedByDefault(capability)
}

// SpecHidden reports an OpenAPI path a published spec leaves out: an
// operator route, or one whose capability is in hidden. Paths no capability
// owns stay published.
func (g *Gate) SpecHidden(path string, hidden map[string]bool) bool {
	switch owner := g.owner(path); owner {
	case operatorOwner:
		return true
	case "":
		return false
	default:
		return hidden[owner]
	}
}

func (g *Gate) owner(patternOrPath string) string {
	return g.lookup(segments(normalize(patternOrPath)))
}

// lookup returns the owner of the longest matching entry. A literal segment
// is preferred over a wildcard, and the walk backtracks into the wildcard
// branch when the literal branch has no owner.
func (g *Gate) lookup(segs []string) string {
	var walk func(n *node, i int) string
	walk = func(n *node, i int) string {
		if i == len(segs) {
			if n.exact != "" {
				return n.exact
			}
			return n.owner
		}
		var found string
		if segs[i] != "{}" {
			if child, ok := n.literal[segs[i]]; ok {
				found = walk(child, i+1)
			}
		}
		if found == "" && n.wild != nil {
			found = walk(n.wild, i+1)
		}
		if found == "" {
			found = n.owner
		}
		return found
	}
	return walk(g.root, 0)
}

// Released reports whether a matched route's capability has its kill switch
// on. It is the pre-authentication decision. Operator routes are released;
// a route no capability owns is released and counted as a catalog gap.
func (g *Gate) Released(pattern string) bool {
	switch owner := g.owner(pattern); owner {
	case operatorOwner:
		return true
	case "":
		g.unmapped.Inc()
		return true
	default:
		on := g.killSwitch(owner)
		if !on {
			g.hidden.WithLabelValues(owner, "kill_switch").Inc()
		}
		return on
	}
}

// Allowed decides a route's capability flag for one subject. It is the
// post-authentication decision; unowned routes are allowed.
func (g *Gate) Allowed(patternOrPath string, subject platformflags.Subject) bool {
	capability, ok := g.Capability(patternOrPath)
	if !ok {
		return true
	}
	on := g.enabled(capability, subject)
	if !on {
		g.hidden.WithLabelValues(capability, "account").Inc()
	}
	return on
}

// Enabled decides a capability's flag, and its ancestors' flags, for one
// subject. A capability without a flag (always-on, or unknown) counts as on.
func (g *Gate) Enabled(capability string, subject platformflags.Subject) bool {
	return g.decider.Enabled(capability, subject)
}

func (g *Gate) enabled(capability string, subject platformflags.Subject) bool {
	return g.decider.Enabled(capability, subject)
}

func (g *Gate) killSwitch(capability string) bool {
	return g.decider.Released(capability)
}

// normalize reduces a ServeMux pattern or request path to slash-separated
// segments with every wildcard written {}: the method and host are dropped,
// "{name}" and "{name...}" become "{}", and the "{$}" end anchor is removed.
func normalize(patternOrPath string) string {
	p := patternOrPath
	if method, rest, ok := strings.Cut(p, " "); ok && method != "" && !strings.Contains(method, "/") {
		p = rest
	}
	if i := strings.Index(p, "/"); i > 0 {
		p = p[i:] // host-qualified pattern
	}
	return p
}

func segments(path string) []string {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	out := parts[:0]
	for _, part := range parts {
		switch {
		case part == "" || part == "{$}":
			continue
		case strings.HasPrefix(part, "{") && strings.HasSuffix(part, "}"):
			out = append(out, "{}")
		default:
			out = append(out, part)
		}
	}
	return out
}
