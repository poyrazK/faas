// Package clientgen renders standard-library Python and Go Data API clients
// from the fingerprinted contract snapshot (ADR 20261018170742).
package clientgen

import (
	"encoding/json"
	"regexp"
	"strings"
)

// Kind classifies one contract type expression.
type Kind int

const (
	KindUnknown Kind = iota
	KindString
	KindNumber
	KindBoolean
	KindJSON
	KindVoid
	KindNever
	KindEnum
	KindArray
	KindRow
	KindComposite
)

// Type is a parsed contract type. The snapshot stores TypeScript-shaped type
// strings produced by the runtime catalog normalizer; this grammar is closed.
type Type struct {
	Kind     Kind
	Nullable bool
	Literals []string // KindEnum
	Elem     *Type    // KindArray
	Schema   string   // KindRow, KindComposite
	Name     string   // KindRow, KindComposite
	View     bool     // KindRow
}

var (
	referencePattern = regexp.MustCompile(`^Database\["([^"\\]+)"\]\["(Tables|Views|CompositeTypes)"\]\["([^"\\]+)"\](\['Row'\])?$`)
	literalPattern   = regexp.MustCompile(`"(?:[^"\\]|\\.)*"`)
)

// ParseType parses a contract type string. Unrecognized shapes become
// KindUnknown, which renders as an untyped JSON value rather than failing.
func ParseType(expr string) Type {
	expr = strings.TrimSpace(expr)
	if inner, ok := strings.CutSuffix(expr, " | null"); ok {
		t := ParseType(inner)
		t.Nullable = true
		return t
	}
	if inner, ok := strings.CutSuffix(expr, "[]"); ok {
		if strings.HasPrefix(inner, "(") && strings.HasSuffix(inner, ")") {
			inner = inner[1 : len(inner)-1]
		}
		elem := ParseType(inner)
		return Type{Kind: KindArray, Elem: &elem}
	}
	if m := referencePattern.FindStringSubmatch(expr); m != nil {
		switch {
		case m[2] == "CompositeTypes" && m[4] == "":
			return Type{Kind: KindComposite, Schema: m[1], Name: m[3]}
		case m[2] != "CompositeTypes" && m[4] != "":
			return Type{Kind: KindRow, Schema: m[1], Name: m[3], View: m[2] == "Views"}
		}
		return Type{Kind: KindUnknown}
	}
	if strings.HasPrefix(expr, `"`) {
		return parseLiterals(expr)
	}
	switch expr {
	case "string":
		return Type{Kind: KindString}
	case "number":
		return Type{Kind: KindNumber}
	case "boolean":
		return Type{Kind: KindBoolean}
	case "Json":
		return Type{Kind: KindJSON}
	case "undefined":
		return Type{Kind: KindVoid}
	case "never":
		return Type{Kind: KindNever}
	}
	return Type{Kind: KindUnknown}
}

// Enum labels are JSON strings joined by " | "; labels may contain that text.
func parseLiterals(expr string) Type {
	matches := literalPattern.FindAllString(expr, -1)
	if strings.Join(matches, " | ") != expr {
		return Type{Kind: KindUnknown}
	}
	literals := make([]string, 0, len(matches))
	for _, match := range matches {
		var literal string
		if json.Unmarshal([]byte(match), &literal) != nil {
			return Type{Kind: KindUnknown}
		}
		literals = append(literals, literal)
	}
	return Type{Kind: KindEnum, Literals: literals}
}
