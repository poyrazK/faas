// Package apihostingcontract owns the production-shaped source fixtures used
// to keep API detection and framework inference stable across releases.
package apihostingcontract

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
)

//go:embed catalog.json
var catalogJSON []byte

// Fixture is one source-tree contract case.
type Fixture struct {
	ID          string `json:"id"`
	Description string `json:"description"`
	// Tags classify acceptance coverage (for example runtime, quick,
	// runtime-candidate, or sse). Candidate fixtures stay out of the runtime
	// matrix until a reference-node run qualifies them.
	Tags []string `json:"tags,omitempty"`
	// SourceRoot selects the app member inside a repository-context fixture.
	// Fixture file names remain repository-relative so workspace manifests and
	// sibling packages travel with the selected app into source deploys.
	SourceRoot string             `json:"source_root,omitempty"`
	Files      map[string]string  `json:"files"`
	Expected   Expected           `json:"expected"`
	Container  *ContainerContract `json:"container,omitempty"`
}

// ContainerContract is the image/runtime portion of an OCI fixture. The
// process fields mirror the subset projected by oci.ManifestFromConfig; the
// lifecycle flags make the platform promise explicit for a normal stateless
// HTTP container without requiring a metal test for every catalog change.
type ContainerContract struct {
	Entrypoint   []string          `json:"entrypoint,omitempty"`
	Cmd          []string          `json:"cmd,omitempty"`
	Env          map[string]string `json:"env,omitempty"`
	WorkingDir   string            `json:"working_dir,omitempty"`
	User         string            `json:"user,omitempty"`
	ExposedPorts []string          `json:"exposed_ports,omitempty"`
	HonorsPort   bool              `json:"honors_port"`
	Stateless    bool              `json:"stateless"`
	ScaleToZero  bool              `json:"scale_to_zero"`
	RequestWake  bool              `json:"request_wake"`
}

// Expected is the profile and readiness contract asserted by the fixture runner.
type Expected struct {
	Framework      string `json:"framework"`
	PackageManager string `json:"package_manager"`
	StartCommand   string `json:"start_command"`
	Port           int    `json:"port"`
	HealthPath     string `json:"health_path"`
	ConfigFile     string `json:"config_file,omitempty"`
	Inferred       bool   `json:"inferred"`
}

// Catalog is the versioned fixture catalog.
type Catalog struct {
	Version  int       `json:"version"`
	Fixtures []Fixture `json:"fixtures"`
}

// Load returns and validates the embedded fixture catalog.
func Load() (Catalog, error) {
	var catalog Catalog
	if err := json.Unmarshal(catalogJSON, &catalog); err != nil {
		return Catalog{}, fmt.Errorf("decode fixture catalog: %w", err)
	}
	if err := Validate(catalog); err != nil {
		return Catalog{}, err
	}
	return catalog, nil
}

// Validate checks identity, source shape, and expected profile fields. The
// runner performs behavioral validation against frameworkprofile separately.
func Validate(catalog Catalog) error {
	if catalog.Version < 1 {
		return fmt.Errorf("version must be positive; got %d", catalog.Version)
	}
	if len(catalog.Fixtures) == 0 {
		return fmt.Errorf("fixtures must not be empty")
	}
	idPattern := regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
	tagPattern := regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
	seen := make(map[string]struct{}, len(catalog.Fixtures))
	ids := make([]string, 0, len(catalog.Fixtures))
	for i, fixture := range catalog.Fixtures {
		if fixture.ID == "" || !idPattern.MatchString(fixture.ID) {
			return fmt.Errorf("fixtures[%d].id must be lowercase kebab-case; got %q", i, fixture.ID)
		}
		if _, ok := seen[fixture.ID]; ok {
			return fmt.Errorf("duplicate fixture id %q", fixture.ID)
		}
		seen[fixture.ID] = struct{}{}
		ids = append(ids, fixture.ID)
		if fixture.Description == "" || len(fixture.Files) == 0 {
			return fmt.Errorf("fixture %q needs a description and at least one file", fixture.ID)
		}
		if fixture.Expected.Framework == "" || fixture.Expected.PackageManager == "" || fixture.Expected.Port < 1 || fixture.Expected.Port > 65535 {
			return fmt.Errorf("fixture %q has invalid expected profile", fixture.ID)
		}
		if fixture.Expected.HealthPath == "" || fixture.Expected.HealthPath[0] != '/' {
			return fmt.Errorf("fixture %q has invalid expected health path %q", fixture.ID, fixture.Expected.HealthPath)
		}
		seenTags := make(map[string]struct{}, len(fixture.Tags))
		for _, tag := range fixture.Tags {
			if tag == "" || !tagPattern.MatchString(tag) {
				return fmt.Errorf("fixture %q has invalid tag %q", fixture.ID, tag)
			}
			if _, ok := seenTags[tag]; ok {
				return fmt.Errorf("fixture %q repeats tag %q", fixture.ID, tag)
			}
			seenTags[tag] = struct{}{}
		}
		if fixture.SourceRoot != "" {
			if path.IsAbs(fixture.SourceRoot) || path.Clean(fixture.SourceRoot) != fixture.SourceRoot || fixture.SourceRoot == "." || fixture.SourceRoot == ".." || strings.HasPrefix(fixture.SourceRoot, "../") {
				return fmt.Errorf("fixture %q has invalid source_root %q", fixture.ID, fixture.SourceRoot)
			}
		}
		if hasTag(fixture.Tags, "workspace") {
			if err := validateWorkspaceFixture(fixture); err != nil {
				return fmt.Errorf("fixture %q: %w", fixture.ID, err)
			}
		} else if fixture.SourceRoot != "" {
			return fmt.Errorf("fixture %q sets source_root without the workspace tag", fixture.ID)
		}
	}
	if !sort.StringsAreSorted(ids) {
		return fmt.Errorf("fixtures must be sorted by id")
	}
	return nil
}

