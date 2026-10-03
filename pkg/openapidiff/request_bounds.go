package openapidiff

import (
	"math"
	"math/big"
	"strconv"
	"strings"
)

// Bounds and scalar values stay private. Findings contain only controlled
// codes and normalized schema locations, never customer limits or enum values.
type requestNumberBound struct {
	value     *big.Rat
	exclusive bool
}

type requestConstraints struct {
	minimum, maximum                         *requestNumberBound
	minLength, maxLength, minItems, maxItems *big.Int
}

func requestNumber(raw any) *big.Rat {
	switch value := raw.(type) {
	case int:
		return new(big.Rat).SetInt64(int64(value))
	case int64:
		return new(big.Rat).SetInt64(value)
	case uint64:
		return new(big.Rat).SetInt(new(big.Int).SetUint64(value))
	case float64:
		if !math.IsNaN(value) && !math.IsInf(value, 0) {
			// Use the decoded number's canonical decimal representation rather
			// than introducing binary floating-point fractions into comparisons.
			number, ok := new(big.Rat).SetString(strconv.FormatFloat(value, 'g', -1, 64))
			if ok {
				return number
			}
		}
	}
	return nil
}

func (parser *requestParser) constraints(schema *requestSchema, value map[string]any, location string) {
	minimum, minValid := parser.numberBound(value, "minimum", "exclusiveMinimum", location, true)
	maximum, maxValid := parser.numberBound(value, "maximum", "exclusiveMaximum", location, false)
	schema.limits.minimum, schema.limits.maximum = minimum, maximum
	schema.opaque = schema.opaque || !minValid || !maxValid
	for _, field := range []struct {
		name string
		out  **big.Int
	}{
		{"minLength", &schema.limits.minLength}, {"maxLength", &schema.limits.maxLength},
		{"minItems", &schema.limits.minItems}, {"maxItems", &schema.limits.maxItems},
	} {
		raw, present := value[field.name]
		if !present {
			continue
		}
		number := requestNumber(raw)
		if number == nil || !number.IsInt() || number.Sign() < 0 {
			schema.opaque = true
			parser.unknown("invalid_size_bound", location+"/"+field.name)
			continue
		}
		*field.out = new(big.Int).Set(number.Num())
	}
}

func (parser *requestParser) numberBound(value map[string]any, inclusive, exclusive, location string, lower bool) (*requestNumberBound, bool) {
	valid := true
	var result *requestNumberBound
	if raw, present := value[inclusive]; present {
		if number := requestNumber(raw); number != nil {
			result = &requestNumberBound{value: number}
		} else {
			valid = false
			parser.unknown("invalid_numeric_bound", location+"/"+inclusive)
		}
	}
	if raw, present := value[exclusive]; present {
		if strings.HasPrefix(parser.spec.version, "3.0.") {
			flag, boolean := raw.(bool)
			if !boolean || (flag && result == nil) {
				valid = false
				parser.unknown("invalid_numeric_bound", location+"/"+exclusive)
			} else if flag {
				result.exclusive = true
			}
		} else if number := requestNumber(raw); number != nil {
			proposed := &requestNumberBound{value: number, exclusive: true}
			if requestBoundTighter(proposed, result, lower) {
				result = proposed
			}
		} else {
			valid = false
			parser.unknown("invalid_numeric_bound", location+"/"+exclusive)
		}
	}
	return result, valid
}

func requestBoundTighter(proposed, baseline *requestNumberBound, lower bool) bool {
	if proposed == nil {
		return false
	}
	if baseline == nil {
		return true
	}
	order := proposed.value.Cmp(baseline.value)
	if order == 0 {
		return proposed.exclusive && !baseline.exclusive
	}
	if lower {
		return order > 0
	}
	return order < 0
}

func requestIntegerBound(bound *requestNumberBound, lower bool) *requestNumberBound {
	if bound == nil {
		return nil
	}
	quotient, remainder := new(big.Int).QuoRem(bound.value.Num(), bound.value.Denom(), new(big.Int))
	one := big.NewInt(1)
	if lower {
		if remainder.Sign() != 0 && bound.value.Sign() > 0 || remainder.Sign() == 0 && bound.exclusive {
			quotient.Add(quotient, one)
		}
	} else if remainder.Sign() != 0 && bound.value.Sign() < 0 || remainder.Sign() == 0 && bound.exclusive {
		quotient.Sub(quotient, one)
	}
	return &requestNumberBound{value: new(big.Rat).SetInt(quotient)}
}

