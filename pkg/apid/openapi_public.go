package apid

import (
	"bytes"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

// publicHiddenPathPrefixes keeps operator-only previews out of the anonymous
// spec served at /v1/openapi.{yaml,json} and rendered at /docs. The durable
// entity preview (ADR-712) is not a customer product; its routes answer like
// unregistered ones unless an operator enables it. OpenAPIYAML stays complete
// for SDK generation, contract diffs and the route compliance gate.
var publicHiddenPathPrefixes = []string{"/v1/apps/{slug}/entities"}

var (
	publicYAMLOnce sync.Once
	publicYAML     []byte
)

// PublicOpenAPIYAML returns the embedded spec without hidden paths or the
// components only they reference. Computed on first use, like the JSON form.
func PublicOpenAPIYAML() []byte {
	publicYAMLOnce.Do(func() { publicYAML = publicSpec(openapiYAML) })
	return publicYAML
}

// publicSpec filters raw. A spec it cannot rewrite yields an error document,
// never the unfiltered spec, so hidden paths cannot leak on failure.
func publicSpec(raw []byte) []byte {
	const malformed = "error: openapi spec is malformed at build time\n"
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil || len(doc.Content) == 0 {
		return []byte(malformed)
	}
	root := doc.Content[0]
	paths := mappingValue(root, "paths")
	if paths == nil {
		return []byte(malformed)
	}
	var removed []*yaml.Node
	kept := make([]*yaml.Node, 0, len(paths.Content))
	for i := 0; i+1 < len(paths.Content); i += 2 {
		if hiddenPublicPath(paths.Content[i].Value) {
			removed = append(removed, paths.Content[i+1])
			continue
		}
		kept = append(kept, paths.Content[i], paths.Content[i+1])
	}
	if len(removed) == 0 {
		return raw
	}
	paths.Content = kept
	pruneComponents(root, removed)
	explicitNulls(root)
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if enc.Encode(&doc) != nil || enc.Close() != nil {
		return []byte(malformed)
	}
	return buf.Bytes()
}

func hiddenPublicPath(path string) bool {
	for _, prefix := range publicHiddenPathPrefixes {
		if path == prefix || strings.HasPrefix(path, prefix+"/") {
			return true
		}
	}
	return false
}

// pruneComponents deletes components reachable from the removed path items
// that no remaining part of the document still reaches.
func pruneComponents(root *yaml.Node, removed []*yaml.Node) {
	components := mappingValue(root, "components")
	if components == nil {
		return
	}
	var keptRoots []*yaml.Node
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value != "components" {
			keptRoots = append(keptRoots, root.Content[i+1])
		}
	}
	stillUsed := componentClosure(components, keptRoots)
	for ref := range componentClosure(components, removed) {
		if stillUsed[ref] {
			continue
		}
		kind, name, _ := strings.Cut(strings.TrimPrefix(ref, "#/components/"), "/")
		if group := mappingValue(components, kind); group != nil {
			deleteMappingKey(group, name)
		}
	}
}

// componentClosure returns every "#/components/<kind>/<name>" reference
// reachable from roots, following references through component bodies.
func componentClosure(components *yaml.Node, roots []*yaml.Node) map[string]bool {
	seen := map[string]bool{}
	queue := append([]*yaml.Node(nil), roots...)
	for len(queue) > 0 {
		node := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		if node.Kind == yaml.ScalarNode {
			ref, ok := componentRef(node.Value)
			if !ok || seen[ref] {
				continue
			}
			seen[ref] = true
			kind, name, _ := strings.Cut(strings.TrimPrefix(ref, "#/components/"), "/")
			if body := mappingValue(mappingValue(components, kind), name); body != nil {
				queue = append(queue, body)
			}
			continue
		}
		queue = append(queue, node.Content...)
	}
	return seen
}

// componentRef normalizes a JSON pointer into components to its
// "#/components/<kind>/<name>" prefix.
func componentRef(value string) (string, bool) {
	rest, ok := strings.CutPrefix(value, "#/components/")
	if !ok {
		return "", false
	}
	parts := strings.SplitN(rest, "/", 3)
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return "", false
	}
	return "#/components/" + parts[0] + "/" + parts[1], true
}

// explicitNulls spells implicit nulls (an empty value) as `null`: the encoder
// would otherwise write them as "", changing the decoded document.
func explicitNulls(node *yaml.Node) {
	if node.Kind == yaml.ScalarNode && node.Tag == "!!null" && node.Value == "" {
		node.Value = "null"
	}
	for _, child := range node.Content {
		explicitNulls(child)
	}
}

func mappingValue(node *yaml.Node, key string) *yaml.Node {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i+1]
		}
	}
	return nil
}

func deleteMappingKey(node *yaml.Node, key string) {
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			node.Content = append(node.Content[:i], node.Content[i+2:]...)
			return
		}
	}
}
