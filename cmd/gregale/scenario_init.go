package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"maps"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/openapidiff"
	"gopkg.in/yaml.v3"
)

const testOpenAPIDocumentLimit = 5 << 20

// cmdTestInit creates a conservative, runnable smoke scenario from public,
// parameter-free GET endpoints. Business assertions remain customer-owned.
func cmdTestInit(args []string) int {
	fs := newFlagSet("test init", flag.ContinueOnError)
	from := fs.String("from", "", "local OpenAPI 3.0 or 3.1 document")
	project := fs.String("project", "", "Gregale project slug")
	source := fs.String("source", "", "application source directory, relative to the manifest (default: the workload holding --from, else .)")
	scenario := fs.String("scenario", "api-smoke", "scenario name")
	output := fs.String("output", "gregale-test.yaml", "output manifest path; must not exist")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if rejectUnexpectedFlagArgs(fs) || fs.NArg() != 0 || *from == "" || *project == "" {
		PrintUsage(osStderr, "usage: gregale test init --from openapi.yaml --project PROJECT [--source DIR] [--scenario NAME] [--output PATH]", "test")
		return 1
	}
	if !api.ValidAppSlug(*project) || len(*scenario) < 3 || !testHTTPNamePattern.MatchString(*scenario) || *output == "" {
		return printErr("Invalid test scaffold options", errors.New("project, scenario, or output is invalid"))
	}
	inferredSource := *source == ""
	if inferredSource {
		*source = scaffoldTestSource(*from, *output)
	}
	data, err := readScaffoldOpenAPIDocument(*from)
	if err != nil {
		return printErr("Could not read OpenAPI document", err)
	}
	if len(data) > testOpenAPIDocumentLimit {
		return printErr("Invalid OpenAPI document", errors.New("document exceeds 5 MiB"))
	}
	steps, skipped, err := scaffoldTestHTTPRequests(data)
	if err != nil {
		return printErr("Could not scaffold test", err)
	}
	if err := writeTestHTTPManifest(*output, *project, *source, *scenario, steps); err != nil {
		return printErr("Could not create test manifest", err)
	}
	_, _ = fmt.Fprintf(osStdout, "Created %s with %d public GET smoke checks (%d operations skipped). Add auth, input fixtures, and business assertions for the remaining routes.\n", *output, len(steps), skipped)
	if inferredSource && *source != "." {
		_, _ = fmt.Fprintf(osStdout, "Scenario source: %s, the workload holding the OpenAPI document (override with --source).\n", *source)
	}
	return 0
}

func readScaffoldOpenAPIDocument(path string) ([]byte, error) {
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	// Nonblocking open and fstat reject FIFOs and devices without waiting for a
	// writer, even if the final path component changes while it is opened.
	file, err := root.OpenFile(filepath.Base(path), os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("OpenAPI document must be a regular file")
	}
	return io.ReadAll(io.LimitReader(file, testOpenAPIDocumentLimit+1))
}

// scaffoldTestSource defaults --source to the workload that holds the OpenAPI
// document. In a monorepo the document usually sits in its service directory
// (services/gateway/openapi.json), where "." names a repository root holding
// several workloads, so `gregale test --validate` failed until the manifest was
// edited by hand. The path is relative to the manifest, as the runner reads it.
func scaffoldTestSource(from, output string) string {
	dir := filepath.Dir(from)
	if detectShape(dir) == shapeUnknown {
		return "."
	}
	workload, err := filepath.Abs(dir)
	if err != nil {
		return "."
	}
	manifestDir, err := filepath.Abs(filepath.Dir(output))
	if err != nil {
		return "."
	}
	rel, err := filepath.Rel(manifestDir, workload)
	if err != nil || rel == "." {
		return "."
	}
	rel = filepath.ToSlash(rel)
	if !strings.HasPrefix(rel, "../") {
		rel = "./" + rel
	}
	return rel
}

