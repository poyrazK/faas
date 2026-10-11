package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/url"
	"regexp"
	"strings"
)

const (
	MCPProtocolVersion       = "2026-07-28"
	MCPLegacyProtocolVersion = "2025-11-25"
	MCPProbeMaxRequestBytes  = 64 << 10
	MCPProbeMaxResponseBytes = 512 << 10
)

var mcpProbeEndpointPathPattern = regexp.MustCompile(`^/(?:[A-Za-z0-9._~-]+/?)*$`)

// ValidMCPProbeHost accepts a URL authority only. It is used to preserve the
// app's deployment-preview hostname when the platform probes one exact replica;
// callers cannot supply a path, query, userinfo, or fragment.
func ValidMCPProbeHost(host string) bool {
	if host == "" || len(host) > 255 || strings.ContainsAny(host, " \t\r\n") {
		return false
	}
	u, err := url.Parse("http://" + host)
	return err == nil && u.User == nil && u.Host == host && u.Hostname() != "" && u.Path == "" && u.RawQuery == "" && u.Fragment == ""
}

// ValidMCPProbeEndpointPath accepts the literal endpoint path supported by
// Gregale's MCP hosting config. It excludes URL authorities, route patterns,
// query strings, fragments, escapes, and dot segments.
func ValidMCPProbeEndpointPath(path string) bool {
	if len(path) == 0 || len(path) > 1024 || !mcpProbeEndpointPathPattern.MatchString(path) {
		return false
	}
	segments := strings.Split(strings.TrimPrefix(path, "/"), "/")
	for i, segment := range segments {
		if segment == "" {
			if path == "/" || i == len(segments)-1 && strings.HasSuffix(path, "/") {
				continue
			}
			return false
		}
		if segment == "." || segment == ".." {
			return false
		}
	}
	return true
}

// MCPProbeRPCMethod validates a single read-only MCP JSON-RPC request and
// returns its method. Tool calls, resource reads, prompt rendering, and task
// operations are intentionally excluded from the exact-instance probe API.
func MCPProbeRPCMethod(body []byte) (string, bool) {
	if len(body) == 0 || len(body) > MCPProbeMaxRequestBytes {
		return "", false
	}
	var request struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Method  string          `json:"method"`
		Params  json.RawMessage `json:"params"`
	}
	if json.Unmarshal(body, &request) != nil || !mcpProbeJSONHasUniqueKeys(body) || request.JSONRPC != "2.0" {
		return "", false
	}
	switch request.Method {
	case "initialize", "server/discover", "tools/list", "resources/list", "resources/templates/list", "prompts/list":
		if len(request.ID) == 0 || !validMCPProbeRPCID(request.ID) {
			return "", false
		}
	case "notifications/initialized":
		if len(request.ID) != 0 {
			return "", false
		}
	default:
		return "", false
	}
	if len(request.Params) > 0 {
		var params map[string]json.RawMessage
		if json.Unmarshal(request.Params, &params) != nil || params == nil {
			return "", false
		}
	}
	return request.Method, true
}

func mcpProbeJSONHasUniqueKeys(body []byte) bool {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if !consumeMCPProbeJSONValue(decoder) {
		return false
	}
	_, err := decoder.Token()
	return err == io.EOF
}

func consumeMCPProbeJSONValue(decoder *json.Decoder) bool {
	token, err := decoder.Token()
	if err != nil {
		return false
	}
	delimiter, compound := token.(json.Delim)
	if !compound {
		return true
	}
	switch delimiter {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return false
			}
			key, ok := keyToken.(string)
			if !ok {
				return false
			}
			if _, duplicate := seen[key]; duplicate {
				return false
			}
			seen[key] = struct{}{}
			if !consumeMCPProbeJSONValue(decoder) {
				return false
			}
		}
		end, err := decoder.Token()
		return err == nil && end == json.Delim('}')
	case '[':
		for decoder.More() {
			if !consumeMCPProbeJSONValue(decoder) {
				return false
			}
		}
		end, err := decoder.Token()
		return err == nil && end == json.Delim(']')
	default:
		return false
	}
}

// MCPProbeRPCMatchesVersion ensures request metadata agrees with the selected
// protocol revision and prevents server/discover from being sent on legacy.
func MCPProbeRPCMatchesVersion(body []byte, version string) bool {
	if !ValidMCPProbeProtocolVersion(version) {
		return false
	}
	method, valid := MCPProbeRPCMethod(body)
	if !valid || method == "server/discover" && version != MCPProtocolVersion {
		return false
	}
	if method != "initialize" {
		return true
	}
	var request struct {
		Params struct {
			ProtocolVersion string `json:"protocolVersion"`
		} `json:"params"`
	}
	return json.Unmarshal(body, &request) == nil && request.Params.ProtocolVersion == version
}

func validMCPProbeRPCID(raw json.RawMessage) bool {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if decoder.Decode(&value) != nil {
		return false
	}
	switch value.(type) {
	case string, json.Number:
		return true
	default:
		return false
	}
}

// ValidMCPProbeProtocolVersion restricts diagnostic requests to the protocol
// revisions implemented by the Gregale MCP hosting profile.
func ValidMCPProbeProtocolVersion(version string) bool {
	return version == MCPProtocolVersion || version == MCPLegacyProtocolVersion
}

// ValidMCPProbeToken rejects values that cannot safely be placed in a bearer
// Authorization header. Empty tokens are allowed for open MCP endpoints.
func ValidMCPProbeToken(token string) bool {
	if len(token) > 8192 || strings.ContainsAny(token, "\r\n") {
		return false
	}
	for _, r := range token {
		if !(r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || strings.ContainsRune("-._~+/=", r)) {
			return false
		}
	}
	return true
}
