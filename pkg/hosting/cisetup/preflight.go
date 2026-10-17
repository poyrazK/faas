package cisetup

import (
	"errors"
	"io"
	"reflect"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"gopkg.in/yaml.v3"
)

// WorkflowMatches accepts only the generated contract. This is not a general
// GitHub Actions security analyzer; executable changes require a new review.
func WorkflowMatches(actual, expected string) bool {
	a, ok := parseWorkflow(actual)
	if !ok {
		return false
	}
	b, ok := parseWorkflow(expected)
	return ok && reflect.DeepEqual(a, b)
}
func parseWorkflow(raw string) (any, bool) {
	if len(raw) == 0 || len(raw) > api.HostingCISetupWorkflowMaxBytes {
		return nil, false
	}
	decoder := yaml.NewDecoder(strings.NewReader(raw))
	var node yaml.Node
	if decoder.Decode(&node) != nil {
		return nil, false
	}
	var extra yaml.Node
	if !errors.Is(decoder.Decode(&extra), io.EOF) {
		return nil, false
	}
	count := 0
	if !validNode(&node, 0, &count) {
		return nil, false
	}
	var out any
	if node.Decode(&out) != nil {
		return nil, false
	}
	return out, true
}
func validNode(node *yaml.Node, depth int, count *int) bool {
	*count += 1
	if *count > api.HostingCISetupYAMLMaxNodes || depth > api.HostingCISetupYAMLMaxDepth || node.Kind == yaml.AliasNode || node.Anchor != "" {
		return false
	}
	switch node.Kind {
	case yaml.DocumentNode:
		if len(node.Content) != 1 {
			return false
		}
	case yaml.MappingNode:
		if node.Tag != "!!map" {
			return false
		}
		keys := map[string]bool{}
		for i := 0; i < len(node.Content); i += 2 {
			key := node.Content[i]
			if key.Kind != yaml.ScalarNode || key.Tag != "!!str" || keys[key.Value] {
				return false
			}
			keys[key.Value] = true
		}
	case yaml.SequenceNode:
		if node.Tag != "!!seq" {
			return false
		}
	case yaml.ScalarNode:
		if node.Tag != "!!str" && node.Tag != "!!int" && node.Tag != "!!bool" && node.Tag != "!!null" {
			return false
		}
	default:
		return false
	}
	for _, child := range node.Content {
		if !validNode(child, depth+1, count) {
			return false
		}
	}
	return true
}

// ValidPreparationAction checks only that the selected local file is a
// well-formed composite Action. It never runs or qualifies its contents.
func ValidPreparationAction(raw string) bool {
	value, ok := parseWorkflow(raw)
	if !ok {
		return false
	}
	root, ok := value.(map[string]any)
	if !ok {
		return false
	}
	runs, ok := root["runs"].(map[string]any)
	if !ok || runs["using"] != "composite" {
		return false
	}
	steps, ok := runs["steps"].([]any)
	return ok && len(steps) > 0
}