func writeTestHTTPManifest(path, project, source, scenario string, steps []testHTTPRequest) error {
	type scaffoldRequest struct {
		Name    string            `yaml:"name"`
		Method  string            `yaml:"method"`
		Path    string            `yaml:"path"`
		Headers map[string]string `yaml:"headers,omitempty"`
		JSON    any               `yaml:"json,omitempty"`
		Expect  testHTTPExpect    `yaml:"expect"`
	}
	type scaffoldScenario struct {
		Project  string            `yaml:"project"`
		Source   string            `yaml:"source"`
		Requests []scaffoldRequest `yaml:"requests"`
	}
	manifest := struct {
		Version   int                         `yaml:"version"`
		Scenarios map[string]scaffoldScenario `yaml:"scenarios"`
	}{Version: 1, Scenarios: map[string]scaffoldScenario{scenario: {Project: project, Source: source}}}
	entry := manifest.Scenarios[scenario]
	for _, step := range steps {
		entry.Requests = append(entry.Requests, scaffoldRequest{Name: step.Name, Method: step.Method, Path: step.Path, Headers: step.Headers, JSON: step.JSON, Expect: step.Expect})
	}
	manifest.Scenarios[scenario] = entry
	body, err := yaml.Marshal(manifest)
	if err != nil {
		return err
	}
	if len(body) > testHTTPBodyLimit {
		return errors.New("generated manifest exceeds 1 MiB")
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err = file.Write(body); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return err
	}
	if err = file.Close(); err != nil {
		_ = os.Remove(path)
		return err
	}
	return nil
}

