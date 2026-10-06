package openapidiff

import (
	"mime"
	"reflect"
	"strings"
)

type requestBody struct {
	required, opaque bool
	content          map[string]*requestSchema
}
type requestParameter struct {
	required, opaque, explode, allowReserved, allowEmpty bool
	style, location                                      string
	schema                                               *requestSchema
}

func (parser *requestParser) body(raw any) *requestBody {
	if raw == nil {
		return nil
	}
	body := &requestBody{content: map[string]*requestSchema{}}
	resolved, valid := parser.resolve(raw, "requestBodies", "/requestBody", 0, map[string]bool{})
	value, ok := resolved.(map[string]any)
	if !valid || !ok {
		body.opaque = true
		if valid {
			parser.unknown("invalid_request_body", "/requestBody")
		}
		return body
	}
	body.required, valid = parser.boolean(value, "required", false, "/requestBody/required")
	body.opaque = !valid
	content, ok := value["content"].(map[string]any)
	if !ok || len(content) == 0 {
		body.opaque = true
		parser.unknown("invalid_request_content", "/requestBody/content")
		return body
	}
	if !parser.work.use(len(content)) {
		return body
	}
	for _, key := range requestSortedKeys(content) {
		media, valid := requestMedia(key)
		if !valid || !parser.work.metadata(key) {
			body.opaque = true
			parser.unknown("unsupported_media_type", "/requestBody/content")
			continue
		}
		location := "/requestBody/content/" + requestPointer(media)
		if _, duplicate := body.content[media]; duplicate {
			body.opaque = true
			parser.unknown("ambiguous_media_type", location)
			continue
		}
		object, ok := content[key].(map[string]any)
		if !ok {
			body.opaque = true
			parser.unknown("invalid_media_type_object", location)
			continue
		}
		var schema *requestSchema
		if rawSchema, present := object["schema"]; present {
			schema = parser.schema(rawSchema, location+"/schema", 0, map[string]bool{})
		} else {
			schema = requestAnySchema()
		}
		if _, encoding := object["encoding"]; encoding {
			schema.opaque = true
			parser.unknown("body_encoding_not_compared", location+"/encoding")
		}
		body.content[media] = schema
	}
	return body
}

func (parser *requestParser) boolean(object map[string]any, key string, fallback bool, location string) (bool, bool) {
	value, present := object[key]
	if !present {
		return fallback, true
	}
	flag, ok := value.(bool)
	if !ok {
		parser.unknown("invalid_boolean", location)
	}
	return flag, ok
}

func requestMedia(raw string) (string, bool) {
	media, parameters, err := mime.ParseMediaType(raw)
	if err != nil || len(parameters) != 0 || strings.TrimSpace(raw) != raw {
		return "", false
	}
	major, minor, found := strings.Cut(media, "/")
	if !found || major == "" || minor == "" || strings.Contains(minor, "/") || (strings.Contains(major, "*") && (major != "*" || minor != "*")) || (strings.Contains(minor, "*") && minor != "*") {
		return "", false
	}
	return strings.ToLower(media), true
}

func requestMediaSchema(content map[string]*requestSchema, media string) *requestSchema {
	if schema := content[media]; schema != nil {
		return schema
	}
	major, _, _ := strings.Cut(media, "/")
	if schema := content[major+"/*"]; schema != nil {
		return schema
	}
	return content["*/*"]
}

func (work *requestWork) compareBody(row *RequestRoute, before, after *requestBody) {
	if after == nil {
		if before != nil {
			work.unknown(row, "request_body_removed", "/requestBody", "")
		}
		return
	}
	if after.opaque || (before != nil && before.opaque) {
		return
	}
	if after.required && (before == nil || !before.required) {
		work.breaking(row, "request_body_required", "/requestBody/required")
	}
	if before == nil {
		return
	}
	keys := map[string]bool{}
	for key := range before.content {
		keys[key] = true
	}
	for key := range after.content {
		keys[key] = true
	}
	for _, media := range requestSortedKeys(keys) {
		baseline := requestMediaSchema(before.content, media)
		if baseline == nil {
			continue
		}
		location := "/requestBody/content/" + requestPointer(media)
		proposed := requestMediaSchema(after.content, media)
		if proposed == nil {
			work.breaking(row, "content_type_removed", location)
			continue
		}
		work.compareSchema(row, baseline, proposed, location+"/schema", 0)
	}
}