// SelectRuntimeFixtures returns the reference-node fixtures for a catalog
// mode. The default and "quick" modes select the smoke subset; "full" selects
// accepted runtime fixtures; "qualify" adds runtime-candidate fixtures for
// an explicit acceptance run without changing the supported full matrix.
func SelectRuntimeFixtures(catalog Catalog, mode string) ([]Fixture, error) {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		mode = "quick"
	}
	switch mode {
	case "quick", "full", "qualify":
	default:
		return nil, fmt.Errorf("unknown runtime catalog mode %q; want quick, full, or qualify", mode)
	}

	selected := make([]Fixture, 0, len(catalog.Fixtures))
	for _, fixture := range catalog.Fixtures {
		accepted := hasTag(fixture.Tags, "runtime")
		candidate := mode == "qualify" && hasTag(fixture.Tags, "runtime-candidate")
		include := candidate || (accepted && (mode != "quick" || hasTag(fixture.Tags, "quick")))
		if include {
			selected = append(selected, fixture)
		}
	}
	if len(selected) == 0 {
		return nil, fmt.Errorf("runtime catalog mode %q selected no fixtures", mode)
	}
	return selected, nil
}

func validateWorkspaceFixture(fixture Fixture) error {
	if fixture.SourceRoot == "" {
		return fmt.Errorf("workspace fixture must select a nested source_root")
	}
	prefix := fixture.SourceRoot + "/"
	selectedMarker := false
	siblingManifest := false
	for name := range fixture.Files {
		if strings.HasPrefix(name, prefix) {
			rel := strings.TrimPrefix(name, prefix)
			switch rel {
			case "package.json", "go.mod", "pyproject.toml", "requirements.txt", "Pipfile", "setup.py", "Dockerfile":
				selectedMarker = true
			}
			continue
		}
		if strings.Contains(name, "/") {
			switch path.Base(name) {
			case "package.json", "go.mod", "pyproject.toml", "requirements.txt", "Pipfile", "setup.py", "Cargo.toml":
				siblingManifest = true
			}
		}
	}
	if !selectedMarker {
		return fmt.Errorf("workspace source_root %q has no app build manifest", fixture.SourceRoot)
	}
	if !siblingManifest {
		return fmt.Errorf("workspace fixture must include a sibling project manifest outside source_root %q", fixture.SourceRoot)
	}
	if !hasWorkspaceRootManifest(fixture.Files) {
		return fmt.Errorf("workspace fixture must include a root workspace manifest")
	}
	return nil
}

func hasWorkspaceRootManifest(files map[string]string) bool {
	if _, ok := files["go.work"]; ok {
		return true
	}
	if _, ok := files["pnpm-workspace.yaml"]; ok {
		return true
	}
	if strings.Contains(files["pyproject.toml"], "[tool.uv.workspace]") {
		return true
	}
	var packageJSON struct {
		Workspaces json.RawMessage `json:"workspaces"`
	}
	return json.Unmarshal([]byte(files["package.json"]), &packageJSON) == nil && len(packageJSON.Workspaces) > 0
}
