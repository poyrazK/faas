package mcpopenapi

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"slices"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// Options always names the target and operations explicitly. Document servers,
// credentials, examples and links never authorize requests or execution.
type Options struct {
	ServerURL    string
	OperationIDs []string
}

type Parameter struct {
	Name     string         `json:"name"`
	In       string         `json:"in"`
	Required bool           `json:"required"`
	Schema   map[string]any `json:"schema"`
}

type Operation struct {
	ID             string         `json:"id"`
	Method         string         `json:"method"`
	Path           string         `json:"path"`
	Description    string         `json:"description"`
	Parameters     []Parameter    `json:"parameters"`
	BodySchema     map[string]any `json:"body_schema,omitempty"`
	BodyRequired   bool           `json:"body_required"`
	ResponseSchema map[string]any `json:"response_schema"`
	ResponseStatus int            `json:"response_status"`
}

type Manifest struct {
	Version          int         `json:"version"`
	DocumentSHA256   string      `json:"document_sha256"`
	ServerURL        string      `json:"server_url"`
	Operations       []Operation `json:"operations"`
	RequestMaxBytes  int         `json:"request_max_bytes"`
	ResponseMaxBytes int         `json:"response_max_bytes"`
	TimeoutMS        int64       `json:"timeout_ms"`
	MaxJSONDepth     int         `json:"max_json_depth"`
	URLMaxBytes      int         `json:"url_max_bytes"`
}

// Project contains deterministic overlays for the existing Node starter.
// Fixtures remain examples requiring behavioral assertions before publication.
type Project struct {
	Manifest Manifest
	Files    map[string][]byte
}

//go:embed runtime/adapter.js
var adapterSource []byte

//go:embed runtime/http.js
var httpSource []byte

//go:embed runtime/server.js
var serverSource []byte

// Generate is a pure source transformation: it performs no filesystem, network,
// credential, deployment or application-execution operations.
func Generate(data []byte, options Options) (Project, error) {
	document, err := decodeDocument(data)
	if err != nil {
		return Project{}, err
	}
	server, err := validateServer(options.ServerURL)
	if err != nil {
		return Project{}, err
	}
	if len(options.OperationIDs) == 0 || len(options.OperationIDs) > api.ToolOpenAPIOperationsMax {
		return Project{}, fmt.Errorf("select 1 to %d operation IDs", api.ToolOpenAPIOperationsMax)
	}
	selected := map[string]bool{}
	for _, id := range options.OperationIDs {
		if !operationName(id) || selected[id] {
			return Project{}, errors.New("operation IDs must be unique bounded printable names without prototype keys")
		}
		selected[id] = true
	}
	paths, err := object(document["paths"], "paths")
	if err != nil {
		return Project{}, err
	}
	resolver := schemaResolver{legacy: strings.HasPrefix(document["openapi"].(string), "3.0.")}
	if components, ok := document["components"].(map[string]any); ok {
		resolver.components, _ = components["schemas"].(map[string]any)
	}
	manifest := Manifest{Version: 1, ServerURL: server, RequestMaxBytes: api.ToolInvocationArgsMaxBytes,
		ResponseMaxBytes: api.ToolOpenAPIResponseMaxBytes, TimeoutMS: api.ToolOpenAPIRequestTimeout.Milliseconds(),
		MaxJSONDepth: api.ToolOpenAPISchemaMaxDepth, URLMaxBytes: api.ToolOpenAPIURLMaxBytes}
	digest := sha256.Sum256(data)
	manifest.DocumentSHA256 = hex.EncodeToString(digest[:])
	seen := map[string]bool{}
	for path, rawPath := range paths {
		item, err := object(rawPath, "path item")
		if err != nil {
			return Project{}, err
		}
		for _, method := range []string{"get", "post", "put", "patch", "delete"} {
			raw, exists := item[method]
			if !exists {
				continue
			}
			op, err := object(raw, "operation")
			if err != nil {
				return Project{}, err
			}
			id, _ := op["operationId"].(string)
			if !selected[id] {
				continue
			}
			if seen[id] {
				return Project{}, fmt.Errorf("selected operation ID %q is ambiguous", id)
			}
			seen[id] = true
			operation, err := compileOperation(id, method, path, item, op, document, &resolver)
			if err != nil {
				return Project{}, fmt.Errorf("operation %q: %w", id, err)
			}
			manifest.Operations = append(manifest.Operations, operation)
		}
	}
	if len(manifest.Operations) != len(selected) {
		return Project{}, errors.New("a selected operation is missing or uses an unsupported HTTP method")
	}
	slices.SortFunc(manifest.Operations, func(a, b Operation) int { return strings.Compare(a.ID, b.ID) })
	body, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil || len(body) > api.ToolOpenAPIManifestMaxBytes {
		return Project{}, errors.New("generated manifest exceeds the size limit; select fewer or simpler operations")
	}
	return renderProject(manifest, append(body, '\n'))
}

