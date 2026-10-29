package apid

import (
	"bytes"
	"slices"
	"strings"
	"sync"

	"github.com/onebox-faas/faas/pkg/apid/monetization"
	"github.com/onebox-faas/faas/pkg/apid/realtimepreview"
	"gopkg.in/yaml.v3"
)

// publicHiddenPathPrefixes keeps operator-only previews out of the anonymous
// spec served at /v1/openapi.{yaml,json} and rendered at /docs. The durable
// entity preview (ADR-712) is not a customer product; its routes answer like
// unregistered ones unless an operator enables it. OpenAPIYAML stays complete
// for SDK generation, contract diffs and the route compliance gate.
// gatewayd-public's trace ring answers only the operator's observer token
// (X-Faas-Trace-Auth); customers read traces through /v1/account/traces.
var publicHiddenPathPrefixes = []string{"/v1/apps/{slug}/entities", "/v1/traces/{trace_id}"}

// Visibility names the unreleased features the served spec publishes.
type Visibility struct {
	// Monetization: API monetization is released (ADR-20261018090210).
	Monetization bool
	// RealtimePreview: every account sees the realtime preview
	// (ADR-20261019102657).
	RealtimePreview bool
}

func (v Visibility) index() int {
	i := 0
	if v.Monetization {
		i |= 1
	}
	if v.RealtimePreview {
		i |= 2
	}
	return i
}

// publicSpecs caches the served spec per Visibility.index.
var publicSpecs [4]struct {
	once sync.Once
	yaml []byte
}

// PublicOpenAPIYAML returns the embedded spec without hidden paths, unreleased
// features included, or the components only they reference.
func PublicOpenAPIYAML() []byte { return PublicOpenAPIYAMLFor(Visibility{}) }

// PublicOpenAPIYAMLFor returns the served spec, publishing API monetization's
// paths and webhook events and the realtime preview's routes and fields only
// as v allows. Each variant is computed on first use, like the JSON form.
func PublicOpenAPIYAMLFor(v Visibility) []byte {
	spec := &publicSpecs[v.index()]
	spec.once.Do(func() { spec.yaml = publicSpec(openapiYAML, v) })
	return spec.yaml
}

// publicSpec filters raw. A spec it cannot rewrite yields an error document,
// never the unfiltered spec, so hidden paths cannot leak on failure.
func publicSpec(raw []byte, v Visibility) []byte {
	return FilterSpec(raw, nil, v)
}

// FilterSpec returns raw without the operator previews, the paths hide
// reports (nil hides none), the unreleased features v leaves out, and the
// components only removed paths referenced. The served spec hides the
// capabilities off by default, and the SDK spec the preview and internal
// ones (ADR-20261023115741). A spec it cannot rewrite yields an error
// document, never the unfiltered spec.
func FilterSpec(raw []byte, hide func(path string) bool, v Visibility) []byte {
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
		key, item := paths.Content[i].Value, paths.Content[i+1]
		if hiddenPublicPath(key) || hide != nil && hide(key) || !v.Monetization && monetization.Path(key) {
			removed = append(removed, item)
			continue
		}
		if !v.RealtimePreview {
			if ops := hideRealtimePreview(key, item); len(ops) > 0 {
				removed = append(removed, ops...)
				if !hasOperation(item) {
					removed = append(removed, item)
					continue
				}
			}
		}
		kept = append(kept, paths.Content[i], item)
	}
	if !v.RealtimePreview {
		removed = append(removed, hideRealtimePreviewFields(root)...)
	}
	if len(removed) == 0 {
		return raw
	}
	paths.Content = kept
	if !v.Monetization {
		schemas := mappingValue(mappingValue(root, "components"), "schemas")
		for _, name := range monetization.Schemas {
			if body := mappingValue(schemas, name); body != nil {
				removed = append(removed, body)
				deleteMappingKey(schemas, name)
			}
		}
	}
	if !v.RealtimePreview {
		components := mappingValue(root, "components")
		for _, component := range realtimepreview.Components {
			kind, name, _ := strings.Cut(component, "/")
			group := mappingValue(components, kind)
			if body := mappingValue(group, name); body != nil {
				removed = append(removed, body)
				deleteMappingKey(group, name)
			}
		}
	}
	pruneComponents(root, removed)
	if !v.Monetization {
		dropListValues(root, monetization.WebhookEvent)
	}
	if !v.RealtimePreview {
		dropListValues(root, realtimepreview.WebhookEvent)
	}
	explicitNulls(root)
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if enc.Encode(&doc) != nil || enc.Close() != nil {
		return []byte(malformed)
	}
	return buf.Bytes()
}