func scaffoldTestHTTPRequests(data []byte) ([]testHTTPRequest, int, error) {
	spec, err := openapidiff.LoadBytes(data)
	if err != nil {
		return nil, 0, err
	}
	if !strings.HasPrefix(spec.OpenAPIVersion(), "3.0.") && !strings.HasPrefix(spec.OpenAPIVersion(), "3.1.") {
		return nil, 0, errors.New("only OpenAPI 3.0 and 3.1 documents are supported")
	}
	var document map[string]any
	if err := yaml.Unmarshal(data, &document); err != nil {
		return nil, 0, err
	}
	globalSecurity := nonemptyOpenAPISecurity(document["security"])
	paths := make([]string, 0, len(spec.Paths))
	for path := range spec.Paths {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	steps := make([]testHTTPRequest, 0)
	skipped := 0
	usedNames := make(map[string]bool)
	for _, path := range paths {
		item := spec.Paths[path]
		if _, referenced := item.Raw["$ref"]; referenced {
			item, err = scaffoldReferencedPathItem(document, item.Raw)
			if err != nil {
				return nil, skipped, fmt.Errorf("path %s: %w", path, err)
			}
		}
		methods := make([]string, 0, len(item.Methods))
		for method := range item.Methods {
			methods = append(methods, method)
		}
		sort.Strings(methods)
		for _, method := range methods {
			op := item.Methods[method]
			if method != "get" || !strings.HasPrefix(path, "/") || strings.ContainsAny(path, "{}?#") || hasRequiredOpenAPIParameter(item.Raw["parameters"]) || hasRequiredOpenAPIParameter(op.Raw["parameters"]) || hasRequiredOpenAPIRequestBody(op.Raw["requestBody"]) || hasOpenAPIAuth(op.Raw, globalSecurity) {
				skipped++
				continue
			}
			prefix, err := scaffoldServerPrefix(document, item.Raw, op.Raw)
			if err != nil {
				return nil, skipped, fmt.Errorf("GET %s: %w", path, err)
			}
			status, contentType, err := scaffoldSuccessResponse(op.Responses, document)
			if err != nil {
				return nil, skipped, fmt.Errorf("GET %s: %w", path, err)
			}
			if status == 0 {
				skipped++
				continue
			}
			if len(steps) >= 100 {
				skipped++
				continue
			}
			name := sanitizeSlug("get-" + strings.ReplaceAll(strings.Trim(path, "/"), "/", "-"))
			if usedNames[name] {
				for suffix := 2; ; suffix++ {
					candidate := fmt.Sprintf("%s-%d", name, suffix)
					if len(candidate) > 64 {
						candidate = candidate[:64]
					}
					if !usedNames[candidate] {
						name = candidate
						break
					}
				}
			}
			usedNames[name] = true
			requestPath := strings.TrimSuffix(prefix, "/") + path
			if _, err := expandTestHTTPPath(requestPath, nil); err != nil {
				return nil, skipped, fmt.Errorf("GET %s: invalid server path: %w", path, err)
			}
			steps = append(steps, testHTTPRequest{Name: name, Method: "GET", Path: requestPath, Expect: testHTTPExpect{Status: status, ContentType: contentType}})
		}
	}
	if len(steps) == 0 {
		return nil, skipped, errors.New("no public, parameter-free GET operation with a 2xx response was found; declare requests manually")
	}
	return steps, skipped, nil
}

func scaffoldReferencedPathItem(document, item map[string]any) (*openapidiff.PathItem, error) {
	resolved, err := resolveScaffoldPathItem(document, item, make(map[string]bool))
	if err != nil {
		return nil, err
	}
	for key := range resolved {
		switch key {
		case "summary", "description", "servers", "parameters", "get", "put", "post", "delete", "options", "head", "patch", "trace":
		default:
			if !strings.HasPrefix(key, "x-") {
				return nil, fmt.Errorf("referenced path item has unsupported field %q", key)
			}
		}
	}
	result := &openapidiff.PathItem{Raw: resolved, Methods: make(map[string]*openapidiff.Operation)}
	for _, method := range []string{"get", "post", "put", "patch", "delete", "options", "head", "trace"} {
		value, present := resolved[method]
		if !present {
			continue
		}
		raw, ok := value.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("referenced %s operation must be an object", method)
		}
		operation := &openapidiff.Operation{Raw: raw, Responses: make(map[string]*openapidiff.Response)}
		if responses, ok := raw["responses"].(map[string]any); ok {
			for code, value := range responses {
				response, ok := value.(map[string]any)
				if !ok {
					return nil, fmt.Errorf("referenced response %q must be an object", code)
				}
				operation.Responses[code] = &openapidiff.Response{Raw: response}
			}
		}
		result.Methods[method] = operation
	}
	return result, nil
}

func resolveScaffoldPathItem(document, item map[string]any, seen map[string]bool) (map[string]any, error) {
	rawRef, present := item["$ref"]
	if !present {
		return maps.Clone(item), nil
	}
	ref, ok := rawRef.(string)
	if !ok || !strings.HasPrefix(ref, "#/") {
		return nil, errors.New("path item references must be local JSON pointers")
	}
	pointer, err := url.PathUnescape(ref[1:])
	if err != nil || len(seen) >= 100 || seen[pointer] {
		return nil, errors.New("path item reference is invalid, cyclic, or too deep")
	}
	if _, err := testJSONPointerParts(pointer); err != nil {
		return nil, errors.New("path item reference has an invalid JSON pointer")
	}
	value, found := testJSONPointer(document, pointer)
	target, ok := value.(map[string]any)
	if !found || !ok {
		return nil, errors.New("path item reference could not be resolved to an object")
	}
	seen[pointer] = true
	resolved, err := resolveScaffoldPathItem(document, target, seen)
	if err != nil {
		return nil, err
	}
	for key, value := range item {
		if key == "$ref" {
			continue
		}
		if _, exists := resolved[key]; exists {
			return nil, fmt.Errorf("path item reference has an ambiguous sibling field %q", key)
		}
		resolved[key] = value
	}
	return resolved, nil
}

func nonemptyOpenAPISecurity(raw any) bool {
	if raw == nil {
		return false
	}
	items, ok := raw.([]any)
	if !ok {
		return true
	}
	for _, item := range items {
		if clause, ok := item.(map[string]any); ok && len(clause) == 0 {
			return false
		}
	}
	return len(items) > 0
}

