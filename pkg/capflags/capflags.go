// Package capflags decides product capability flags for any daemon
// (ADR-20261023115741). Every capability in pkg/productcap has a platform
// flag; a sub-feature is released only while every ancestor is released, and
// a flag with no operator value decides by its capability's tier default.
//
// apid uses it through pkg/apid/capgate to hide routes and refuse settings,
// and decides realtime for realtimed, which has no flag source. schedd,
// githubd, gatewayd-internal and vmmd use it to stop the work a capability
// does outside apid when its flag is off.
package capflags

import (
	"os"
	"strings"

	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/productcap"
)

// ReleaseAllEnv is the test and acceptance switch that releases every
// generated capability flag at boot; a durable operator value still wins.
const ReleaseAllEnv = "FAAS_CAPABILITY_FLAGS_RELEASE_ALL"

// maxDepth bounds an ancestor walk; productcap.Validate already rejects loops.
const maxDepth = 8

// Decider decides capability flags from a platform-flag snapshot.
type Decider struct {
	flagKey  map[string]string
	fallback map[string]platformflags.Definition
	parent   map[string]string
	flags    func() *platformflags.Snapshot
}

// New builds a decider from the product catalog. flags returns the current
// snapshot; a nil snapshot, or one without a capability's flag, decides by
// the capability's tier default.
func New(catalog productcap.Catalog, flags func() *platformflags.Snapshot) *Decider {
	d := &Decider{
		flagKey:  map[string]string{},
		fallback: map[string]platformflags.Definition{},
		parent:   map[string]string{},
		flags:    flags,
	}
	for _, capability := range catalog.Capabilities {
		d.parent[capability.ID] = capability.Parent
		if capability.AlwaysOn {
			continue
		}
		key, ok := platformflags.CapabilityFlag(capability.ID)
		if !ok {
			key = platformflags.CapabilityFlagKey(capability.ID)
		}
		d.flagKey[capability.ID] = key
		d.fallback[capability.ID] = platformflags.TierDefault(capability.Maturity)
	}
	return d
}

// Load builds a decider from the embedded catalog.
func Load(flags func() *platformflags.Snapshot) (*Decider, error) {
	catalog, err := productcap.Load()
	if err != nil {
		return nil, err
	}
	return New(catalog, flags), nil
}

// Enabled decides a capability's flag, and its ancestors' flags, for one
// subject. A capability without a flag (always-on, or unknown) counts as on.
// A nil decider allows everything.
func (d *Decider) Enabled(capability string, subject platformflags.Subject) bool {
	if d == nil {
		return true
	}
	snapshot := d.snapshot()
	for id, depth := capability, 0; id != "" && depth < maxDepth; id, depth = d.parent[id], depth+1 {
		key, flagged := d.flagKey[id]
		if !flagged {
			continue
		}
		if _, _, ok := snapshot.Definition(key); ok {
			if !snapshot.Evaluate(key, subject).Enabled {
				return false
			}
			continue
		}
		if def := d.fallback[id]; !def.Enabled || !def.Default {
			return false
		}
	}
	return true
}

// Released reports whether a capability's kill switch, and every ancestor's,
// is on: the platform-wide decision, before any account is known.
func (d *Decider) Released(capability string) bool {
	if d == nil {
		return true
	}
	snapshot := d.snapshot()
	for id, depth := capability, 0; id != "" && depth < maxDepth; id, depth = d.parent[id], depth+1 {
		key, flagged := d.flagKey[id]
		if !flagged {
			continue
		}
		if _, _, ok := snapshot.Definition(key); ok {
			if !snapshot.GloballyEnabled(key) {
				return false
			}
			continue
		}
		if !d.fallback[id].Enabled {
			return false
		}
	}
	return true
}

// ReleasedByDefault reports whether a capability, and every ancestor, is on
// for an account no rule names: its kill switch and default are on. The
// anonymous spec and the SDK spec publish these (ADR-20261023115741).
func (d *Decider) ReleasedByDefault(capability string) bool {
	if d == nil {
		return true
	}
	snapshot := d.snapshot()
	for id, depth := capability, 0; id != "" && depth < maxDepth; id, depth = d.parent[id], depth+1 {
		key, flagged := d.flagKey[id]
		if !flagged {
			continue
		}
		def, _, ok := snapshot.Definition(key)
		if !ok {
			def = d.fallback[id]
		}
		if !def.Enabled || !def.Default {
			return false
		}
	}
	return true
}

// Known reports whether the catalog has the capability.
func (d *Decider) Known(capability string) bool {
	if d == nil {
		return false
	}
	_, ok := d.parent[capability]
	return ok
}

func (d *Decider) snapshot() *platformflags.Snapshot {
	if d.flags == nil {
		return nil
	}
	return d.flags()
}

// Bootstrap is the boot fallback a daemon seeds into its platform-flag Set:
// with ReleaseAllEnv set, every generated capability flag is on for every
// account; otherwise nothing is seeded and tier defaults apply.
func Bootstrap(getenv func(string) string) map[string]platformflags.Definition {
	if getenv == nil {
		getenv = os.Getenv
	}
	switch strings.ToLower(strings.TrimSpace(getenv(ReleaseAllEnv))) {
	case "1", "true", "yes", "on":
	default:
		return nil
	}
	out := map[string]platformflags.Definition{}
	for _, spec := range platformflags.Catalog() {
		if spec.Generated {
			out[spec.Key] = platformflags.Definition{Enabled: true, Default: true}
		}
	}
	return out
}
