package reposcan

import (
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// composeCandidate is the loose, decoded shape of one
// docker-compose services entry. We read only the fields §3
// commits to and ignore the rest — healthcheck, network_mode,
// volumes, secrets, etc. are out of scope at this phase (they
// become env/ServiceTime/Limits in Phase 3's app row).
//
// Build is `any` because compose supports three shapes:
//
//	build: ./path                      (string → context shorthand)
//	build: {context: ./path}           (object form)
//	build:
//	  context: ./path
//	  dockerfile: Dockerfile           (object form with separate dockerfile key)
//
// A typed struct forces unmarshal errors; `any` lets the helper
// buildFromAny() below disambiguate all three.
type composeCandidate struct {
	Name        string   `yaml:"-"`
	Build       any      `yaml:"build"`
	Command     any      `yaml:"command"`    // string OR []string
	DependsOn   any      `yaml:"depends_on"` // []string OR map[string]any
	Ports       []any    `yaml:"ports"`      // "8080:80", 8080, {"target": 8080, …}
	EnvFile     any      `yaml:"env_file"`
	Environment any      `yaml:"environment"`
	Image       string   `yaml:"image"`
	Profiles    []string `yaml:"profiles"`

	ServiceBindingPolicy      string `yaml:"x-gregale-service-policy"`
	PreviewServiceCallsPolicy string `yaml:"x-gregale-preview-calls"`
}

// buildFromAny returns (context, dockerfile, present) from any
// of the three compose build: shapes.
func buildFromAny(v any) (string, string, bool, error) {
	switch x := v.(type) {
	case nil:
		return "", "", false, nil
	case string:
		s := strings.TrimSpace(x)
		if s == "" {
			return "", "", false, nil
		}
		return normalizeComposeRelativePath(s), "", true, nil
	case map[string]any:
		ctx, _ := x["context"].(string)
		rawDockerfile, _ := x["dockerfile"].(string)
		ctx = normalizeComposeRelativePath(ctx)
		df := normalizeComposeRelativePath(rawDockerfile)
		if strings.TrimSpace(rawDockerfile) != "" && df == "" {
			return "", "", false, fmt.Errorf("dockerfile path must name a file")
		}
		if ctx == "" && df == "" {
			return "", "", false, nil
		}
		return ctx, df, true, nil
	}
	return "", "", false, fmt.Errorf("unsupported build declaration")
}

func normalizeComposeRelativePath(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	value = strings.TrimSpace(strings.TrimPrefix(value, "./"))
	clean := path.Clean(value)
	if clean == "." {
		return "."
	}
	return clean
}

type composeDoc struct {
	Services map[string]composeCandidate `yaml:"services"`
}
type composeRoot struct {
	Compose composeDoc `yaml:"compose"`
}

// composeFileNames — the canonical compose filename set (impl plan §3).
var composeFileNames = []string{
	nameComposeYAML,
	nameComposeYML,
	nameDockerComposeYML,
	nameDockerComposeYAML,
}

// detectCompose reads the first present compose file from the
// candidate list, decodes services, and emits one workloadSeed per
// service plus one Managed for a denylisted prebuilt image (without
// build:) and a warning for a non-denylisted prebuilt image (the
// two-drive FROM-base rejection path).
//
// Env-key hygiene: environment can be `map[string]string` (KEY:
// value), `[]string` (KEY alone or KEY=value), or absent. We pull
// only the KEY portion; values are intentionally dropped per §11
// (never log secrets).
func detectCompose(fsys fs.FS) ([]workloadSeed, []Managed, []string, error) {
	body, src, err := readFirstValidFile(fsys, composeFileNames)
	if err != nil {
		return nil, nil, nil, err
	}
	if body == nil {
		// not present in this tarball — quiet skip
		return nil, nil, nil, nil
	}
	resolved, sources, err := resolveComposeFiles(fsys, body, src)
	if err != nil {
		return nil, nil, nil, err
	}
	src = strings.Join(sources, " + ")
	var c composeDoc
	if err := yaml.Unmarshal(resolved, &c); err != nil {
		return nil, nil, nil, fmt.Errorf("reposcan: parse %s: %w", src, err)
	}
	if len(c.Services) == 0 {
		// Try the wrapped form.
		var r composeRoot
		if err := yaml.Unmarshal(resolved, &r); err != nil || len(r.Compose.Services) == 0 {
			// Same warn-and-skip semantics; the wrapped form
			// also failed to find any services — quiet skip.
			return nil, nil, nil, nil //nolint:nilerr
		}
		c = r.Compose
	}

	// Deterministic service ordering.
	svcNames := make([]string, 0, len(c.Services))
	for n := range c.Services {
		if n != "" {
			svcNames = append(svcNames, n)
		}
	}
	sort.Strings(svcNames)

	var (
		seeds    []workloadSeed
		managed  []Managed
		warnings []string
	)
	composeEnv, envErr := readComposeEnv(fsys)
	if envErr != nil {
		return nil, nil, nil, envErr
	}
	for _, name := range svcNames {
		s := c.Services[name]
		if len(s.Profiles) > 0 {
			warnings = append(warnings, fmt.Sprintf("reposcan: %s: %s skipped because profiles %s are not active", src, name, strings.Join(s.Profiles, ",")))
			continue
		}
		if err := interpolateComposeCandidate(&s, composeEnv, src, name); err != nil {
			return nil, nil, nil, err
		}
		ctx, df, hasBuild, buildErr := buildFromAny(s.Build)
		if buildErr != nil {
			return nil, nil, nil, fmt.Errorf("reposcan: %s: %s has invalid build configuration: %w", src, name, buildErr)
		}
		if s.Build != nil && !hasBuild {
			return nil, nil, nil, fmt.Errorf("reposcan: %s: %s build configuration resolved empty", src, name)
		}
		serviceBindingPolicy, policyErr := normalizeServiceBindingPolicy(s.ServiceBindingPolicy)
		if policyErr != nil {
			return nil, nil, nil, fmt.Errorf("reposcan: %s: %s: %w", src, name, policyErr)
		}
		if !hasBuild && serviceBindingPolicy != "" {
			return nil, nil, nil, fmt.Errorf("reposcan: %s: %s x-gregale-service-policy requires a build workload", src, name)
		}
		previewServiceCallsPolicy, previewPolicyErr := normalizePreviewServiceCallsPolicy(s.PreviewServiceCallsPolicy)
		if previewPolicyErr != nil {
			return nil, nil, nil, fmt.Errorf("reposcan: %s: %s: %w", src, name, previewPolicyErr)
		}
		if !hasBuild && previewServiceCallsPolicy != "" {
			return nil, nil, nil, fmt.Errorf("reposcan: %s: %s x-gregale-preview-calls requires a build workload", src, name)
		}
		command, commandShell := commandSpec(s.Command)
		if hasBuild {
			if (ctx != "" && !fs.ValidPath(ctx)) || strings.HasPrefix(ctx, "../") ||
				(df != "" && (!fs.ValidPath(df) || strings.HasPrefix(df, "../"))) {
				return nil, nil, nil, fmt.Errorf("reposcan: %s: %s has invalid build context or Dockerfile path", src, name)
			}
			if df != "" {
				dockerfilePath := path.Join(ctx, df)
				info, statErr := fs.Stat(fsys, dockerfilePath)
				if statErr != nil || info.IsDir() {
					return nil, nil, nil, fmt.Errorf("reposcan: %s: %s Dockerfile %q does not exist in build context", src, name, df)
				}
			}
		}
		if !hasBuild && s.Image == "" {
			warnings = append(warnings, "reposcan: "+src+": "+name+
				" has no build: or image: — skipping")
			continue
		}
		if !hasBuild && s.Image != "" {
			// prebuilt image path: denylist hits → Managed,
			// otherwise warning (the two-drive FROM-base
			// constraint rejects arbitrary prebuilt base).
			if hint, ok := denylistKind(s.Image); ok {
				managed = append(managed, Managed{
					Name:    name,
					Kind:    imageBase(s.Image),
					EnvHint: hint,
					Source:  src + ": " + name,
					Image:   s.Image,
				})
			} else {
				warnings = append(warnings, "reposcan: "+src+": "+
					name+" has image: without build: ("+
					s.Image+") — refusing arbitrary prebuilt base")
			}
			continue
		}
		// build: path — emit a workloadSeed. Class stays empty so an
		// explicit hint from another detector can fill it. If no hint
		// exists, mergeByKey infers HTTP from a published port and worker
		// otherwise.
		seeds = append(seeds, workloadSeed{
			name:         name,
			rootDir:      ctx,
			dockerfile:   df,
			command:      command,
			commandShell: commandShell,
			dependsOn:    dependencyNames(s.DependsOn),

			serviceBindingPolicy:      serviceBindingPolicy,
			previewServiceCallsPolicy: previewServiceCallsPolicy,

			ports:   parsePorts(s.Ports),
			envKeys: envKeys(s.Environment),
			source:  src + ": " + name,
		})
	}
	return seeds, managed, warnings, nil
}

func normalizeServiceBindingPolicy(value string) (ServiceBindingPolicy, error) {
	switch normalized := strings.ToLower(strings.TrimSpace(value)); normalized {
	case "":
		return "", nil
	case string(ServiceBindingPolicyAccount):
		return ServiceBindingPolicyAccount, nil
	case string(ServiceBindingPolicyDeclared):
		return ServiceBindingPolicyDeclared, nil
	default:
		return "", fmt.Errorf("x-gregale-service-policy must be account or declared")
	}
}

func normalizePreviewServiceCallsPolicy(value string) (PreviewServiceCallsPolicy, error) {
	switch normalized := strings.ToLower(strings.TrimSpace(value)); normalized {
	case "":
		return "", nil
	case string(PreviewServiceCallsAllow):
		return PreviewServiceCallsAllow, nil
	case string(PreviewServiceCallsDeny):
		return PreviewServiceCallsDeny, nil
	default:
		return "", fmt.Errorf("x-gregale-preview-calls must be allow or deny")
	}
}

// dependencyNames normalizes Compose's short and long depends_on forms.
// Conditions (service_started/service_healthy/service_completed_successfully)
// are deliberately not carried into the project graph: Gregale's separate
// app VMs cannot share Compose's container lifecycle, so readiness is handled
// by the internal service proxy and the caller can still use the generated
// service URL. Names are sorted and deduplicated for stable plans.
func dependencyNames(v any) []string {
	var names []string
	switch x := v.(type) {
	case []any:
		for _, item := range x {
			if name, ok := item.(string); ok && strings.TrimSpace(name) != "" {
				names = append(names, strings.TrimSpace(name))
			}
		}
	case []string:
		for _, name := range x {
			if strings.TrimSpace(name) != "" {
				names = append(names, strings.TrimSpace(name))
			}
		}
	case map[string]any:
		for name := range x {
			if strings.TrimSpace(name) != "" {
				names = append(names, strings.TrimSpace(name))
			}
		}
	case map[any]any:
		for name := range x {
			if s, ok := name.(string); ok && strings.TrimSpace(s) != "" {
				names = append(names, strings.TrimSpace(s))
			}
		}
	}
	if len(names) == 0 {
		return nil
	}
	sort.Strings(names)
	out := names[:0]
	for _, name := range names {
		if len(out) == 0 || out[len(out)-1] != name {
			out = append(out, name)
		}
	}
	return out
}

// commandSlice normalizes the compose `command:` form which can
// be a string ("bundle exec rails s") or a sequence
// ([bundle, exec, rails, s]). Empty / unset yields nil.
func commandSpec(v any) ([]string, bool) {
	switch x := v.(type) {
	case nil:
		return nil, false
	case string:
		s := strings.TrimSpace(x)
		if s == "" {
			return nil, false
		}
		return []string{s}, true
	case []any:
		out := make([]string, 0, len(x))
		for _, e := range x {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		if len(out) == 0 {
			return nil, false
		}
		return out, false
	case []string:
		if len(x) == 0 {
			return nil, false
		}
		return x, false
	}
	return nil, false
}

func commandSlice(v any) []string {
	command, _ := commandSpec(v)
	return command
}

// envKeys pulls only the KEY from a compose `environment:` field.
// Supports:
//
//	map[string]string   ("VAR": "value")
//	map[string]any      ("VAR": "value" or number)
//	[]string            ("VAR" or "VAR=value")
//	nil
//
// Values are intentionally dropped — §11 forbids logging secret
// values. A warning fires for list-form entries that lack "="
// (the list form is the conventional place to declare a secret
// ref).
func envKeys(v any) []string {
	switch x := v.(type) {
	case nil:
		return nil
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		return keys
	case map[any]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			if ks, ok := k.(string); ok {
				keys = append(keys, ks)
			}
		}
		sort.Strings(keys)
		return keys
	case []any:
		keys := make([]string, 0, len(x))
		for _, e := range x {
			if s, ok := e.(string); ok {
				keys = append(keys, envKeyFromEntry(s))
			}
		}
		if len(keys) == 0 {
			return nil
		}
		sort.Strings(keys)
		return keys
	case []string:
		keys := make([]string, 0, len(x))
		for _, s := range x {
			keys = append(keys, envKeyFromEntry(s))
		}
		if len(keys) == 0 {
			return nil
		}
		sort.Strings(keys)
		return keys
	}
	return nil
}

func envKeyFromEntry(s string) string {
	if i := strings.Index(s, "="); i >= 0 {
		return s[:i]
	}
	return s
}

// parsePorts normalizes compose `ports:` which is a heterogeneous
// list:
//
//	"8080:80"                          — published 8080, container 80 → 8080
//	"8080"                             — published == container → 8080
//	"127.0.0.1:8080:80"                — host:published:container → 8080
//	"8080-8090:80"                     — published range, container 80 → not supported, drop
//	8080                               — YAML int form
//	{published: 8080, target: 80}      — long form → 8080
//
// We keep the PUBLISHED port so the customer's URL contract
// matches the confirm table.
func parsePorts(items []any) []int {
	if len(items) == 0 {
		return nil
	}
	var out []int
	for _, it := range items {
		switch x := it.(type) {
		case string:
			s := strings.TrimSpace(x)
			// Strip a "hostip:" prefix if present. We don't track
			// the binding address; only the published port.
			if i := strings.Index(s, ":"); i >= 0 {
				// Could be:
				//   "8080:80"               (2 parts)
				//   "127.0.0.1:8080:80"     (3 parts)
				//   "8080-8090:80"          (range — unsupported, drop silently)
				//   "8080"                  (1 part — same as container)
				parts := strings.Split(s, ":")
				switch len(parts) {
				case 1:
					// host == container
					n, err := strconv.Atoi(parts[0])
					if err == nil {
						out = append(out, n)
					}
				case 2:
					// "host:container" or "published:container"
					// Take the published.
					if strings.Contains(parts[0], "-") {
						break // range form, drop
					}
					n, err := strconv.Atoi(parts[0])
					if err == nil {
						out = append(out, n)
					}
				case 3:
					// "host_ip:published:container" — parts[1]
					if strings.Contains(parts[1], "-") {
						break
					}
					n, err := strconv.Atoi(parts[1])
					if err == nil {
						out = append(out, n)
					}
				}
			} else {
				n, err := strconv.Atoi(s)
				if err == nil {
					out = append(out, n)
				}
			}
		case int:
			out = append(out, x)
		case float64:
			out = append(out, int(x))
		case map[string]any:
			if p, ok := x["published"]; ok {
				out = append(out, intOf(p))
			} else if t, ok := x["target"]; ok {
				out = append(out, intOf(t))
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	sort.Ints(out)
	return out
}

func intOf(v any) int {
	switch x := v.(type) {
	case int:
		return x
	case float64:
		return int(x)
	case string:
		n, _ := strconv.Atoi(x)
		return n
	}
	return 0
}
