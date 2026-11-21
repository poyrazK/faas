package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/functionschema"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
)

// Typed function calls under `gregale dev --local` (ADR-20261111150000):
// each function gets GREGALE_FUNCTIONS and, for every sibling, the
// GREGALE_SERVICE_<NAME>_URL the SDK clients prefer over the binding alias,
// pointing at this server's path route for that sibling. A call reaches the
// local sibling; a send answers 202 at once and runs it in the background, as
// the platform's async path does; the callee's input schema is checked by
// its local adapter exactly as deployed. A sender reads its sends back and
// names a completion target as on the platform (ADR-20261112160000).

// devLocalCallPrefix routes a sibling call by path, so no *.localhost name
// has to resolve inside the caller's runtime:
// /__gregale/calls/<caller>/<callee>/<path>. The caller in the path stands in
// for the platform's identification of the calling VM.
const devLocalCallPrefix = "/__gregale/calls/"

// devLocalPlatformPrefix is each function's GREGALE_PLATFORM_API_URL:
// /__gregale/platform/<caller>, serving /v1/invocations/<id>.
const devLocalPlatformPrefix = "/__gregale/platform/"

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
		if spec, ok, err := byName[fn.Name].Spec.WorkflowSpec(); err == nil && ok {
			fn.Workflow = &spec
		}
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
			env = append(env, fmt.Sprintf("%s=http://127.0.0.1:%d%s%s/%s", serviceURLEnv(slug), fn.devPort, devLocalCallPrefix, fn.Name, slug))
		}
		env = append(env, fmt.Sprintf("GREGALE_PLATFORM_API_URL=http://127.0.0.1:%d%s%s", fn.devPort, devLocalPlatformPrefix, fn.Name))
	}
	if len(fn.InputSchema) > 0 {
		env = append(env, api.FunctionInputSchemaEnv+"="+string(fn.InputSchema))
	}
	return env
}

// serveFunctionCall handles a sibling call or an invocation read on the path
// routes: it reports whether path was one.
func (s *devLocalServer) serveFunctionCall(w http.ResponseWriter, r *http.Request) bool {
	if rest, ok := strings.CutPrefix(r.URL.Path, devLocalPlatformPrefix); ok {
		if callerName, route, _ := strings.Cut(rest, "/"); strings.HasPrefix(route, "v1/workflow-runs") {
			s.serveWorkflowRuns(w, r, callerName, strings.TrimPrefix(strings.TrimPrefix(route, "v1/workflow-runs"), "/"))
			return true
		}
		s.serveInvocationRead(w, r, rest)
		return true
	}
	rest, ok := strings.CutPrefix(r.URL.Path, devLocalCallPrefix)
	if !ok {
		return false
	}
	callerName, rest, _ := strings.Cut(rest, "/")
	name, path, _ := strings.Cut(rest, "/")
	fn := s.byName[name]
	if fn == nil {
		http.Error(w, fmt.Sprintf("no local function %q", name), http.StatusNotFound)
		return true
	}
	r.URL.Path, r.URL.RawPath = "/"+path, ""
	r.RequestURI = r.URL.RequestURI()
	if r.Header.Get(api.FunctionInvokeHeader) != api.FunctionInvokeAsync {
		s.invokeCall(w, r, fn, "")
		return true
	}
	r.Header.Del(api.FunctionInvokeHeader)
	onComplete, err := s.completionTarget(callerName, r.Header.Get(api.FunctionOnCompleteHeader))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return true
	}
	r.Header.Del(api.FunctionOnCompleteHeader)
	body, err := io.ReadAll(io.LimitReader(r.Body, 8<<20))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return true
	}
	id := uuid.NewString()
	sent := httptest.NewRequest(r.Method, r.URL.RequestURI(), bytes.NewReader(body))
	sent.Header = r.Header.Clone()
	inv := s.invocations.start(id, callerName)
	go func() {
		rec := httptest.NewRecorder()
		s.invokeCall(rec, sent, fn, "async_invoke")
		_, _ = fmt.Fprintf(s.out, "%s async invocation %s finished with %d\n", fn.Name, id, rec.Code)
		st := s.invocations.finish(inv, rec.Code, rec.Body.Bytes())
		if onComplete != nil {
			s.deliverCompletion(onComplete, fn.Name, st)
		}
	}()
	w.Header().Set("content-type", "application/json")
	w.Header().Set(api.InvocationIDHeader, id)
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(api.AsyncInvokeResponse{ID: id})
	return true
}