var operationMethods = []string{"get", "put", "post", "delete", "options", "head", "patch", "trace"}

// hideRealtimePreview deletes the realtime preview's operations from a path
// item and the retained delivery value from the released publish route,
// returning the deleted operations.
func hideRealtimePreview(template string, item *yaml.Node) []*yaml.Node {
	var removed []*yaml.Node
	for _, method := range operationMethods {
		op := mappingValue(item, method)
		if op == nil {
			continue
		}
		if realtimepreview.Route(method, template) {
			removed = append(removed, op)
			deleteMappingKey(item, method)
			continue
		}
		params := mappingValue(op, "parameters")
		if params == nil || params.Kind != yaml.SequenceNode {
			continue
		}
		for _, param := range params.Content {
			if name := mappingValue(param, "name"); name != nil && name.Value == "delivery" && strings.ToUpper(method)+" "+template == realtimepreview.RetainedDeliveryRoute {
				dropListValues(param, func(value string) bool { return value == realtimepreview.RetainedDelivery })
			}
		}
	}
	return removed
}

// hideRealtimePreviewFields deletes the preview-only properties of released
// schemas, returning them.
func hideRealtimePreviewFields(root *yaml.Node) []*yaml.Node {
	schemas := mappingValue(mappingValue(root, "components"), "schemas")
	var removed []*yaml.Node
	for schema, fields := range realtimepreview.Fields {
		body := mappingValue(schemas, schema)
		properties := mappingValue(body, "properties")
		for _, field := range fields {
			if property := mappingValue(properties, field); property != nil {
				removed = append(removed, property)
				deleteMappingKey(properties, field)
			}
		}
		if required := mappingValue(body, "required"); required != nil && required.Kind == yaml.SequenceNode {
			required.Content = slices.DeleteFunc(required.Content, func(n *yaml.Node) bool { return slices.Contains(fields, n.Value) })
		}
	}
	return removed
}

func hasOperation(item *yaml.Node) bool {
	for _, method := range operationMethods {
		if mappingValue(item, method) != nil {
			return true
		}
	}
	return false
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
	candidates := componentClosure(components, removed)
	// A component the removed paths never reached stays, and so does
	// everything it references, even when no path references it.
	for i := 0; i+1 < len(components.Content); i += 2 {
		group := components.Content[i+1]
		for j := 0; j+1 < len(group.Content); j += 2 {
			if !candidates["#/components/"+components.Content[i].Value+"/"+group.Content[j].Value] {
				keptRoots = append(keptRoots, group.Content[j+1])
			}
		}
	}
	stillUsed := componentClosure(components, keptRoots)
	for ref := range candidates {
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

// dropListValues removes matching scalars from every enum and default list,
// so the remaining schemas stop naming hidden webhook events.
func dropListValues(node *yaml.Node, drop func(string) bool) {
	if node.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(node.Content); i += 2 {
			list := node.Content[i+1]
			if key := node.Content[i].Value; (key != "enum" && key != "default") || list.Kind != yaml.SequenceNode {
				continue
			}
			kept := list.Content[:0]
			for _, item := range list.Content {
				if item.Kind != yaml.ScalarNode || !drop(item.Value) {
					kept = append(kept, item)
				}
			}
			list.Content = kept
		}
	}
	for _, child := range node.Content {
		dropListValues(child, drop)
	}
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
