package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"
)

func supportedMCPVersions(legacy bool) []string {
	versions := []string{currentProtocol}
	if legacy {
		versions = append(versions, legacyProtocol)
	}
	return versions
}

func validateMCPHeaders(headers http.Header, request rpcRequest, version string) error {
	mismatch := errors.New("MCP header mismatch")
	var params map[string]any
	_ = json.Unmarshal(request.Params, &params)
	meta, _ := params["_meta"].(map[string]any)
	declared, hasVersion := meta["io.modelcontextprotocol/protocolVersion"]
	if hasVersion && declared != version {
		return mismatch
	}
	// Notifications do not carry the standard request headers. Keep unversioned
	// compatibility requests working; explicit modern requests must mirror body.
	if len(request.ID) == 0 || version != currentProtocol || (headers.Get("MCP-Protocol-Version") == "" && !hasVersion) {
		return nil
	}
	if values := headers.Values("MCP-Method"); len(values) != 1 || values[0] != request.Method {
		return mismatch
	}
	key := ""
	switch request.Method {
	case "tools/call", "prompts/get":
		key = "name"
	case "resources/read":
		key = "uri"
	}
	if key != "" {
		values := headers.Values("MCP-Name")
		if len(values) != 1 || values[0] == "" {
			return mismatch
		}
		name, ok := decodeMCPHeaderValue(values[0])
		if !ok || name != params[key] {
			return mismatch
		}
	}
	if request.Method == "tools/call" {
		name, _ := params["name"].(string)
		if entry, ok := mcpRuntimeCatalog.tool(name); ok {
			schema, _ := entry.definition["inputSchema"].(map[string]any)
			args, _ := params["arguments"].(map[string]any)
			return validateMCPParamHeaders(headers, schema, args)
		}
	}
	return nil
}

func validateMCPParamHeaders(headers http.Header, schema, args map[string]any) error {
	props, _ := schema["properties"].(map[string]any)
	for name, raw := range props {
		prop, _ := raw.(map[string]any)
		value := args[name]
		if suffix, ok := prop["x-mcp-header"].(string); ok && suffix != "" {
			values := headers.Values("MCP-Param-" + suffix)
			if value == nil {
				if len(values) > 0 {
					return errors.New("unexpected MCP parameter header")
				}
			} else if len(values) != 1 || !mcpHeaderPrimitiveEqual(values[0], value) {
				return errors.New("MCP parameter header mismatch")
			}
		}
		nested, _ := value.(map[string]any)
		if err := validateMCPParamHeaders(headers, prop, nested); err != nil {
			return err
		}
	}
	return nil
}

func decodeMCPHeaderValue(header string) (string, bool) {
	if !strings.HasPrefix(header, "=?base64?") {
		return header, true
	}
	if !strings.HasSuffix(header, "?=") {
		return "", false
	}
	decoded, err := base64.StdEncoding.Strict().DecodeString(strings.TrimSuffix(strings.TrimPrefix(header, "=?base64?"), "?="))
	if err != nil || !utf8.Valid(decoded) {
		return "", false
	}
	return string(decoded), true
}

func mcpHeaderPrimitiveEqual(header string, value any) bool {
	var valid bool
	header, valid = decodeMCPHeaderValue(header)
	if !valid {
		return false
	}

	switch value := value.(type) {
	case string:
		return header == value
	case bool:
		return header == strconv.FormatBool(value)
	case float64:
		number, err := strconv.ParseFloat(header, 64)
		return err == nil && !math.IsInf(number, 0) && !math.IsNaN(number) && number == value
	default:
		return false
	}
}
