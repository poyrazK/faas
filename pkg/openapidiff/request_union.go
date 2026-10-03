package openapidiff

import (
	"fmt"
	"strings"
)

// Only the common 3.1 value-or-null shape is folded. Generic branch unions,
// conjunctions and exclusive unions need a different acceptance-set model.
func (parser *requestParser) nullableUnion(schema *requestSchema, value map[string]any, location string, depth int, refs map[string]bool) *requestSchema {
	branches, valid := value["anyOf"].([]any)
	if !valid || !parser.work.use(len(branches)) {
		schema.opaque = true
		parser.unknown("invalid_nullable_union", location+"/anyOf")
		return schema
	}
	if len(branches) != 2 || !strings.HasPrefix(parser.spec.version, "3.1.") {
		schema.opaque = true
		parser.unknown("nullable_union_not_compared", location+"/anyOf")
		return schema
	}
	for _, key := range requestSortedKeys(value) {
		if key != "anyOf" && !requestAnnotation(key) {
			schema.opaque = true
			parser.unknown("nullable_union_siblings_not_compared", location)
			return schema
		}
	}
	left := parser.schema(branches[0], fmt.Sprintf("%s/anyOf/0", location), depth+1, refs)
	right := parser.schema(branches[1], fmt.Sprintf("%s/anyOf/1", location), depth+1, refs)
	isNull := func(branch *requestSchema) bool {
		types := requestActiveTypes(branch)
		return !branch.opaque && !branch.deny && len(types) == 1 && types["null"]
	}
	var result *requestSchema
	switch {
	case isNull(left) && !right.opaque:
		result = right
	case isNull(right) && !left.opaque:
		result = left
	default:
		schema.opaque = true
		parser.unknown("nullable_union_not_compared", location+"/anyOf")
		return schema
	}
	if result.deny {
		result = requestAnySchema()
		result.types = map[string]bool{}
	}
	result.types["null"] = true
	if result.enum != nil {
		result.enum["null"] = requestEnum{kind: "null"}
	}
	result.deny = false
	result.opaque = result.opaque || schema.opaque
	requestNormalizeSchema(result)
	return result
}
