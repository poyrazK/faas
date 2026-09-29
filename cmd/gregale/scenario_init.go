package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/openapidiff"
	"gopkg.in/yaml.v3"
)

// cmdTestInit creates a conservative, runnable smoke scenario from public,
// parameter-free GET endpoints. Business assertions remain customer-owned.
func cmdTestInit(args []string) int {
	fs := newFlagSet("test init", flag.ContinueOnError)
	from := fs.String("from", "", "local OpenAPI 3.0 or 3.1 document")
	project := fs.String("project", "", "Gregale project slug")
	source := fs.String("source", ".", "application source directory")
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
	data, err := os.ReadFile(*from)
	if err != nil {
		return printErr("Could not read OpenAPI document", err)
	}
	if len(data) > 5<<20 {
		return printErr("Invalid OpenAPI document", errors.New("document exceeds 5 MiB"))
	}
	steps, skipped, err := scaffoldTestHTTPRequests(data)
	if err != nil {
		return printErr("Could not scaffold test", err)
	}
	type scaffoldRequest struct {
		Name   string         `yaml:"name"`
		Method string         `yaml:"method"`
		Path   string         `yaml:"path"`
		Expect testHTTPExpect `yaml:"expect"`
	}
	type scaffoldScenario struct {
		Project  string            `yaml:"project"`
		Source   string            `yaml:"source"`
		Requests []scaffoldRequest `yaml:"requests"`
	}
	manifest := struct {
		Version   int                         `yaml:"version"`
		Scenarios map[string]scaffoldScenario `yaml:"scenarios"`
	}{Version: 1, Scenarios: map[string]scaffoldScenario{*scenario: {Project: *project, Source: *source}}}
	entry := manifest.Scenarios[*scenario]
	for _, step := range steps {
		entry.Requests = append(entry.Requests, scaffoldRequest{Name: step.Name, Method: step.Method, Path: step.Path, Expect: step.Expect})
	}
	manifest.Scenarios[*scenario] = entry
	body, err := yaml.Marshal(manifest)
	if err != nil {
		return printErr("Could not encode test manifest", err)
	}
	file, err := os.OpenFile(*output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return printErr("Could not create test manifest", err)
	}
	if _, err = file.Write(body); err != nil {
		_ = file.Close()
		_ = os.Remove(*output)
		return printErr("Could not write test manifest", err)
	}
	if err = file.Close(); err != nil {
		return printErr("Could not close test manifest", err)
	}
	_, _ = fmt.Fprintf(osStdout, "Created %s with %d public GET smoke checks (%d operations skipped). Add auth, input fixtures, and business assertions for the remaining routes.\n", *output, len(steps), skipped)
	return 0
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
		methods := make([]string, 0, len(item.Methods))
		for method := range item.Methods {
			methods = append(methods, method)
		}
		sort.Strings(methods)
		for _, method := range methods {
			op := item.Methods[method]
			if method != "get" || !strings.HasPrefix(path, "/") || strings.ContainsAny(path, "{}?#") || hasRequiredOpenAPIParameter(item.Raw["parameters"]) || hasRequiredOpenAPIParameter(op.Raw["parameters"]) || hasOpenAPIAuth(op.Raw, globalSecurity) {
				skipped++
				continue
			}
			status, contentType := scaffoldSuccessResponse(op.Responses)
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
			steps = append(steps, testHTTPRequest{Name: name, Method: "GET", Path: path, Expect: testHTTPExpect{Status: status, ContentType: contentType}})
		}
	}
	if len(steps) == 0 {
		return nil, skipped, errors.New("no public, parameter-free GET operation with a 2xx response was found; declare requests manually")
	}
	return steps, skipped, nil
}

func nonemptyOpenAPISecurity(raw any) bool {
	if raw == nil {
		return false
	}
	items, ok := raw.([]any)
	return !ok || len(items) > 0
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

func scaffoldSuccessResponse(responses map[string]*openapidiff.Response) (int, string) {
	status := 0
	contentType := ""
	for code, response := range responses {
		parsed, err := strconv.Atoi(code)
		if err != nil || parsed < 200 || parsed > 299 || (status != 0 && parsed >= status) {
			continue
		}
		status = parsed
		contentType = ""
		if _, ok := response.Content["application/json"]; ok {
			contentType = "application/json"
		}
	}
	return status, contentType
}