// invokeCall runs a sibling call. A Go function has no in-language adapter,
// so its input schema is checked here as the deployed Go runner checks it.
func (s *devLocalServer) invokeCall(w http.ResponseWriter, r *http.Request, fn *localFunction, source string) {
	if fn.Family == "go" && len(fn.InputSchema) > 0 && r.Header.Get(api.FunctionCallHeader) == "1" {
		if schema, err := functionschema.Parse(fn.InputSchema); err == nil {
			body, _ := io.ReadAll(io.LimitReader(r.Body, 8<<20))
			r.Body = io.NopCloser(bytes.NewReader(body))
			if detail := schema.CheckBody(body); detail != "" {
				w.Header().Set("Content-Type", "application/problem+json")
				w.WriteHeader(http.StatusUnprocessableEntity)
				_ = json.NewEncoder(w).Encode(map[string]any{"title": "Function input invalid", "status": http.StatusUnprocessableEntity,
					"code": api.CodeFunctionInputInvalid, "detail": detail})
				return
			}
		}
	}
	s.invoke(w, r, fn, source)
}

// completionTarget resolves an X-Gregale-On-Complete value the way the
// platform does: "self" or a name the caller may call that is served here.
func (s *devLocalServer) completionTarget(callerName, value string) (*localFunction, error) {
	if value == "" {
		return nil, nil
	}
	caller := s.byName[callerName]
	if caller == nil {
		return nil, fmt.Errorf("on-complete needs a known caller")
	}
	if value == api.FunctionOnCompleteSelf {
		return caller, nil
	}
	if target, ok := caller.Calls[value]; ok {
		if fn := s.byName[target]; fn != nil {
			return fn, nil
		}
		return nil, fmt.Errorf("on-complete %q is a calls: app, which is not served locally", value)
	}
	return nil, fmt.Errorf("on-complete %q is not a function %s may call", value, callerName)
}

// deliverCompletion POSTs the platform's completion payload to target.
func (s *devLocalServer) deliverCompletion(target *localFunction, callee string, st api.FunctionInvocationStatus) {
	status := st.Outcome
	if status == "success" {
		status = "succeeded"
	}
	payload := map[string]any{"invocation_id": st.ID, "app_id": callee, "status": status, "outcome": st.Outcome, "attempts": st.Attempts}
	if len(st.Result) > 0 {
		payload["result"] = st.Result
	}
	if st.Error != "" {
		payload["error"] = st.Error
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Gregale-Invocation-Completion", st.ID)
	rec := httptest.NewRecorder()
	s.invoke(rec, req, target, "async_invoke")
}

// serveInvocationRead answers GET <platform>/<caller>/v1/invocations/<id>
// with the platform's shape; only the sender reads its sends.
func (s *devLocalServer) serveInvocationRead(w http.ResponseWriter, r *http.Request, rest string) {
	callerName, rest, _ := strings.Cut(rest, "/")
	id, ok := strings.CutPrefix(rest, "v1/invocations/")
	if !ok || r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	wait, _ := strconv.Atoi(r.URL.Query().Get("wait"))
	st, ok := s.invocations.read(r.Context().Done(), id, callerName, time.Duration(min(max(wait, 0), 30))*time.Second)
	if !ok {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]any{"status": http.StatusNotFound, "code": api.CodeNotFound, "detail": "invocation not found"})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(st)
}

// devLocalInvocations holds this run's sends for wait() and status().
type devLocalInvocations struct {
	mu   sync.Mutex
	byID map[string]*devLocalInvocation
}

type devLocalInvocation struct {
	sender string
	status api.FunctionInvocationStatus
	done   chan struct{}
}

func (d *devLocalInvocations) start(id, sender string) *devLocalInvocation {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.byID == nil {
		d.byID = map[string]*devLocalInvocation{}
	}
	inv := &devLocalInvocation{sender: sender, status: api.FunctionInvocationStatus{ID: id, State: "dispatching", Attempts: 1}, done: make(chan struct{})}
	d.byID[id] = inv
	return inv
}

func (d *devLocalInvocations) finish(inv *devLocalInvocation, code int, body []byte) api.FunctionInvocationStatus {
	d.mu.Lock()
	defer d.mu.Unlock()
	now := time.Now().UTC()
	st := &inv.status
	st.Done, st.CompletedAt = true, &now
	if code >= 200 && code < 300 {
		st.State, st.Outcome = "completed", "success"
		switch trimmed := bytes.TrimSpace(body); {
		case len(trimmed) == 0:
		case json.Valid(trimmed):
			st.Result = append(json.RawMessage(nil), trimmed...)
		default:
			st.Result, _ = json.Marshal(string(trimmed))
		}
	} else {
		st.State, st.Outcome = "failed", "failed"
		st.Error = fmt.Sprintf("HTTP %d: %s", code, strings.TrimSpace(string(body)))
	}
	close(inv.done)
	return *st
}

func (d *devLocalInvocations) read(cancel <-chan struct{}, id, sender string, wait time.Duration) (api.FunctionInvocationStatus, bool) {
	d.mu.Lock()
	inv := d.byID[id]
	d.mu.Unlock()
	if inv == nil || inv.sender != sender {
		return api.FunctionInvocationStatus{}, false
	}
	if wait > 0 {
		timer := time.NewTimer(wait)
		defer timer.Stop()
		select {
		case <-inv.done:
		case <-timer.C:
		case <-cancel:
		}
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return inv.status, true
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
