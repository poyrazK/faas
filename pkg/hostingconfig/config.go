// Package hostingconfig defines the small, source-controlled hosting
// override surface shared by the CLI manifest and zero-config profile
// inference.
package hostingconfig

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config contains optional overrides for the inferred API run contract.
// Empty fields preserve zero-config inference. The command is intentionally a
// shell command: this is the same contract used by the profile fallback when
// an OCI artifact has no entrypoint or command.
type Config struct {
	Start  string `yaml:"start,omitempty"`
	Port   int    `yaml:"port,omitempty"`
	Health string `yaml:"health,omitempty"`
}

// Validate checks the explicit values before they can affect a deployment.
func (c Config) Validate() error {
	if start := strings.TrimSpace(c.Start); start != "" {
		if len(start) > 2048 {
			return fmt.Errorf("start must be at most 2048 characters")
		}
		if strings.ContainsAny(start, "\x00\r\n") {
			return fmt.Errorf("start must not contain NUL or newline characters")
		}
	}
	if c.Port != 0 && (c.Port < 1 || c.Port > 65535) {
		return fmt.Errorf("port %d out of range; must be 1..65535", c.Port)
	}
	if health := strings.TrimSpace(c.Health); health != "" {
		if !strings.HasPrefix(health, "/") {
			return fmt.Errorf("health must start with '/' (got %q)", health)
		}
		if strings.ContainsAny(health, "\x00\r\n") {
			return fmt.Errorf("health must not contain NUL or newline characters")
		}
		if len(health) > 1024 {
			return fmt.Errorf("health must be at most 1024 characters")
		}
	}
	return nil
}

// Load finds gregale.yaml or gregale.yml in fsys. The returned bool reports
// whether a hosting block was present; a manifest without hosting config is
// not an error and leaves profile inference unchanged.
func Load(fsys fs.FS) (Config, bool, error) {
	for _, name := range []string{"gregale.yaml", "gregale.yml"} {
		body, err := fs.ReadFile(fsys, name)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return Config{}, false, fmt.Errorf("read %s: %w", name, err)
		}
		cfg, present, err := Parse(body)
		if err != nil {
			return Config{}, false, fmt.Errorf("parse %s: %w", name, err)
		}
		return cfg, present, nil
	}
	return Config{}, false, nil
}

// Parse extracts and strictly decodes the hosting mapping from a complete
// gregale manifest. Other top-level declarations are intentionally ignored;
// gregalemanifest owns their validation.
func Parse(body []byte) (Config, bool, error) {
	if len(bytes.TrimSpace(body)) == 0 {
		return Config{}, false, nil
	}
	var doc yaml.Node
	dec := yaml.NewDecoder(bytes.NewReader(body))
	if err := dec.Decode(&doc); err != nil {
		return Config{}, false, fmt.Errorf("decode: %w", err)
	}
	root := &doc
	if doc.Kind == yaml.DocumentNode {
		if len(doc.Content) == 0 {
			return Config{}, false, nil
		}
		root = doc.Content[0]
	}
	if root.Kind == 0 || root.Tag == "!!null" {
		return Config{}, false, nil
	}
	if root.Kind != yaml.MappingNode {
		return Config{}, false, fmt.Errorf("root must be a mapping")
	}
	for i := 0; i+1 < len(root.Content); i += 2 {
		key, value := root.Content[i], root.Content[i+1]
		if key.Value != "hosting" {
			continue
		}
		if value.Tag == "!!null" {
			return Config{}, false, nil
		}
		raw, err := yaml.Marshal(value)
		if err != nil {
			return Config{}, false, fmt.Errorf("encode hosting: %w", err)
		}
		var cfg Config
		hostingDecoder := yaml.NewDecoder(bytes.NewReader(raw))
		hostingDecoder.KnownFields(true)
		if err := hostingDecoder.Decode(&cfg); err != nil {
			return Config{}, false, fmt.Errorf("hosting: %w", err)
		}
		if err := cfg.Validate(); err != nil {
			return Config{}, false, err
		}
		return cfg, true, nil
	}
	return Config{}, false, nil
}
