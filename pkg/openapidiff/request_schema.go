package openapidiff

import (
	"math/big"
	"strings"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/api"
)

type requestSchema struct {
	types                  map[string]bool
	enum                   map[string]requestEnum
	properties             map[string]*requestSchema
	required               map[string]bool
	items                  *requestSchema
	limits                 requestConstraints
	additional             bool
	opaque, deny, readOnly bool
}

type requestEnum struct {
	kind    string
	boolean bool
	number  *big.Rat
	runes   int
}

func requestAnySchema() *requestSchema {
	return &requestSchema{types: map[string]bool{"null": true, "boolean": true, "integer": true, "number": true, "string": true, "object": true, "array": true}, additional: true, properties: map[string]*requestSchema{}, required: map[string]bool{}}
}

func (parser *requestParser) schema(raw any, location string, depth int, refs map[string]bool) *requestSchema {
	schema := requestAnySchema()
	if dialect, present := parser.spec.Raw["jsonSchemaDialect"]; present {
		name, valid := dialect.(string)
		if !valid || name != "https://spec.openapis.org/oas/3.1/dialect/base" {
			schema.opaque = true
			parser.unknown("unsupported_schema_dialect", location)
		}
	}
	if !parser.work.use(1) || depth > api.RequestCompatibilityMaxDepth {
		parser.work.exceeded = true
		schema.opaque = true
		return schema
	}
	if flag, ok := raw.(bool); ok {
		if strings.HasPrefix(parser.spec.version, "3.1.") {
			schema.deny = !flag
		} else {
			schema.opaque = true
			parser.unknown("boolean_schema_not_supported_in_30", location)
		}
		return schema
	}
	value, ok := raw.(map[string]any)
	if !ok {
		schema.opaque = true
		parser.unknown("invalid_schema", location)
		return schema
	}
	if ref, present := value["$ref"]; present {
		name, _ := ref.(string)
		if refs[name] {
			schema.opaque = true
			parser.unknown("recursive_reference_not_compared", location)
			return schema
		}
		resolved, valid := parser.resolve(raw, "schemas", location, depth, refs)
		if !valid {
			schema.opaque = true
			return schema
		}
		refs[name] = true
		defer delete(refs, name)
		resolvedSchema := parser.schema(resolved, location, depth+1, refs)
		resolvedSchema.opaque = resolvedSchema.opaque || schema.opaque
		return resolvedSchema
	}
	if !parser.work.use(len(value)) {
		schema.opaque = true
		return schema
	}
	if _, union := value["anyOf"]; union {
		return parser.nullableUnion(schema, value, location, depth, refs)
	}
	for _, key := range requestSortedKeys(value) {
		if requestAnnotation(key) {
			continue
		}
		switch key {
		case "type", "enum", "properties", "required", "items", "additionalProperties", "readOnly", "nullable", "minimum", "maximum", "exclusiveMinimum", "exclusiveMaximum", "minLength", "maxLength", "minItems", "maxItems":
		default:
			schema.opaque = true
			parser.unknown("unsupported_schema_keyword", location)
		}
	}
	if rawType, present := value["type"]; present {
		schema.types = map[string]bool{}
		var types []any
		switch typed := rawType.(type) {
		case string:
			types = []any{typed}
		case []any:
			if strings.HasPrefix(parser.spec.version, "3.1.") {
				types = typed
			}
		}
		if len(types) == 0 || !parser.work.use(len(types)) {
			schema.opaque = true
			parser.unknown("invalid_schema_type", location+"/type")
		}
		for _, rawName := range types {
			name, ok := rawName.(string)
			if !ok || !requestAnySchema().types[name] || (name == "null" && strings.HasPrefix(parser.spec.version, "3.0.")) {
				schema.opaque = true
				parser.unknown("invalid_schema_type", location+"/type")
				continue
			}
			schema.types[name] = true
			if name == "number" {
				schema.types["integer"] = true
			}
		}
	}
	if rawNullable, present := value["nullable"]; present {
		nullable, ok := rawNullable.(bool)
		if !ok {
			schema.opaque = true
			parser.unknown("invalid_nullable", location+"/nullable")
		} else if nullable {
			if strings.HasPrefix(parser.spec.version, "3.0.") {
				if _, typed := value["type"]; typed {
					schema.types["null"] = true
				}
			} else {
				schema.opaque = true
				parser.unknown("legacy_nullable_not_compared_in_31", location+"/nullable")
			}
		}
	}
	if rawReadOnly, present := value["readOnly"]; present {
		readOnly, ok := rawReadOnly.(bool)
		if !ok || readOnly {
			schema.readOnly = readOnly
			schema.opaque = true
			parser.unknown("read_only_schema_not_compared", location+"/readOnly")
		}
	}
	if rawEnum, present := value["enum"]; present {
		values, ok := rawEnum.([]any)
		if len(values) > api.RequestCompatibilityMaxEnumValues {
			parser.work.exceeded = true
			return schema
		}
		if !ok {
			schema.opaque = true
			parser.unknown("unsupported_enum", location+"/enum")
		} else if parser.work.use(len(values)) {
			schema.enum = map[string]requestEnum{}
			for _, rawValue := range values {
				key, entry, valid := requestEnumValue(rawValue)
				if !valid {
					schema.opaque = true
					parser.unknown("unsupported_enum", location+"/enum")
					continue
				}
				parser.work.bytes += len(key)
				if parser.work.bytes > api.RequestCompatibilityMaxWorkBytes {
					parser.work.exceeded = true
					return schema
				}
				if schema.types[entry.kind] {
					schema.enum[key] = entry
				}
			}
			if len(schema.enum) == 0 {
				schema.deny = true
			}
		}
	}
	if rawAdditional, present := value["additionalProperties"]; present {
		additional, ok := rawAdditional.(bool)
		if !ok {
			schema.opaque = true
			parser.unknown("additional_property_schema_not_compared", location+"/additionalProperties")
		} else {
			schema.additional = additional
		}
	}
	if rawProps, present := value["properties"]; present {
		properties, ok := rawProps.(map[string]any)
		if !ok {
			schema.opaque = true
			parser.unknown("invalid_properties", location+"/properties")
		} else if parser.work.use(len(properties)) {
			for _, key := range requestSortedKeys(properties) {
				if !parser.work.metadata(key) {
					schema.opaque = true
					parser.unknown("invalid_property_metadata", location+"/properties")
					continue
				}
				schema.properties[key] = parser.schema(properties[key], location+"/properties/"+requestPointer(key), depth+1, refs)
			}
		}
	}
	if rawRequired, present := value["required"]; present {
		required, ok := rawRequired.([]any)
		if !ok {
			schema.opaque = true
			parser.unknown("invalid_required", location+"/required")
		} else if parser.work.use(len(required)) {
			for _, rawName := range required {
				name, ok := rawName.(string)
				if !ok || !parser.work.metadata(name) {
					schema.opaque = true
					parser.unknown("invalid_required", location+"/required")
					continue
				}
				schema.required[name] = true
			}
		}
	}
	if strings.HasPrefix(parser.spec.version, "3.0.") {
		for name, child := range schema.properties {
			if child.readOnly {
				delete(schema.required, name)
			}
		}
	}
	if rawItems, present := value["items"]; present {
		schema.items = parser.schema(rawItems, location+"/items", depth+1, refs)
	}
	parser.constraints(schema, value, location)
	requestNormalizeSchema(schema)
	return schema
}

