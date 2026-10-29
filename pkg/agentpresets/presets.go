// Package agentpresets is the catalog of agent harness presets Gregale
// publishes (ADR-20261027090010 §2). All of them live in the shared, read-only
// agent-base drive0 (images/agent-base.Dockerfile); a preset is a harness
// binary there plus the command a run starts it with, not an image of its
// own. presets.json is the one source: apid validates agent versions against
// it, and the image copies it to /usr/share/faas/agent-presets.json, where
// the image contract check compares every binary's version with it.
package agentpresets

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

var envName = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,63}$`)

// PromptPlaceholder is the argv entry a run replaces with its rendered
// prompt (ADR-20261027090010 §3).
const PromptPlaceholder = "{prompt}"

//go:embed presets.json
var raw []byte

// Preset is one published harness preset.
type Preset struct {
	Name    string `json:"name"`
	Harness string `json:"harness"`
	// Package and Version pin the npm package in images/agent-base.
	Package string `json:"package"`
	Version string `json:"version"`
	License string `json:"license"`
	// Binary is where the harness lives in agent-base.
	Binary string `json:"binary"`
	// Run is the default command; exactly one entry is PromptPlaceholder.
	Run []string `json:"run"`
	// Models maps a model provider to the base-URL variable the harness
	// reads; the run's environment points it at the relay.
	Models map[string]string `json:"models"`
	// Env is fixed environment every run of the preset gets, for example
	// Claude Code's documented DISABLE_AUTOUPDATER: drive0 is read-only and
	// a preset updates only by rebuilding agent-base.
	Env map[string]string `json:"env,omitempty"`
	// DefaultRAMMB sizes a first run before size history exists (§5),
	// lowered to the plan's AgentRunRAMMBMax.
	DefaultRAMMB int `json:"default_ram_mb"`
}

// Catalog is presets.json.
type Catalog struct {
	Version int      `json:"version"`
	Image   string   `json:"image"`
	Presets []Preset `json:"presets"`
}

var catalog = mustParse(raw)

// All returns every preset, in catalog order.
func All() []Preset {
	out := make([]Preset, len(catalog.Presets))
	for i, p := range catalog.Presets {
		out[i] = p.clone()
	}
	return out
}

// Lookup returns the named preset.
func Lookup(name string) (Preset, bool) {
	for _, p := range catalog.Presets {
		if p.Name == name {
			return p.clone(), true
		}
	}
	return Preset{}, false
}

// Names returns the preset names, in catalog order.
func Names() []string {
	out := make([]string, len(catalog.Presets))
	for i, p := range catalog.Presets {
		out[i] = p.Name
	}
	return out
}

// Image is the image every preset runs from.
func Image() string { return catalog.Image }

func (p Preset) clone() Preset {
	p.Run = slices.Clone(p.Run)
	p.Models = cloneMap(p.Models)
	p.Env = cloneMap(p.Env)
	return p
}

// Parse reads and checks a catalog.
func Parse(data []byte) (Catalog, error) {
	var c Catalog
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return Catalog{}, fmt.Errorf("agentpresets: decode: %w", err)
	}
	if c.Version != 1 || c.Image == "" || len(c.Presets) == 0 {
		return Catalog{}, fmt.Errorf("agentpresets: catalog needs version 1, an image and presets")
	}
	seen := map[string]bool{}
	for _, p := range c.Presets {
		if err := p.check(); err != nil {
			return Catalog{}, err
		}
		if seen[p.Name] {
			return Catalog{}, fmt.Errorf("agentpresets: preset %q listed twice", p.Name)
		}
		seen[p.Name] = true
	}
	return c, nil
}

func (p Preset) check() error {
	switch {
	case p.Name == "" || p.Package == "" || p.Version == "" || p.License == "":
		return fmt.Errorf("agentpresets: preset %q needs a name, package, version and license", p.Name)
	case !strings.HasPrefix(p.Binary, "/usr/local/bin/") || len(p.Run) == 0 || p.Run[0] != p.Binary[len("/usr/local/bin/"):]:
		return fmt.Errorf("agentpresets: preset %q must run its /usr/local/bin binary by name", p.Name)
	case count(p.Run, PromptPlaceholder) != 1:
		return fmt.Errorf("agentpresets: preset %q needs exactly one %s in its command", p.Name, PromptPlaceholder)
	case len(p.Models) == 0:
		return fmt.Errorf("agentpresets: preset %q names no model provider", p.Name)
	case p.DefaultRAMMB < 512 || p.DefaultRAMMB%512 != 0:
		return fmt.Errorf("agentpresets: preset %q default_ram_mb must be a multiple of 512", p.Name)
	}
	for name := range p.Env {
		if !envName.MatchString(name) || strings.HasPrefix(name, "FAAS_") || strings.HasPrefix(name, "GREGALE_") {
			return fmt.Errorf("agentpresets: preset %q env %q must be an environment variable outside FAAS_ and GREGALE_", p.Name, name)
		}
	}
	return nil
}

func cloneMap(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func count(list []string, want string) int {
	n := 0
	for _, s := range list {
		if s == want {
			n++
		}
	}
	return n
}

func mustParse(data []byte) Catalog {
	c, err := Parse(data)
	if err != nil {
		panic(err)
	}
	return c
}
