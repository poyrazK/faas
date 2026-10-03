package mcphosting

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

var fieldToken = regexp.MustCompile("^[!#$%&'*+.^_`|~0-9A-Za-z-]+$")

// Validate every annotation, including annotations in unreachable schema
// branches. Only a chain of properties from the root may mirror arguments.
func parameterHeaders(schema map[string]any, args map[string]any) (http.Header, error) {
	if schema == nil {
		return nil, fmt.Errorf("inputSchema must be an object")
	}
	headers := make(http.Header)
	seen := make(map[string]bool)
	var walk func(any, any, bool) error
	walk = func(node, value any, reachable bool) error {
		switch n := node.(type) {
		case map[string]any:
			if raw, ok := n["x-mcp-header"]; ok {
				name, ok := raw.(string)
				if !reachable || !ok || !fieldToken.MatchString(name) || seen[strings.ToLower(name)] {
					return fmt.Errorf("invalid or duplicate x-mcp-header annotation")
				}
				seen[strings.ToLower(name)] = true
				kind, _ := n["type"].(string)
				if kind != "string" && kind != "integer" && kind != "boolean" {
					return fmt.Errorf("x-mcp-header requires string, integer or boolean")
				}
				if value != nil {
					literal, err := primitiveHeader(value, kind)
					if err != nil {
						return err
					}
					headers.Set("Mcp-Param-"+name, encodeHeader(literal))
				}
			}
			for key, child := range n {
				if key == "properties" {
					props, ok := child.(map[string]any)
					if !ok {
						return fmt.Errorf("schema properties must be an object")
					}
					values, _ := value.(map[string]any)
					for prop, def := range props {
						if err := walk(def, values[prop], reachable); err != nil {
							return err
						}
					}
				} else if err := walk(child, nil, false); err != nil {
					return err
				}
			}
		case []any:
			for _, child := range n {
				if err := walk(child, nil, false); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := walk(schema, args, true); err != nil {
		return nil, err
	}
	return headers, nil
}

func primitiveHeader(value any, kind string) (string, error) {
	switch kind {
	case "string":
		if s, ok := value.(string); ok {
			return s, nil
		}
	case "boolean":
		if b, ok := value.(bool); ok {
			return strconv.FormatBool(b), nil
		}
	case "integer":
		var number float64
		switch n := value.(type) {
		case float64:
			number = n
		case int:
			number = float64(n)
		case json.Number:
			v, err := n.Float64()
			if err != nil {
				return "", fmt.Errorf("invalid mirrored integer")
			}
			number = v
		default:
			return "", fmt.Errorf("mirrored argument must be an integer")
		}
		if math.IsNaN(number) || math.IsInf(number, 0) || math.Trunc(number) != number || math.Abs(number) > 9007199254740991 {
			return "", fmt.Errorf("mirrored integer is outside the JavaScript safe range")
		}
		return strconv.FormatFloat(number, 'f', 0, 64), nil
	}
	return "", fmt.Errorf("mirrored argument does not match its schema type")
}