func requestEnumValue(raw any) (string, requestEnum, bool) {
	switch value := raw.(type) {
	case nil:
		return "null", requestEnum{kind: "null"}, true
	case bool:
		if value {
			return "true", requestEnum{kind: "boolean", boolean: true}, true
		}
		return "false", requestEnum{kind: "boolean"}, true
	case string:
		if len(value) > api.RequestCompatibilityMaxMetadataBytes {
			return "", requestEnum{}, false
		}
		return "string:" + value, requestEnum{kind: "string", runes: utf8.RuneCountInString(value)}, true
	}
	number := requestNumber(raw)
	if number == nil {
		return "", requestEnum{}, false
	}
	kind := "number"
	if number.IsInt() {
		kind = "integer"
	}
	return "number:" + number.RatString(), requestEnum{kind: kind, number: number}, true
}

func requestActiveTypes(schema *requestSchema) map[string]bool {
	if schema.enum == nil {
		return schema.types
	}
	result := map[string]bool{}
	for _, value := range schema.enum {
		result[value.kind] = true
	}
	return result
}

func requestEnumAccepts(schema *requestSchema, key string, value requestEnum) bool {
	if schema.deny || !schema.types[value.kind] || !requestConstraintsAccept(schema, value) {
		return false
	}
	if schema.enum != nil {
		_, found := schema.enum[key]
		return found
	}
	return true
}

