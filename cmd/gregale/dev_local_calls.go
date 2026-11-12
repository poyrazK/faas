package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sort"
	"strings"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
)

// Typed function calls under `gregale dev --local` (ADR-20261111150000):
// each function gets GREGALE_FUNCTIONS and, for every sibling, the
// GREGALE_SERVICE_<NAME>_URL the SDK clients prefer over the binding alias,
// pointing at this server's path route for that sibling. A call reaches the
// local sibling; a send answers 202 at once and runs it in the background, as
// the platform's async path does; the callee's input schema is checked by
// its local adapter exactly as deployed.

// devLocalFunctionPrefix routes a sibling call by path, so no *.localhost
// name has to resolve inside the caller's runtime.
const devLocalFunctionPrefix = "/__gregale/functions/"

var serviceEnvKeyUnsafe = regexp.MustCompile(`[^A-Z0-9]`)

// serviceURLEnv is the variable the SDKs read for slug's base URL.
func serviceURLEnv(slug string) string {
	return "GREGALE_SERVICE_" + serviceEnvKeyUnsafe.ReplaceAllString(strings.ToUpper(slug), "_") + "_URL"
}

// wireLocalFunctionCalls fills each function's callables and input schema
// from the project's plan. Locally a sibling's "slug" is its function name;
// calls: apps keep their slug and are unreachable here. A plan error leaves
// the functions unwired rather than refusing to start.
func wireLocalFunctionCalls(m *gregalemanifest.Manifest, projectDir string, fns []*localFunction) []gregalemanifest.FunctionPlan {
	resolved, err := m.PlanFunctions(projectDir, functionTypesProjectSlug)
	if err != nil {
		return nil
	}
	specs := make([]gregalemanifest.FunctionSpec, 0, len(resolved))
	for _, p := range resolved {
		specs = append(specs, p.Spec)
	}
	graph, err := gregalemanifest.FunctionCallGraph(specs, func(name string) string { return name })
	if err != nil {
		return resolved
	}
	byName := make(map[string]gregalemanifest.FunctionPlan, len(resolved))
	for _, p := range resolved {
		byName[p.Spec.Name] = p
	}
	for _, fn := range fns {
		fn.Calls = graph[fn.Name].Names
		fn.InputSchema = byName[fn.Name].Spec.InputSchema
	}
	return resolved
}

// callEnvironment is the typed-call part of fn's environment.
func (fn *localFunction) callEnvironment(local map[string]bool) []string {
	var env []string
	if len(fn.Calls) > 0 {
		names, _ := json.Marshal(fn.Calls)
		env = append(env, api.FunctionCallablesEnv+"="+string(names))
		slugs := make([]string, 0, len(fn.Calls))
		for _, slug := range fn.Calls {
			if local[slug] {
				slugs = append(slugs, slug)
			}
		}
		sort.Strings(slugs)
		for _, slug := range slugs {
			env = append(env, fmt.Sprintf("%s=http://127.0.0.1:%d%s%s", serviceURLEnv(slug), fn.devPort, devLocalFunctionPrefix, slug))
		}
	}
	if len(fn.InputSchema) > 0 {
		env = append(env, api.FunctionInputSchemaEnv+"="+string(fn.InputSchema))
	}
	return env
}

// serveFunctionCall handles a sibling call on the path route: it reports
// whether path was one.
func (s *devLocalServer) serveFunctionCall(w http.ResponseWriter, r *http.Request) bool {
	rest, ok := strings.CutPrefix(r.URL.Path, devLocalFunctionPrefix)
	if !ok {
		return false
	}
	name, path, _ := strings.Cut(rest, "/")
	fn := s.byName[name]
	if fn == nil {
		http.Error(w, fmt.Sprintf("no local function %q", name), http.StatusNotFound)
		return true
	}
	r.URL.Path, r.URL.RawPath = "/"+path, ""
	r.RequestURI = r.URL.RequestURI()
	if r.Header.Get(api.FunctionInvokeHeader) != api.FunctionInvokeAsync {
		s.invoke(w, r, fn, "")
		return true
	}
	r.Header.Del(api.FunctionInvokeHeader)
	body, err := io.ReadAll(io.LimitReader(r.Body, 8<<20))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return true
	}
	id := uuid.NewString()
	sent := httptest.NewRequest(r.Method, r.URL.RequestURI(), strings.NewReader(string(body)))
	sent.Header = r.Header.Clone()
	go func() {
		rec := httptest.NewRecorder()
		s.invoke(rec, sent, fn, "async_invoke")
		_, _ = fmt.Fprintf(s.out, "%s async invocation %s finished with %d\n", fn.Name, id, rec.Code)
	}()
	w.Header().Set("content-type", "application/json")
	w.Header().Set(api.InvocationIDHeader, id)
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(api.AsyncInvokeResponse{ID: id})
	return true
}

// refreshLocalFunctionTypes rewrites generated typed-client files that are out
// of date, as gregale deploy does, so local runs see current schemas.
func refreshLocalFunctionTypes(projectDir string, out io.Writer) {
	m, ok, err := functionSetManifest(projectDir)
	if err != nil || !ok {
		return
	}
	resolved, err := m.PlanFunctions(projectDir, functionTypesProjectSlug)
	if err != nil {
		return
	}
	if err := refreshFunctionTypes(projectDir, resolved, functionTypesProjectSlug, out); err != nil {
		_, _ = fmt.Fprintf(out, "warning: generated function types were not refreshed: %v\n", err)
	}
}
