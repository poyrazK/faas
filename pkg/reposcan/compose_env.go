package reposcan

import (
	"fmt"
	"io/fs"
	"strings"

	"gopkg.in/yaml.v3"
)

// ComposeEnv is the inline `environment:` of every service in the Compose
// model, values included, for the opt-in import (ADR-20261023085230). It is
// read apart from Scan so values never enter Result, a plan or a log.
type ComposeEnv struct {
	// Import is the top-level `x-gregale-env-import: true` opt-in.
	Import bool
	// Services maps a service name to its entries. A nil value is an entry
	// without one (`- KEY`, or `KEY:`), which Compose reads from the host.
	Services map[string]map[string]*string
}

type composeEnvDoc struct {
	Import   *bool                            `yaml:"x-gregale-env-import"`
	Services map[string]composeEnvService     `yaml:"services"`
	Compose  *composeEnvDocWithoutWrapperKeys `yaml:"compose"`
}

type composeEnvDocWithoutWrapperKeys struct {
	Import   *bool                        `yaml:"x-gregale-env-import"`
	Services map[string]composeEnvService `yaml:"services"`
}

type composeEnvService struct {
	Environment yaml.Node `yaml:"environment"`
	Profiles    []string  `yaml:"profiles"`
}

// ReadComposeEnv reads the same Compose model Scan does (base file, its
// automatic override, extends and include). A repository without a Compose
// file yields a zero ComposeEnv. Services behind inactive profiles are left
// out, as Scan leaves them out.
func ReadComposeEnv(fsys fs.FS) (ComposeEnv, error) {
	body, src, err := readFirstValidFile(fsys, composeFileNames)
	if err != nil || body == nil {
		return ComposeEnv{}, err
	}
	resolved, sources, err := resolveComposeFiles(fsys, body, src)
	if err != nil {
		return ComposeEnv{}, err
	}
	resolved, sources, _, err = resolveComposeLinks(fsys, resolved, sources)
	if err != nil {
		return ComposeEnv{}, err
	}
	var doc composeEnvDoc
	if err := yaml.Unmarshal(resolved, &doc); err != nil {
		return ComposeEnv{}, fmt.Errorf("reposcan: parse %s: %w", strings.Join(sources, " + "), err)
	}
	importFlag, services := doc.Import, doc.Services
	if len(services) == 0 && doc.Compose != nil {
		importFlag, services = doc.Compose.Import, doc.Compose.Services
	}
	out := ComposeEnv{Import: importFlag != nil && *importFlag, Services: map[string]map[string]*string{}}
	for name, service := range services {
		if name == "" || len(service.Profiles) > 0 {
			continue
		}
		entries, err := composeEnvEntries(&service.Environment)
		if err != nil {
			return ComposeEnv{}, fmt.Errorf("reposcan: %s: %s environment: %w", strings.Join(sources, " + "), name, err)
		}
		out.Services[name] = entries
	}
	return out, nil
}

// composeEnvEntries reads the map form (`KEY: value`) and the list form
// (`- KEY=value`) with each scalar's source text, so `1.0` stays `1.0`.
func composeEnvEntries(node *yaml.Node) (map[string]*string, error) {
	entries := map[string]*string{}
	switch node.Kind {
	case 0:
		return entries, nil
	case yaml.MappingNode:
		for i := 0; i+1 < len(node.Content); i += 2 {
			key, value := node.Content[i], node.Content[i+1]
			if key.Kind != yaml.ScalarNode || value.Kind != yaml.ScalarNode {
				return nil, fmt.Errorf("entries must be scalars")
			}
			if value.Tag == "!!null" {
				entries[key.Value] = nil
				continue
			}
			text := value.Value
			entries[key.Value] = &text
		}
	case yaml.SequenceNode:
		for _, item := range node.Content {
			if item.Kind != yaml.ScalarNode {
				return nil, fmt.Errorf("list entries must be KEY or KEY=value strings")
			}
			key, value, found := strings.Cut(item.Value, "=")
			if !found {
				entries[key] = nil
				continue
			}
			entries[key] = &value
		}
	default:
		return nil, fmt.Errorf("must be a map or a list")
	}
	return entries, nil
}
