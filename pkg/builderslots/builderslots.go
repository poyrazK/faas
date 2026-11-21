// Package builderslots holds the rules elastic builder slots share across
// apid, builderd and vmmd (ADR-20261118154632): the shape of the per-node
// `builder_slots` runtime config, and the memory thresholds that admit a
// borrowed build and take its memory back for tenants.
//
// A node's slots are Reserved (funded out of tenant sizing, never preempted)
// plus Max-Reserved borrowed ones. A borrowed build runs only in memory the
// tenant slice is allowed but not using, and gives it back: builderd cancels
// and requeues the newest borrowed build above BuilderBorrowGiveBackPct, and
// vmmd destroys one itself above BuilderBorrowForcePct. Every reading is the
// node's own cgroupfs and /proc/meminfo, never the control plane.
package builderslots

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// Slots is one node's builder slots.
type Slots struct {
	Reserved int `json:"reserved"`
	Max      int `json:"max"`
}

// Borrowed is the number of slots above the reserved ones.
func (s Slots) Borrowed() int {
	return max(s.Max-s.Reserved, 0)
}

// Config is a `builder_slots` value: a fleet default and per-node overrides
// keyed by the node name daemons read from FAAS_NODE_NAME.
type Config struct {
	Default Slots            `json:"default"`
	Nodes   map[string]Slots `json:"nodes,omitempty"`
}

// DefaultSlots is a node's slots when `builder_slots` is unset: today's one
// reserved slot and two in all.
var DefaultSlots = Slots{Reserved: api.BuilderSlotsDefaultReserved, Max: api.BuilderSlotsDefaultMax}

// DefaultConfig is the compiled `builder_slots`.
func DefaultConfig() Config {
	return Config{Default: DefaultSlots}
}

// DefaultJSON is the compiled `builder_slots` as the runtime config catalog
// stores it.
func DefaultJSON() json.RawMessage {
	raw, _ := json.Marshal(DefaultConfig())
	return raw
}

// nodeNamePattern matches builds.builder_node_name's CHECK, the names
// compute nodes register under.
var nodeNamePattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,253}$`)

// wireConfig keeps "default" optional: a value that names only nodes leaves
// every other node at DefaultSlots.
type wireConfig struct {
	Default *Slots           `json:"default"`
	Nodes   map[string]Slots `json:"nodes"`
}

// Parse decodes and validates a `builder_slots` value. Unknown fields are
// refused so a misspelt key cannot silently mean the default.
func Parse(raw json.RawMessage) (Config, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return Config{}, errors.New("builder_slots must be a JSON object")
	}
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.DisallowUnknownFields()
	var wire wireConfig
	if err := dec.Decode(&wire); err != nil {
		return Config{}, fmt.Errorf("builder_slots: %w", err)
	}
	if dec.More() {
		return Config{}, errors.New("builder_slots: trailing data after the object")
	}
	cfg := DefaultConfig()
	if wire.Default != nil {
		cfg.Default = *wire.Default
	}
	if err := validSlots("default", cfg.Default); err != nil {
		return Config{}, err
	}
	if len(wire.Nodes) > api.BuilderSlotsNodeEntriesMax {
		return Config{}, fmt.Errorf("builder_slots: %d nodes named, at most %d", len(wire.Nodes), api.BuilderSlotsNodeEntriesMax)
	}
	names := make([]string, 0, len(wire.Nodes))
	for name := range wire.Nodes {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if !nodeNamePattern.MatchString(name) {
			return Config{}, fmt.Errorf("builder_slots: node name %q is not a compute node name", name)
		}
		if err := validSlots("nodes."+name, wire.Nodes[name]); err != nil {
			return Config{}, err
		}
	}
	if len(wire.Nodes) > 0 {
		cfg.Nodes = wire.Nodes
	}
	return cfg, nil
}

func validSlots(where string, s Slots) error {
	if s.Reserved < api.BuilderReservedSlotsMin || s.Reserved > api.BuilderReservedSlotsMax {
		return fmt.Errorf("builder_slots: %s.reserved is %d; it must be between %d and %d (ADR-20261118154632 amendment 1)",
			where, s.Reserved, api.BuilderReservedSlotsMin, api.BuilderReservedSlotsMax)
	}
	if s.Max < s.Reserved || s.Max > api.BuilderSlotsMax {
		return fmt.Errorf("builder_slots: %s.max is %d; it must be between reserved (%d) and %d", where, s.Max, s.Reserved, api.BuilderSlotsMax)
	}
	return nil
}

// For returns node's slots: its own entry, or the default.
func (c Config) For(node string) Slots {
	if s, ok := c.Nodes[node]; ok && node != "" {
		return s
	}
	return c.Default
}

// ReservedSlotsEnv carries the reserved builder slots a node is provisioned
// with (ADR-20261118154632 amendment 1). node_join writes it for builderd and
// vmmd from the manifest's builder_reserved_slots, next to the build slice and
// tenant fences it sizes for that count.
const ReservedSlotsEnv = "FAAS_BUILDER_RESERVED_SLOTS"

// ReservedFromEnv reads ReservedSlotsEnv. Unset, unparsable or below one is
// the compiled one slot; above api.BuilderReservedSlotsMax is clamped.
func ReservedFromEnv(getenv func(string) string) int {
	n, err := strconv.Atoi(strings.TrimSpace(getenv(ReservedSlotsEnv)))
	if err != nil {
		return api.BuilderSlotsDefaultReserved
	}
	return api.ClampBuilderReservedSlots(n)
}

// Provisioned sets s.Reserved to the reserved slots the node was provisioned
// with (FAAS_BUILDER_RESERVED_SLOTS, ADR-20261118154632 amendment 1): its
// build slice and tenant sizing fund exactly that many, so builder_slots
// cannot add or remove one. s.Max is raised to at least that count. It
// reports whether s changed.
func Provisioned(s Slots, reserved int) (Slots, bool) {
	reserved = api.ClampBuilderReservedSlots(reserved)
	out := Slots{Reserved: reserved, Max: max(s.Max, reserved)}
	return out, out != s
}

// Clamp lowers s.Max to the builds the node's CPUs can run at BuildVMVCPU
// each, never below s.Reserved. It reports whether it lowered it.
func Clamp(s Slots, onlineCPUs int) (Slots, bool) {
	fit := onlineCPUs / api.BuildVMVCPU
	if fit >= s.Max {
		return s, false
	}
	s.Max = max(fit, s.Reserved)
	return s, true
}