func requestNumericEmpty(minimum, maximum *requestNumberBound) bool {
	if minimum == nil || maximum == nil {
		return false
	}
	order := minimum.value.Cmp(maximum.value)
	return order > 0 || order == 0 && (minimum.exclusive || maximum.exclusive)
}

func requestSizeMinimum(value *big.Int) *big.Int {
	if value == nil {
		return new(big.Int)
	}
	return value
}

func requestSizeEmpty(minimum, maximum *big.Int) bool {
	return maximum != nil && requestSizeMinimum(minimum).Cmp(maximum) > 0
}

func requestNormalizeSchema(schema *requestSchema) {
	limits := &schema.limits
	if requestNumericEmpty(limits.minimum, limits.maximum) {
		delete(schema.types, "number")
		delete(schema.types, "integer")
	} else {
		if requestNumericEmpty(requestIntegerBound(limits.minimum, true), requestIntegerBound(limits.maximum, false)) {
			delete(schema.types, "integer")
		}
		// A closed singleton integral interval contains no fractional numbers.
		if limits.minimum != nil && limits.maximum != nil && limits.minimum.value.Cmp(limits.maximum.value) == 0 && limits.minimum.value.IsInt() {
			delete(schema.types, "number")
		}
	}
	if !schema.types["number"] && schema.types["integer"] {
		limits.minimum = requestIntegerBound(limits.minimum, true)
		limits.maximum = requestIntegerBound(limits.maximum, false)
	}
	if !schema.types["number"] && !schema.types["integer"] {
		limits.minimum, limits.maximum = nil, nil
	}
	if requestSizeEmpty(limits.minLength, limits.maxLength) {
		delete(schema.types, "string")
	}
	if schema.items != nil && schema.items.deny && !schema.items.opaque && schema.types["array"] {
		limits.maxItems = new(big.Int) // Only the empty array is accepted.
	}
	if requestSizeEmpty(limits.minItems, limits.maxItems) {
		delete(schema.types, "array")
	}
	for name := range schema.required {
		child := schema.properties[name]
		if child == nil && !schema.additional || child != nil && child.deny && !child.opaque {
			delete(schema.types, "object")
		}
	}
	if !schema.types["string"] {
		limits.minLength, limits.maxLength = nil, nil
	}
	if !schema.types["array"] {
		limits.minItems, limits.maxItems = nil, nil
	}
	if limits.minLength != nil && limits.minLength.Sign() == 0 {
		limits.minLength = nil
	}
	if limits.minItems != nil && limits.minItems.Sign() == 0 {
		limits.minItems = nil
	}
	for key, entry := range schema.enum {
		if !schema.types[entry.kind] || !requestConstraintsAccept(schema, entry) {
			delete(schema.enum, key)
		}
	}
	schema.deny = schema.deny || len(schema.types) == 0 || schema.enum != nil && len(schema.enum) == 0
}

func requestConstraintsAccept(schema *requestSchema, value requestEnum) bool {
	switch value.kind {
	case "number", "integer":
		return requestNumericAccepts(schema.limits.minimum, value.number, true) && requestNumericAccepts(schema.limits.maximum, value.number, false)
	case "string":
		length := big.NewInt(int64(value.runes))
		return length.Cmp(requestSizeMinimum(schema.limits.minLength)) >= 0 && (schema.limits.maxLength == nil || length.Cmp(schema.limits.maxLength) <= 0)
	}
	return true
}

func requestNumericAccepts(bound *requestNumberBound, value *big.Rat, lower bool) bool {
	if bound == nil {
		return true
	}
	if value == nil {
		return false
	}
	order := value.Cmp(bound.value)
	if order == 0 {
		return !bound.exclusive
	}
	if lower {
		return order > 0
	}
	return order < 0
}

