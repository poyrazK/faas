// Package productcap contains the customer-facing product capability catalog.
//
// This catalog is deliberately separate from provider and kernel capability
// bitsets. It answers a product question: what can a customer use, at which
// maturity, on which plan, and where is the acceptance evidence?
package productcap

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

//go:embed catalog.json
var catalogJSON []byte

// Maturity is the only supported product-availability vocabulary.
type Maturity string

const (
	MaturityInternal Maturity = "internal"
	MaturityPreview  Maturity = "preview"
	MaturityBeta     Maturity = "beta"
	MaturityGA       Maturity = "ga"
)

var (
	idPattern       = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
	operatorPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*$`)
	// routeSegmentPattern is one segment of a route ownership entry: a
	// literal (which may carry a `:action` suffix) or the `{}` wildcard.
	routeSegmentPattern = regexp.MustCompile(`^(\{\}|[A-Za-z0-9._~$-]+(:[A-Za-z0-9_-]+)?)$`)
	settingPattern      = regexp.MustCompile(`^(app|edge_rule|runtime|manifest|domain)\.[a-z0-9_.-]+(=[a-z0-9_-]+)?$`)
	cliPathPattern      = regexp.MustCompile(`^[a-z][a-z0-9-]*( [a-z][a-z0-9-]*){0,2}$`)
)

// Capability is one customer-facing product capability.
type Capability struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Category       string   `json:"category"`
	Description    string   `json:"description"`
	Maturity       Maturity `json:"maturity"`
	Plans          []string `json:"plans"`
	DocsURL        string   `json:"docs_url"`
	OperatorFlag   string   `json:"operator_flag,omitempty"`
	AcceptanceTest string   `json:"acceptance_test"`
	// Core marks platform-core capabilities (sign-in, API keys, billing,
	// GDPR, …). Each still has a platform flag, but turning it off needs an
	// explicit force (ADR-20261023115741).
	Core bool `json:"core,omitempty"`
	// Routes are the apid route paths the capability owns, with every
	// wildcard written `{}`. An entry owns its path and everything below it;
	// an entry starting with `=` owns only that exact path. The capability
	// gate decides a request by the longest matching entry.
	Routes []string `json:"routes,omitempty"`
	// Parent makes this a sub-feature: it is released only while its parent
	// (and every ancestor) is released too.
	Parent string `json:"parent,omitempty"`
	// AlwaysOn marks a capability that cannot be switched off (the VM
	// isolation, scale-to-zero, the CLI, the SDKs). It has no platform flag.
	AlwaysOn bool `json:"always_on,omitempty"`
	// Settings are the request fields and declarations the capability owns,
	// refused while its flag is off for the caller: "app.<field>" (any
	// non-zero value), "app.<field>=<value>" (one value), "edge_rule.<kind>",
	// "runtime.<runtime>", "manifest.<key>" or "domain.wildcard".
	Settings []string `json:"settings,omitempty"`
	// Consumers are the daemons besides apid that stop the capability's
	// background work while its flag is off (schedd, githubd,
	// gatewayd-internal, vmmd). realtimed has no flag source; apid decides
	// for it.
	Consumers []string `json:"consumers,omitempty"`
	// CLI are the gregale command paths the capability owns: a top-level
	// command ("realtime") or a verb path under one ("bucket lifecycle").
	// The CLI hides a command from help, completion and suggestions while
	// its capability is off for the account; the longest owned path decides.
	CLI []string `json:"cli,omitempty"`
}

// KnownConsumers are the daemons that may decide capability flags.
var KnownConsumers = []string{"schedd", "githubd", "gatewayd-internal", "vmmd", "meterd"}

// Catalog is the versioned on-disk product capability registry.
type Catalog struct {
	Version      int          `json:"version"`
	Capabilities []Capability `json:"capabilities"`
}

// Load returns the embedded catalog after applying all registry invariants.
func Load() (Catalog, error) {
	var catalog Catalog
	if err := json.Unmarshal(catalogJSON, &catalog); err != nil {
		return Catalog{}, fmt.Errorf("decode embedded catalog: %w", err)
	}
	if err := Validate(catalog); err != nil {
		return Catalog{}, err
	}
	return catalog, nil
}

// JSON returns the canonical machine-readable registry bytes.
func JSON() []byte {
	return append([]byte(nil), catalogJSON...)
}

// Validate enforces the closed vocabulary and the evidence required for every
// capability. It intentionally does not validate whether a referenced test
// exists: test names can live in another package or in a metal-only suite.
func Validate(catalog Catalog) error {
	if catalog.Version < 1 {
		return fmt.Errorf("version must be positive; got %d", catalog.Version)
	}
	if len(catalog.Capabilities) == 0 {
		return fmt.Errorf("capabilities must not be empty")
	}

	seen := make(map[string]struct{}, len(catalog.Capabilities))
	for i, capability := range catalog.Capabilities {
		if capability.ID == "" || !idPattern.MatchString(capability.ID) {
			return fmt.Errorf("capabilities[%d].id must be lowercase kebab-case; got %q", i, capability.ID)
		}
		if _, exists := seen[capability.ID]; exists {
			return fmt.Errorf("duplicate capability id %q", capability.ID)
		}
		seen[capability.ID] = struct{}{}
		if strings.TrimSpace(capability.Name) == "" {
			return fmt.Errorf("capability %q has empty name", capability.ID)
		}
		if strings.TrimSpace(capability.Category) == "" {
			return fmt.Errorf("capability %q has empty category", capability.ID)
		}
		if strings.TrimSpace(capability.Description) == "" {
			return fmt.Errorf("capability %q has empty description", capability.ID)
		}
		if !validMaturity(capability.Maturity) {
			return fmt.Errorf("capability %q has invalid maturity %q", capability.ID, capability.Maturity)
		}
		if err := validatePlans(capability.ID, capability.Maturity, capability.Plans); err != nil {
			return err
		}
		if capability.OperatorFlag != "" && !operatorPattern.MatchString(capability.OperatorFlag) {
			return fmt.Errorf("capability %q has invalid operator_flag %q", capability.ID, capability.OperatorFlag)
		}
		if strings.TrimSpace(capability.AcceptanceTest) == "" || !strings.Contains(capability.AcceptanceTest, "::Test") {
			return fmt.Errorf("capability %q must name an acceptance test as package::TestName", capability.ID)
		}
		if capability.Maturity != MaturityInternal {
			if !validDocsURL(capability.DocsURL) {
				return fmt.Errorf("capability %q needs a /docs URL when maturity is %q", capability.ID, capability.Maturity)
			}
		} else if strings.TrimSpace(capability.DocsURL) == "" {
			return fmt.Errorf("internal capability %q needs an operator documentation URL", capability.ID)
		}
	}

	ids := make([]string, 0, len(catalog.Capabilities))
	for _, capability := range catalog.Capabilities {
		ids = append(ids, capability.ID)
	}
	if !sort.StringsAreSorted(ids) {
		return fmt.Errorf("capabilities must be sorted by id")
	}
	if err := validateParents(catalog); err != nil {
		return err
	}
	if err := validateSettings(catalog); err != nil {
		return err
	}
	if err := validateCLI(catalog); err != nil {
		return err
	}
	for _, capability := range catalog.Capabilities {
		for _, consumer := range capability.Consumers {
			known := false
			for _, k := range KnownConsumers {
				known = known || k == consumer
			}
			if !known || capability.AlwaysOn {
				return fmt.Errorf("capability %q has an unknown or meaningless consumer %q", capability.ID, consumer)
			}
		}
	}
	return validateRoutes(catalog)
}

// validateSettings keeps setting ownership unambiguous and well formed.
func validateSettings(catalog Catalog) error {
	owner := map[string]string{}
	for _, capability := range catalog.Capabilities {
		for _, setting := range capability.Settings {
			if !settingPattern.MatchString(setting) {
				return fmt.Errorf("capability %q has an invalid setting %q", capability.ID, setting)
			}
			if capability.AlwaysOn {
				return fmt.Errorf("always-on capability %q cannot own settings: nothing could refuse them", capability.ID)
			}
			if other, exists := owner[setting]; exists {
				return fmt.Errorf("setting %q is owned by both %q and %q", setting, other, capability.ID)
			}
			owner[setting] = capability.ID
		}
	}
	return nil
}

// validateCLI keeps CLI command ownership unambiguous and well formed.
func validateCLI(catalog Catalog) error {
	owner := map[string]string{}
	for _, capability := range catalog.Capabilities {
		for _, path := range capability.CLI {
			if !cliPathPattern.MatchString(path) {
				return fmt.Errorf("capability %q has an invalid cli path %q", capability.ID, path)
			}
			if capability.AlwaysOn {
				return fmt.Errorf("always-on capability %q cannot own cli commands: nothing could hide them", capability.ID)
			}
			if other, exists := owner[path]; exists {
				return fmt.Errorf("cli path %q is owned by both %q and %q", path, other, capability.ID)
			}
			owner[path] = capability.ID
		}
	}
	return nil
}

// validateParents keeps the sub-feature tree well formed: every parent
// exists, no chain loops, and core or always-on capabilities are not
// sub-features (turning a parent off must never take a core one with it).
func validateParents(catalog Catalog) error {
	byID := make(map[string]Capability, len(catalog.Capabilities))
	for _, capability := range catalog.Capabilities {
		byID[capability.ID] = capability
	}
	for _, capability := range catalog.Capabilities {
		if capability.Parent == "" {
			continue
		}
		if capability.Core || capability.AlwaysOn {
			return fmt.Errorf("capability %q is core or always-on and cannot have a parent", capability.ID)
		}
		seen := map[string]bool{capability.ID: true}
		for id := capability.Parent; id != ""; id = byID[id].Parent {
			if _, ok := byID[id]; !ok {
				return fmt.Errorf("capability %q has unknown parent %q", capability.ID, id)
			}
			if seen[id] {
				return fmt.Errorf("capability %q has a parent loop through %q", capability.ID, id)
			}
			seen[id] = true
		}
	}
	for _, capability := range catalog.Capabilities {
		if capability.AlwaysOn && len(capability.Routes) > 0 {
			return fmt.Errorf("always-on capability %q cannot own routes: nothing could hide them", capability.ID)
		}
	}
	return nil
}

// validateRoutes keeps route ownership unambiguous: every entry is a
// well-formed path and no two capabilities own the same entry.
func validateRoutes(catalog Catalog) error {
	owner := map[string]string{}
	for _, capability := range catalog.Capabilities {
		for _, route := range capability.Routes {
			path := strings.TrimPrefix(route, "=")
			if !strings.HasPrefix(path, "/") || strings.HasSuffix(path, "/") && path != "/" {
				return fmt.Errorf("capability %q route %q must be an absolute path without a trailing slash", capability.ID, route)
			}
			for _, segment := range strings.Split(strings.TrimPrefix(path, "/"), "/") {
				if !routeSegmentPattern.MatchString(segment) {
					return fmt.Errorf("capability %q route %q has an invalid segment %q; write wildcards as {}", capability.ID, route, segment)
				}
			}
			if other, exists := owner[route]; exists {
				return fmt.Errorf("route %q is owned by both %q and %q", route, other, capability.ID)
			}
			owner[route] = capability.ID
		}
	}
	return nil
}

func validMaturity(maturity Maturity) bool {
	switch maturity {
	case MaturityInternal, MaturityPreview, MaturityBeta, MaturityGA:
		return true
	default:
		return false
	}
}

func validatePlans(id string, maturity Maturity, plans []string) error {
	known := map[string]int{"free": 0, "hobby": 1, "pro": 2, "scale": 3}
	previous := -1
	seen := make(map[string]struct{}, len(plans))
	for _, plan := range plans {
		rank, ok := known[plan]
		if !ok {
			return fmt.Errorf("capability %q has unknown plan %q", id, plan)
		}
		if _, exists := seen[plan]; exists {
			return fmt.Errorf("capability %q repeats plan %q", id, plan)
		}
		seen[plan] = struct{}{}
		if rank <= previous {
			return fmt.Errorf("capability %q plans must be ordered free, hobby, pro, scale", id)
		}
		previous = rank
	}
	if len(plans) == 0 && maturity != MaturityInternal {
		return fmt.Errorf("capability %q must have at least one entitled plan", id)
	}
	return nil
}

func validDocsURL(url string) bool {
	return strings.HasPrefix(url, "/docs/") || strings.HasPrefix(url, "https://gregale.dev/docs/")
}