// Operation and path servers override the document servers. Different base
// paths or unresolved variables require a choice the scaffold cannot make.
func scaffoldServerPrefix(document, path, operation map[string]any) (string, error) {
	var raw any
	for _, scope := range []map[string]any{operation, path, document} {
		if value, exists := scope["servers"]; exists {
			raw = value
			break
		}
	}
	if raw == nil {
		return "", nil
	}
	servers, ok := raw.([]any)
	if !ok {
		return "", errors.New("servers must be an array")
	}
	prefix := ""
	for i, entry := range servers {
		server, ok := entry.(map[string]any)
		if !ok {
			return "", errors.New("invalid server declaration")
		}
		address, ok := server["url"].(string)
		if !ok || address == "" || strings.ContainsAny(address, "{}") {
			return "", errors.New("server URL must be explicit and cannot contain variables")
		}
		parsed, err := url.Parse(address)
		if err != nil || parsed.User != nil || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" || (parsed.IsAbs() && (parsed.Scheme != "http" && parsed.Scheme != "https" || parsed.Host == "")) || (!parsed.IsAbs() && (parsed.Host != "" || !strings.HasPrefix(address, "/") || strings.HasPrefix(address, "//"))) {
			return "", errors.New("server URL must be an HTTP URL or an absolute path without credentials, query, or fragment")
		}
		candidate := strings.TrimSuffix(parsed.EscapedPath(), "/")
		if i > 0 && candidate != prefix {
			return "", errors.New("server URLs have different path prefixes; select one in the OpenAPI document")
		}
		prefix = candidate
	}
	return prefix, nil
}

func hasOpenAPIAuth(operation map[string]any, global bool) bool {
	security, declared := operation["security"]
	if !declared {
		return global
	}
	return nonemptyOpenAPISecurity(security)
}

func hasRequiredOpenAPIParameter(raw any) bool {
	if raw == nil {
		return false
	}
	items, ok := raw.([]any)
	if !ok {
		return true
	}
	for _, item := range items {
		parameter, ok := item.(map[string]any)
		if !ok || parameter["$ref"] != nil || parameter["required"] == true {
			return true
		}
	}
	return false
}

func hasRequiredOpenAPIRequestBody(raw any) bool {
	if raw == nil {
		return false
	}
	body, ok := raw.(map[string]any)
	if !ok {
		return true
	}
	if _, referenced := body["$ref"]; referenced {
		return true
	}
	if required, exists := body["required"]; exists {
		value, ok := required.(bool)
		return !ok || value
	}
	return false
}

func scaffoldSuccessResponse(responses map[string]*openapidiff.Response, document map[string]any) (int, string, error) {
	status := 0
	var selected *openapidiff.Response
	for code, response := range responses {
		parsed, err := strconv.Atoi(code)
		if err != nil || parsed < 200 || parsed > 299 || (status != 0 && parsed >= status) {
			continue
		}
		status = parsed
		selected = response
	}
	if selected == nil {
		return 0, "", nil
	}
	raw := selected.Raw
	seen := map[string]bool{}
	for raw["$ref"] != nil {
		ref, ok := raw["$ref"].(string)
		if !ok || !strings.HasPrefix(ref, "#/components/responses/") || seen[ref] || len(seen) >= 100 {
			return 0, "", errors.New("unsupported or cyclic response reference")
		}
		seen[ref] = true
		value, found := testJSONPointer(document, strings.TrimPrefix(ref, "#"))
		raw, ok = value.(map[string]any)
		if !found || !ok {
			return 0, "", errors.New("response reference could not be resolved")
		}
	}
	if content, ok := raw["content"].(map[string]any); ok {
		if _, ok := content["application/json"]; ok {
			return status, "application/json", nil
		}
	}
	return status, "", nil
}