func operationName(name string) bool {
	if !safeName(name) || name[0] < 'A' || name[0] > 'Z' && name[0] < 'a' || name[0] > 'z' {
		return false
	}
	for _, r := range name {
		if r != '_' && r != '-' && r != '.' && (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') {
			return false
		}
	}
	return true
}

func validateServer(value string) (string, error) {
	u, err := url.Parse(value)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.ForceQuery || u.Opaque != "" || len(value) > api.ToolOAuthURLMaxBytes {
		return "", errors.New("server_url must be an explicit HTTPS base URL without credentials, query or fragment")
	}
	host := strings.ToLower(u.Hostname())
	if host == "" || strings.HasSuffix(host, ".") || strings.ContainsAny(host, "{}%\\") || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".internal") {
		return "", errors.New("server_url must name a public hostname or public IP address")
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		if !publicIP(ip) {
			return "", errors.New("server_url must not use a private, reserved or local address")
		}
	} else {
		if !strings.Contains(host, ".") || !strings.ContainsAny(host[strings.LastIndex(host, ".")+1:], "abcdefghijklmnopqrstuvwxyz") {
			return "", errors.New("server_url must use a public DNS name or canonical public IP address")
		}
		for _, r := range host {
			if r != '.' && r != '-' && (r < 'a' || r > 'z') && (r < '0' || r > '9') {
				return "", errors.New("server_url hostname must use ASCII DNS labels")
			}
		}
	}
	if strings.ContainsAny(u.EscapedPath(), "%\\{}") || strings.Contains(u.Path, "//") {
		return "", errors.New("server_url path must be literal and contain no encoded or repeated separators")
	}
	for _, segment := range strings.Split(u.Path, "/") {
		if segment == "." || segment == ".." {
			return "", errors.New("server_url path must not contain dot segments")
		}
	}
	if u.Port() != "" && u.Port() != "443" {
		return "", errors.New("server_url must use the standard HTTPS port")
	}
	return strings.TrimSuffix(u.String(), "/"), nil
}

func publicIP(ip netip.Addr) bool {
	if ip.Is4In6() {
		return false
	}
	ip = ip.Unmap()
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return false
	}
	if ip.Is4() {
		b := ip.As4()
		switch {
		case b[0] == 0 || b[0] == 10 || b[0] == 127 || b[0] >= 224:
			return false
		case b[0] == 100 && b[1] >= 64 && b[1] <= 127:
			return false
		case b[0] == 169 && b[1] == 254:
			return false
		case b[0] == 192 && (b[1] == 0 && (b[2] == 0 || b[2] == 2) || b[1] == 168 || b[1] == 88 && b[2] == 99):
			return false
		case b[0] == 198 && (b[1] == 18 || b[1] == 19 || b[1] == 51 && b[2] == 100):
			return false
		case b[0] == 203 && b[1] == 0 && b[2] == 113:
			return false
		}
		return true
	}
	b := ip.As16()
	// Only ordinary global IPv6; exclude translation, transition and special
	// allocation space that can embed a local IPv4 destination.
	if b[0]&0xe0 != 0x20 {
		return false
	}
	if b[0] == 0x20 && (b[1] == 0x02 || b[1] == 0x01 && (b[2] < 2 || b[2] == 0x0d && b[3] == 0xb8)) {
		return false
	}
	return b[0] != 0x3f || b[1] != 0xff || b[2] >= 0x10
}

