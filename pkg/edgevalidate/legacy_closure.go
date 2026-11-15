package edgevalidate

import jsonschema "github.com/santhosh-tekuri/jsonschema/v6"

// Keep the original dialect's validation (including ignored $ref siblings and
// legacy tuple items), and apply the additional strict policy in an outer
// applicator. The validator propagates evaluated properties through legacy
// schemas when their caller needs that annotation. Closing each allOf branch
// with additionalProperties:false would reject its siblings' legitimate fields.
func closeLegacyCompiledSchema(root *jsonschema.Schema) *jsonschema.Schema {
	type edge struct {
		schema *jsonschema.Schema
		close  bool
	}
	memo := make(map[edge]*jsonschema.Schema)
	visited := make(map[*jsonschema.Schema]bool)
	var visit func(*jsonschema.Schema, bool) *jsonschema.Schema
	visit = func(s *jsonschema.Schema, closeSelf bool) *jsonschema.Schema {
		if s == nil {
			return nil
		}
		key := edge{s, closeSelf}
		if result, ok := memo[key]; ok {
			return result
		}
		result := s
		declares := len(s.Properties) > 0 || len(s.PatternProperties) > 0 || s.Ref != nil || len(s.AllOf) > 0 || len(s.AnyOf) > 0 || len(s.OneOf) > 0 || s.If != nil || len(s.Dependencies) > 0
		if closeSelf && s.DraftVersion < 2019 && declares && s.AdditionalProperties == nil && s.UnevaluatedProperties == nil {
			deny := false
			result = &jsonschema.Schema{DraftVersion: 2020, Location: s.Location,
				AllOf: []*jsonschema.Schema{s}, UnevaluatedProperties: &jsonschema.Schema{DraftVersion: 2020, Location: s.Location + "/unevaluatedProperties", Bool: &deny}}
		}
		memo[key] = result
		if visited[s] {
			return result
		}
		visited[s] = true
		for k, child := range s.Properties {
			s.Properties[k] = visit(child, true)
		}
		for k, child := range s.PatternProperties {
			s.PatternProperties[k] = visit(child, true)
		}
		s.Ref = visit(s.Ref, false)
		for i, child := range s.AllOf {
			s.AllOf[i] = visit(child, false)
		}
		for i, child := range s.AnyOf {
			s.AnyOf[i] = visit(child, false)
		}
		for i, child := range s.OneOf {
			s.OneOf[i] = visit(child, false)
		}
		s.Not = visit(s.Not, false)
		s.If = visit(s.If, false)
		s.Then = visit(s.Then, false)
		s.Else = visit(s.Else, false)
		for k, child := range s.Dependencies {
			if schema, ok := child.(*jsonschema.Schema); ok {
				s.Dependencies[k] = visit(schema, false)
			}
		}
		for k, child := range s.DependentSchemas {
			s.DependentSchemas[k] = visit(child, false)
		}
		switch items := s.Items.(type) {
		case *jsonschema.Schema:
			s.Items = visit(items, true)
		case []*jsonschema.Schema:
			for i, child := range items {
				items[i] = visit(child, true)
			}
		}
		s.Contains = visit(s.Contains, true)
		if child, ok := s.AdditionalProperties.(*jsonschema.Schema); ok {
			s.AdditionalProperties = visit(child, true)
		}
		if child, ok := s.AdditionalItems.(*jsonschema.Schema); ok {
			s.AdditionalItems = visit(child, true)
		}
		for i, child := range s.PrefixItems {
			s.PrefixItems[i] = visit(child, true)
		}
		s.Items2020 = visit(s.Items2020, true)
		s.UnevaluatedItems = visit(s.UnevaluatedItems, true)
		return result
	}
	return visit(root, true)
}
