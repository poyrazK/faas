package gregalemanifest

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/onebox-faas/faas/pkg/api"
)

// DevWatchConfig is `dev.watch` (ADR-970): `true` runs the package's dev
// script as a development server; an object names the command explicitly.
//
//	dev:
//	  watch: true
//	  # or
//	  watch:
//	    command: next dev
type DevWatchConfig struct {
	Enabled bool
	Command string
}

// UnmarshalYAML accepts a boolean or a {command: ...} object.
func (w *DevWatchConfig) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {
	case yaml.ScalarNode:
		var enabled bool
		if err := node.Decode(&enabled); err != nil {
			return fmt.Errorf("dev.watch must be true, false, or an object with command")
		}
		*w = DevWatchConfig{Enabled: enabled}
		return nil
	case yaml.MappingNode:
		for i := 0; i < len(node.Content); i += 2 {
			if key := node.Content[i].Value; key != "command" {
				return fmt.Errorf("dev.watch: unknown key %q (only command is supported)", key)
			}
		}
		var raw struct {
			Command string `yaml:"command"`
		}
		if err := node.Decode(&raw); err != nil {
			return fmt.Errorf("dev.watch.command must be a string")
		}
		*w = DevWatchConfig{Enabled: true, Command: strings.TrimSpace(raw.Command)}
		return nil
	}
	return fmt.Errorf("dev.watch must be true, false, or an object with command")
}

// MarshalYAML writes the shorter form when no command is set.
func (w DevWatchConfig) MarshalYAML() (any, error) {
	if w.Command == "" {
		return w.Enabled, nil
	}
	return map[string]string{"command": w.Command}, nil
}

// Validate checks the command shape the API accepts.
func (w *DevWatchConfig) Validate() error {
	if w == nil || w.Command == "" {
		return nil
	}
	if len(w.Command) > api.DevWatchCommandMaxBytes || strings.ContainsAny(w.Command, "\n\r\x00") {
		return fmt.Errorf("dev.watch.command must be one line of at most %d bytes", api.DevWatchCommandMaxBytes)
	}
	return nil
}
