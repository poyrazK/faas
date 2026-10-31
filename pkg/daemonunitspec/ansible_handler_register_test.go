package daemonunitspec

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// A handler whose `when` reads a variable that another handler registers runs
// with that variable undefined unless every notification that fires it also
// fires the registering handler. The rc.292 control-plane converge
// (2026-10-24) failed "'systemd_available' is undefined" this way: tasks
// notified "daemon-reload postgres_backup" by name, and the handler that
// registers systemd_available listened on nothing.

type ansibleHandler struct {
	Name     string    `yaml:"name"`
	Listen   yaml.Node `yaml:"listen"`
	Register string    `yaml:"register"`
	When     yaml.Node `yaml:"when"`
}

func (h ansibleHandler) triggers() map[string]bool {
	out := map[string]bool{}
	if h.Name != "" {
		out[h.Name] = true
	}
	switch h.Listen.Kind {
	case yaml.ScalarNode:
		out[h.Listen.Value] = true
	case yaml.SequenceNode:
		for _, item := range h.Listen.Content {
			out[item.Value] = true
		}
	}
	return out
}

func (h ansibleHandler) listens() map[string]bool {
	out := h.triggers()
	delete(out, h.Name)
	return out
}

func (h ansibleHandler) whenText() string {
	switch h.When.Kind {
	case yaml.ScalarNode:
		return h.When.Value
	case yaml.SequenceNode:
		parts := make([]string, 0, len(h.When.Content))
		for _, item := range h.When.Content {
			parts = append(parts, item.Value)
		}
		return strings.Join(parts, "\n")
	}
	return ""
}

func TestAnsibleHandlersRegisterBeforeTheyAreRead(t *testing.T) {
	root := filepath.Join("..", "..", "deploy", "ansible", "roles")
	files, err := filepath.Glob(filepath.Join(root, "*", "handlers", "main.yml"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no role handler files under %s: %v", root, err)
	}
	for _, file := range files {
		body, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		var handlers []ansibleHandler
		if err := yaml.Unmarshal(body, &handlers); err != nil {
			t.Fatalf("parse %s: %v", file, err)
		}
		for i, reader := range handlers {
			when := reader.whenText()
			if when == "" {
				continue
			}
			for j, registrar := range handlers {
				if i == j || registrar.Register == "" {
					continue
				}
				if !regexp.MustCompile(`\b` + regexp.QuoteMeta(registrar.Register) + `\b`).MatchString(when) {
					continue
				}
				if j > i {
					t.Errorf("%s: handler %q reads %s, registered by %q defined after it", file, reader.Name, registrar.Register, registrar.Name)
					continue
				}
				// Notifying a handler's name or one of its listen topics
				// fires it; the registrar runs only for its own listen topics.
				var missing []string
				registrarTopics := registrar.listens()
				for trigger := range reader.triggers() {
					if !registrarTopics[trigger] {
						missing = append(missing, trigger)
					}
				}
				sort.Strings(missing)
				if len(missing) > 0 {
					t.Errorf("%s: handler %q reads %s, but %q does not listen on %q, so a notification there leaves it undefined",
						file, reader.Name, registrar.Register, registrar.Name, missing)
				}
			}
		}
	}
}