// Merge path-level and operation parameters by their declared name/location
// before parsing schemas. A valid operation override replaces shared metadata.
func (parser *requestParser) parameters(shared, operation map[string]any) (map[string]*requestParameter, bool) {
	identitiesKnown := true
	merged := map[string]map[string]any{}
	ambiguous := map[string]bool{}
	for _, object := range []map[string]any{shared, operation} {
		raw, present := object["parameters"]
		if !present {
			continue
		}
		list, ok := raw.([]any)
		if !ok {
			identitiesKnown = false
			parser.unknown("invalid_parameters", "/parameters")
			continue
		}
		if !parser.work.use(len(list)) {
			return nil, false
		}
		seen := map[string]bool{}
		for _, item := range list {
			resolved, valid := parser.resolve(item, "parameters", "/parameters", 0, map[string]bool{})
			value, ok := resolved.(map[string]any)
			if !valid || !ok {
				identitiesKnown = false
				if valid {
					parser.unknown("invalid_parameter", "/parameters")
				}
				continue
			}
			name, nameOK := value["name"].(string)
			in, inOK := value["in"].(string)
			if !nameOK || !inOK || name == "" || !parser.work.metadata(name) || (in != "query" && in != "header" && in != "path" && in != "cookie") {
				identitiesKnown = false
				parser.unknown("invalid_parameter_identity", "/parameters")
				continue
			}
			if in == "header" && (strings.EqualFold(name, "Accept") || strings.EqualFold(name, "Content-Type") || strings.EqualFold(name, "Authorization")) {
				continue
			}
			key := in + " " + name
			if seen[key] {
				ambiguous[key] = true
				parser.unknown("duplicate_parameter", "/parameters/"+in+"/"+requestPointer(name))
			}
			seen[key] = true
			merged[key] = value
		}
	}
	result := map[string]*requestParameter{}
	for _, key := range requestSortedKeys(merged) {
		value := merged[key]
		in := value["in"].(string)
		name := value["name"].(string)
		canonical := key
		if in == "header" {
			canonical = in + " " + strings.ToLower(name)
		}
		location := "/parameters/" + in + "/" + requestPointer(name)
		parameter := parser.parameter(value, in, name, location)
		parameter.opaque = parameter.opaque || ambiguous[key]
		if existing, duplicate := result[canonical]; duplicate {
			existing.opaque, parameter.opaque = true, true
			parser.unknown("ambiguous_header_parameter", location)
		}
		result[canonical] = parameter
	}
	return result, identitiesKnown
}

func (parser *requestParser) parameter(value map[string]any, in, name, location string) *requestParameter {
	parameter := &requestParameter{location: location}
	var valid bool
	parameter.required, valid = parser.boolean(value, "required", false, location+"/required")
	parameter.opaque = !valid
	if in == "path" && (!parameter.required || !strings.Contains(parser.row.Path, "{"+name+"}")) {
		parameter.opaque = true
		parser.unknown("invalid_path_parameter", location)
	}
	parameter.style = "simple"
	if in == "query" || in == "cookie" {
		parameter.style = "form"
	}
	if style, present := value["style"]; present {
		text, ok := style.(string)
		if !ok || !requestStyle(in, text) {
			parameter.opaque = true
			parser.unknown("unsupported_parameter_style", location+"/style")
		} else {
			parameter.style = text
		}
	}
	parameter.explode, valid = parser.boolean(value, "explode", parameter.style == "form", location+"/explode")
	parameter.opaque = parameter.opaque || !valid
	if in == "query" {
		parameter.allowReserved, valid = parser.boolean(value, "allowReserved", false, location+"/allowReserved")
		parameter.opaque = parameter.opaque || !valid
		parameter.allowEmpty, valid = parser.boolean(value, "allowEmptyValue", false, location+"/allowEmptyValue")
		parameter.opaque = parameter.opaque || !valid
	}
	if _, content := value["content"]; content {
		parameter.opaque = true
		parser.unknown("parameter_content_not_compared", location+"/content")
	}
	if rawSchema, present := value["schema"]; present {
		parameter.schema = parser.schema(rawSchema, location+"/schema", 0, map[string]bool{})
	} else if !parameter.opaque {
		parameter.opaque = true
		parser.unknown("parameter_schema_missing", location+"/schema")
	}
	return parameter
}

func requestStyle(in, style string) bool {
	switch in {
	case "path":
		return style == "simple" || style == "label" || style == "matrix"
	case "header":
		return style == "simple"
	case "cookie":
		return style == "form"
	case "query":
		return style == "form" || style == "spaceDelimited" || style == "pipeDelimited" || style == "deepObject"
	}
	return false
}

// Keep map comparisons insensitive to declaration order and casing noise in
// HTTP header names, while preserving their report locations.
func requestParametersEqual(before, after map[string]*requestParameter) bool {
	if len(before) != len(after) {
		return false
	}
	for key, left := range before {
		right := after[key]
		if right == nil {
			return false
		}
		a, b := *left, *right
		a.location, b.location = "", ""
		if !reflect.DeepEqual(a, b) {
			return false
		}
	}
	return true
}