func (work *requestWork) compareSchema(row *RequestRoute, before, after *requestSchema, location string, depth int) {
	if before == nil || after == nil || before.opaque || after.opaque || !work.use(1) {
		return
	}
	if depth > api.RequestCompatibilityMaxDepth {
		work.exceeded = true
		return
	}
	if before.deny {
		return
	}
	if after.deny {
		work.breaking(row, "schema_rejects_all", location)
		return
	}
	oldTypes, newTypes := requestActiveTypes(before), requestActiveTypes(after)
	for _, kind := range requestSortedKeys(oldTypes) {
		if !newTypes[kind] {
			if kind == "null" {
				work.breaking(row, "null_no_longer_allowed", location)
			} else {
				work.breaking(row, "accepted_type_narrowed", location)
			}
		}
	}
	if after.enum != nil {
		narrowed := false
		if before.enum != nil {
			for key, value := range before.enum {
				if !requestEnumAccepts(after, key, value) {
					narrowed = true
					break
				}
			}
		} else {
			for _, kind := range requestSortedKeys(oldTypes) {
				if !work.enumCoversKind(before, after, kind) {
					narrowed = true
				}
			}
		}
		if narrowed {
			work.breaking(row, "enum_values_restricted", location+"/enum")
		}
	}
	work.compareConstraints(row, before, after, location)
	if oldTypes["object"] && newTypes["object"] {
		for _, key := range requestSortedKeys(after.required) {
			if child := after.properties[key]; child != nil && child.readOnly {
				continue
			}
			if !before.required[key] {
				work.breaking(row, "property_required", location+"/required/"+requestPointer(key))
			}
		}
		if before.additional && !after.additional {
			work.breaking(row, "additional_properties_restricted", location+"/additionalProperties")
		}
		keys := map[string]bool{}
		for key := range before.properties {
			keys[key] = true
		}
		for key := range after.properties {
			keys[key] = true
		}
		for _, key := range requestSortedKeys(keys) {
			baseline, proposed := before.properties[key], after.properties[key]
			childLocation := location + "/properties/" + requestPointer(key)
			if baseline == nil {
				if !before.additional {
					continue
				}
				baseline = requestAnySchema()
			}
			if proposed == nil {
				if !after.additional {
					work.breaking(row, "property_no_longer_allowed", childLocation)
					continue
				}
				proposed = requestAnySchema()
			}
			work.compareSchema(row, baseline, proposed, childLocation, depth+1)
		}
	}
	if oldTypes["array"] && newTypes["array"] && (before.items != nil || after.items != nil) && (before.limits.maxItems == nil || before.limits.maxItems.Sign() > 0) {
		oldItems, newItems := before.items, after.items
		if oldItems == nil {
			oldItems = requestAnySchema()
		}
		if newItems == nil {
			newItems = requestAnySchema()
		}
		work.compareSchema(row, oldItems, newItems, location+"/items", depth+1)
	}
}