func (work *requestWork) compareConstraints(row *RequestRoute, before, after *requestSchema, location string) {
	if before.enum != nil {
		for _, value := range before.enum {
			if !work.use(1) {
				return
			}
			if !after.types[value.kind] {
				continue
			}
			switch value.kind {
			case "integer", "number":
				if !requestNumericAccepts(after.limits.minimum, value.number, true) {
					work.breaking(row, "numeric_minimum_restricted", location)
				}
				if !requestNumericAccepts(after.limits.maximum, value.number, false) {
					work.breaking(row, "numeric_maximum_restricted", location)
				}
			case "string":
				length := big.NewInt(int64(value.runes))
				if length.Cmp(requestSizeMinimum(after.limits.minLength)) < 0 {
					work.breaking(row, "string_min_length_restricted", location+"/minLength")
				}
				if after.limits.maxLength != nil && length.Cmp(after.limits.maxLength) > 0 {
					work.breaking(row, "string_max_length_restricted", location+"/maxLength")
				}
			}
		}
		return
	}
	oldTypes, newTypes := requestActiveTypes(before), requestActiveTypes(after)
	for _, kind := range []string{"integer", "number"} {
		if !oldTypes[kind] || !newTypes[kind] {
			continue
		}
		oldMin, oldMax, newMin, newMax := before.limits.minimum, before.limits.maximum, after.limits.minimum, after.limits.maximum
		if kind == "integer" {
			oldMin, newMin = requestIntegerBound(oldMin, true), requestIntegerBound(newMin, true)
			oldMax, newMax = requestIntegerBound(oldMax, false), requestIntegerBound(newMax, false)
		}
		if requestBoundTighter(newMin, oldMin, true) {
			work.breaking(row, "numeric_minimum_restricted", location)
		}
		if requestBoundTighter(newMax, oldMax, false) {
			work.breaking(row, "numeric_maximum_restricted", location)
		}
	}
	if oldTypes["string"] && newTypes["string"] {
		work.compareSizes(row, before.limits.minLength, before.limits.maxLength, after.limits.minLength, after.limits.maxLength, location, "string", "Length")
	}
	if oldTypes["array"] && newTypes["array"] {
		work.compareSizes(row, before.limits.minItems, before.limits.maxItems, after.limits.minItems, after.limits.maxItems, location, "array", "Items")
	}
}

func (work *requestWork) compareSizes(row *RequestRoute, oldMin, oldMax, newMin, newMax *big.Int, location, kind, keyword string) {
	if requestSizeMinimum(newMin).Cmp(requestSizeMinimum(oldMin)) > 0 {
		code := "string_min_length_restricted"
		if kind == "array" {
			code = "array_min_items_restricted"
		}
		work.breaking(row, code, location+"/min"+keyword)
	}
	if newMax != nil && (oldMax == nil || newMax.Cmp(oldMax) < 0) {
		code := "string_max_length_restricted"
		if kind == "array" {
			code = "array_max_items_restricted"
		}
		work.breaking(row, code, location+"/max"+keyword)
	}
}

// Some bounded domains are finite even without enum: an integer interval,
// a numeric singleton, or the empty string. Count candidate coverage rather
// than expanding an arbitrarily large integer range.
func (work *requestWork) enumCoversKind(before, after *requestSchema, kind string) bool {
	switch kind {
	case "null":
		return requestEnumAccepts(after, "null", requestEnum{kind: "null"})
	case "boolean":
		return requestEnumAccepts(after, "true", requestEnum{kind: "boolean", boolean: true}) && requestEnumAccepts(after, "false", requestEnum{kind: "boolean"})
	case "string":
		return before.limits.maxLength != nil && before.limits.maxLength.Sign() == 0 && requestEnumAccepts(after, "string:", requestEnum{kind: "string"})
	case "number":
		minimum, maximum := before.limits.minimum, before.limits.maximum
		if minimum == nil || maximum == nil || minimum.value.Cmp(maximum.value) != 0 || minimum.exclusive || maximum.exclusive {
			return false
		}
		return requestEnumAccepts(after, "number:"+minimum.value.RatString(), requestEnum{kind: "number", number: minimum.value})
	case "integer":
		minimum := requestIntegerBound(before.limits.minimum, true)
		maximum := requestIntegerBound(before.limits.maximum, false)
		if minimum == nil || maximum == nil {
			return false
		}
		count := new(big.Int).Sub(maximum.value.Num(), minimum.value.Num())
		count.Add(count, big.NewInt(1))
		if count.Cmp(big.NewInt(int64(len(after.enum)))) > 0 {
			return false
		}
		covered := int64(0)
		for key, value := range after.enum {
			if !work.use(1) {
				return false
			}
			if value.kind == "integer" && requestEnumAccepts(before, key, value) {
				covered++
			}
		}
		return count.Cmp(big.NewInt(covered)) == 0
	}
	return false
}
