package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	taskSubmissionExtension       = "io.gregale/task-submission"
	taskSubmissionLookupExtension = "io.gregale/task-submission-lookup"
	taskSubmissionKeyMeta         = "io.gregale/task-submission-key"
	taskSubmissionMethod          = "gregale/tasks/submit"
	taskSubmissionLookupMethod    = "gregale/tasks/lookup"
	taskInboxMethod               = "gregale/tasks/inbox"
	taskInputReviewMethod         = "gregale/tasks/input-review"
	taskRespondReviewedMethod     = "gregale/tasks/respond-reviewed"
	taskCheckReviewedMethod       = "gregale/tasks/check-reviewed-response"
)

func taskSubmissionIssuedAt(key string) (time.Time, bool) {
	if !taskIDPattern.MatchString(key) || key[14] != '7' || key != strings.ToLower(key) {
		return time.Time{}, false
	}
	millis, err := strconv.ParseInt(key[:8]+key[9:13], 16, 64)
	return time.UnixMilli(millis), err == nil
}

// olderLegacyProtocols share the 2025-11-25 wire format for the tools,
// resources and prompts this starter serves. Clients pinned to them (SDKs
// released before late 2025) connect only when the server negotiates them.
var olderLegacyProtocols = []string{"2025-06-18", "2025-03-26"}

// unversionedLegacyProtocol is the revision a request without any version
// header or _meta declaration speaks (MCP 2025-06-18, "Protocol Version Header").
const unversionedLegacyProtocol = "2025-03-26"

func supportedMCPVersions(legacy bool) []string {
	versions := []string{currentProtocol}
	if legacy {
		versions = append(versions, legacyProtocol)
		versions = append(versions, olderLegacyProtocols...)
	}
	return versions
}

// isLegacyProtocol reports a pre-2026 revision this starter serves.
func isLegacyProtocol(version string) bool {
	if version == legacyProtocol {
		return true
	}
	for _, older := range olderLegacyProtocols {
		if version == older {
			return true
		}
	}
	return false
}

// protocolSupported reports whether version is served under this config.
func protocolSupported(version string, legacy bool) bool {
	return version == currentProtocol || (legacy && isLegacyProtocol(version))
}

// negotiateProtocol answers initialize: the requested revision when served,
// otherwise the newest revision a client of that era can speak. Refusing the
// handshake outright left older clients nothing to retry with.
func negotiateProtocol(requested string, legacy bool) string {
	if protocolSupported(requested, legacy) {
		return requested
	}
	if legacy {
		return legacyProtocol
	}
	return currentProtocol
}

// requestProtocol picks the revision a request speaks: the version header,
// else its _meta declaration, else (legacy) the unversioned 2025-03-26 default.
// initialize always negotiates from its body: the handshake is where an older
// client learns which revision to send, so a header naming an unserved
// revision must not refuse it.
func requestProtocol(headers http.Header, request rpcRequest, legacy bool) string {
	var params struct {
		Meta            map[string]any `json:"_meta"`
		ProtocolVersion string         `json:"protocolVersion"`
	}
	_ = json.Unmarshal(request.Params, &params)
	if request.Method == "initialize" {
		return negotiateProtocol(params.ProtocolVersion, legacy)
	}
	if version := headers.Get("MCP-Protocol-Version"); version != "" {
		return version
	}
	if declared, ok := params.Meta["io.modelcontextprotocol/protocolVersion"].(string); ok && declared != "" {
		return declared
	}
	if legacy {
		return unversionedLegacyProtocol
	}
	return currentProtocol
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
	methodHeaders := headers.Values("MCP-Method")
	expectedMethod := request.Method
	submissionKey, hasKey := meta[taskSubmissionKeyMeta]
	if hasKey || slices.Contains(methodHeaders, taskSubmissionMethod) {
		// Require an explicitly versioned call so older replicas reject the
		// extension marker instead of dispatching an ordinary tools/call.
		value, _ := submissionKey.(string)
		if _, valid := taskSubmissionIssuedAt(value); !valid || request.Method != "tools/call" ||
			headers.Get("MCP-Protocol-Version") != currentProtocol || version != currentProtocol || len(request.ID) == 0 {
			return mismatch
		}
		expectedMethod = taskSubmissionMethod
	} else if len(request.ID) == 0 || version != currentProtocol || (headers.Get("MCP-Protocol-Version") == "" && !hasVersion) {
		// Notifications and unversioned compatibility requests keep their
		// existing behavior. Explicit modern requests must mirror the body.
		return nil
	}
	if len(methodHeaders) != 1 || methodHeaders[0] != expectedMethod {
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
