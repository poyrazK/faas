// Package projectconfig is the schema of a repository's project-wide service
// settings: the `project:` block of the root gregale.yaml
// (ADR-20261107180000), and the legacy `.gregale/project.yaml` file it
// replaces (ADR-20261025093331). It has no dependencies beyond YAML so the
// manifest parser and the repository scanner share one definition.
package projectconfig

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	// LegacyPath is the deprecated standalone project settings file.
	LegacyPath = ".gregale/project.yaml"
	// ManifestKey is the root gregale.yaml key that holds the same settings.
	ManifestKey = "project"
)

// ManifestNames are the root manifest files that may carry a project block,
// in lookup order.
var ManifestNames = []string{"gregale.yaml", "gregale.yml"}

// Config is the project block. The legacy file's whole document has the same
// shape.
type Config struct {
	ServicePolicy string             `yaml:"service_policy,omitempty" json:"service_policy,omitempty"`
	Services      map[string]Service `yaml:"services,omitempty" json:"services,omitempty"`
}

// Service is one service's project-wide settings.
type Service struct {
	ServicePolicy string   `yaml:"service_policy,omitempty" json:"service_policy,omitempty"`
	Visibility    string   `yaml:"visibility,omitempty" json:"visibility,omitempty"`
	DependsOn     []string `yaml:"depends_on,omitempty" json:"depends_on,omitempty"`
}

// Empty reports whether the block declares nothing.
func (c Config) Empty() bool {
	return strings.TrimSpace(c.ServicePolicy) == "" && len(c.Services) == 0
}

// Validate checks values that do not depend on the scanned services. Service
// names are checked against the scan by the caller.
func (c Config) Validate() error {
	if err := validPolicy(c.ServicePolicy); err != nil {
		return err
	}
	for name, service := range c.Services {
		if strings.TrimSpace(name) == "" {
			return fmt.Errorf("services has an empty service name")
		}
		if err := validPolicy(service.ServicePolicy); err != nil {
			return fmt.Errorf("service %q: %w", name, err)
		}
		if service.Visibility != "" && service.Visibility != "public" && service.Visibility != "internal" {
			return fmt.Errorf("service %q visibility must be public or internal, got %q", name, service.Visibility)
		}
		for _, dependency := range service.DependsOn {
			if strings.TrimSpace(dependency) == "" {
				return fmt.Errorf("service %q has an empty depends_on entry", name)
			}
		}
	}
	return nil
}

func validPolicy(value string) error {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "project", "declared", "account":
		return nil
	}
	return fmt.Errorf("service_policy must be project, declared or account, got %q", value)
}

// Parse decodes a legacy file body strictly. An empty body is an empty config.
func Parse(body []byte) (Config, error) {
	var cfg Config
	decoder := yaml.NewDecoder(bytes.NewReader(body))
	decoder.KnownFields(true)
	if err := decoder.Decode(&cfg); err != nil && !errors.Is(err, io.EOF) {
		return Config{}, err
	}
	return cfg, nil
}

// FromManifest extracts the project block of a root manifest body. Other keys
// belong to the manifest parser and are ignored here; the block itself is
// decoded strictly. found is false when the manifest has no project key.
func FromManifest(body []byte) (cfg Config, found bool, err error) {
	node, err := manifestProjectNode(body)
	if err != nil || node == nil {
		return Config{}, false, err
	}
	cfg, err = decodeNode(node)
	if err != nil {
		return Config{}, true, fmt.Errorf("%s: %w", ManifestKey, err)
	}
	return cfg, true, nil
}

// DecodeNode decodes an already-parsed project block strictly.
func DecodeNode(node *yaml.Node) (Config, error) { return decodeNode(node) }

func decodeNode(node *yaml.Node) (Config, error) {
	if node.Tag == "!!null" {
		return Config{}, nil
	}
	raw, err := yaml.Marshal(node)
	if err != nil {
		return Config{}, err
	}
	return Parse(raw)
}

func manifestProjectNode(body []byte) (*yaml.Node, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(body, &doc); err != nil {
		return nil, err
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, nil
	}
	root := doc.Content[0]
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == ManifestKey {
			return root.Content[i+1], nil
		}
	}
	return nil, nil
}