func compileOperation(id, method, path string, item, op, document map[string]any, resolver *schemaResolver) (Operation, error) {
	out := Operation{ID: id, Method: strings.ToUpper(method), Path: path, Parameters: []Parameter{},
		Description: "Call the selected public API operation " + id + "."}
	if summary, ok := op["summary"].(string); ok && len(summary) <= 1024 {
		out.Description += " " + summary
	}
	security := document["security"]
	if value, exists := document["security"]; exists && value == nil {
		return out, errors.New("security must be an explicit array")
	}
	if value, exists := op["security"]; exists {
		if value == nil {
			return out, errors.New("security must be an explicit array")
		}
		security = value
	}
	if security != nil {
		list, ok := security.([]any)
		if !ok || len(list) != 0 {
			return out, errors.New("only public operations with empty effective security are supported")
		}
	}
	if !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") || strings.ContainsAny(path, "?#%\\") {
		return out, errors.New("operation paths must be literal absolute paths with named parameter segments")
	}
	if item["$ref"] != nil {
		return out, errors.New("expand path item references before generation")
	}
	for _, char := range path {
		if char < '!' || char > '~' {
			return out, errors.New("operation paths must use printable ASCII without spaces")
		}
	}
	parameters, err := compileParameters(item["parameters"], op["parameters"], resolver)
	if err != nil {
		return out, err
	}
	out.Parameters = parameters
	for _, segment := range strings.Split(path, "/") {
		if strings.HasPrefix(segment, "{") && strings.HasSuffix(segment, "}") {
			name := strings.TrimSuffix(strings.TrimPrefix(segment, "{"), "}")
			if !slices.ContainsFunc(parameters, func(p Parameter) bool { return p.In == "path" && p.Name == name }) {
				return out, errors.New("every path placeholder requires an explicit required path parameter")
			}
		} else if segment == "." || segment == ".." || strings.ContainsAny(segment, "{}") {
			return out, errors.New("path placeholders must occupy a whole segment; dot segments are unsupported")
		}
	}
	for _, p := range parameters {
		if p.In == "path" && !slices.Contains(strings.Split(path, "/"), "{"+p.Name+"}") {
			return out, errors.New("path parameter has no matching placeholder")
		}
	}
	if value, exists := op["requestBody"]; exists {
		if method == "get" || method == "delete" {
			return out, errors.New("GET and DELETE request bodies are unsupported")
		}
		body, err := object(value, "requestBody")
		if err != nil {
			return out, err
		}
		if body["$ref"] != nil {
			return out, errors.New("expand requestBody references before generation")
		}
		if value, exists := body["required"]; exists {
			var ok bool
			out.BodyRequired, ok = value.(bool)
			if !ok {
				return out, errors.New("requestBody.required must be a boolean")
			}
		}
		out.BodySchema, err = jsonContentSchema(body, resolver)
		if err != nil {
			return out, err
		}
	}
	responses, err := object(op["responses"], "responses")
	if err != nil {
		return out, err
	}
	for status, raw := range responses {
		if len(status) == 3 && status[0] == '2' {
			if status[1] < '0' || status[1] > '9' || status[2] < '0' || status[2] > '9' || out.ResponseStatus != 0 {
				return out, errors.New("select operations with exactly one concrete JSON success response")
			}
			out.ResponseStatus = 200 + int(status[1]-'0')*10 + int(status[2]-'0')
			response, err := object(raw, "response")
			if err != nil || response["$ref"] != nil {
				return out, errors.New("expand response references before generation")
			}
			out.ResponseSchema, err = jsonContentSchema(response, resolver)
			if err != nil {
				return out, err
			}
		}
	}
	if out.ResponseStatus == 0 {
		return out, errors.New("operation requires one concrete JSON success response")
	}
	return out, nil
}

func jsonContentSchema(container map[string]any, resolver *schemaResolver) (map[string]any, error) {
	content, err := object(container["content"], "JSON content")
	if err != nil || len(content) != 1 {
		return nil, errors.New("exactly one application/json content type is required")
	}
	media, err := object(content["application/json"], "application/json")
	if err != nil {
		return nil, err
	}
	return resolver.schema(media["schema"], 0, map[string]bool{})
}

func compileParameters(pathValues, opValues any, resolver *schemaResolver) ([]Parameter, error) {
	parameters := map[string]Parameter{}
	for _, value := range []any{pathValues, opValues} {
		if value == nil {
			continue
		}
		list, ok := value.([]any)
		if !ok {
			return nil, errors.New("parameters must be an array")
		}
		seen := map[string]bool{}
		for _, value := range list {
			p, err := object(value, "parameter")
			if err != nil || p["$ref"] != nil {
				return nil, errors.New("expand parameter references before generation")
			}
			name, _ := p["name"].(string)
			location, _ := p["in"].(string)
			key := location + ":" + name
			if !safeName(name) || strings.ContainsAny(name, "{}") || location != "path" && location != "query" || seen[key] {
				return nil, errors.New("parameters require unique safe names in path or query; header and cookie inputs are unsupported")
			}
			seen[key] = true
			required, _ := p["required"].(bool)
			if value, exists := p["required"]; exists {
				if _, ok := value.(bool); !ok {
					return nil, errors.New("parameter.required must be a boolean")
				}
			}
			if location == "path" && !required {
				return nil, errors.New("path parameters must be required")
			}
			if style, exists := p["style"]; exists {
				expected := "form"
				if location == "path" {
					expected = "simple"
				}
				if style != expected {
					return nil, errors.New("only simple path and form query parameters are supported")
				}
			}
			if p["allowReserved"] == true || p["content"] != nil {
				return nil, errors.New("reserved-value and content parameters are unsupported")
			}
			schema, err := resolver.schema(p["schema"], 0, map[string]bool{})
			if err != nil {
				return nil, err
			}
			kind, _ := schema["type"].(string)
			if !slices.Contains([]string{"string", "integer", "number", "boolean"}, kind) || schema["nullable"] == true {
				return nil, errors.New("path and query parameters must have non-null scalar schemas")
			}
			parameters[key] = Parameter{Name: name, In: location, Required: required, Schema: schema}
		}
	}
	out := []Parameter{}
	for _, p := range parameters {
		out = append(out, p)
	}
	slices.SortFunc(out, func(a, b Parameter) int { return strings.Compare(a.In+":"+a.Name, b.In+":"+b.Name) })
	return out, nil
}
