// edge_rules_trace_e2e_test.go — the edge-rule trace agrees with the gateway.
//
// `gregale edge rule trace` (and the dashboard's trace, the same engine in
// pkg/edgeruletrace) predicts what the edge does with a request from the
// app's rules. Nothing checked that prediction against the gateway, so the
// two could drift apart unnoticed. This test runs the real CLI against the
// real apid for a set of requests, sends the same requests through the real
// gateways, and compares.

package e2e_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/edgeruletrace"
)

// buildGregaleCLI builds the gregale CLI for a test, as the commit CLI
// acceptance test does.
func buildGregaleCLI(t *testing.T) string {
	t.Helper()
	cliPath := filepath.Join(t.TempDir(), "gregale")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if output, err := exec.CommandContext(ctx, "go", "build", "-o", cliPath, "github.com/onebox-faas/faas/cmd/gregale").CombinedOutput(); err != nil {
		t.Fatalf("build Gregale CLI: %v\n%s", err, output)
	}
	return cliPath
}

// TestE2E_EdgeRules_TraceMatchesGateway: wherever the trace says the edge
// answers a request, the gateway answers with the trace's status, Location,
// Retry-After and problem code; wherever it says the request continues, the
// app receives it at the trace's final path and the response carries the
// trace's response headers.
func TestE2E_EdgeRules_TraceMatchesGateway(t *testing.T) {
	f := newEdgeRulesFixture(t, "edge-trace")
	if f == nil {
		return
	}
	slug := f.app.Slug
	for _, req := range []map[string]any{
		edgeRuleRequest("redirect", "/old", map[string]any{"status_code": 302, "to": "/new"}),
		edgeRuleRequest("rewrite", "/api/**", map[string]any{"from": "/api", "to": "/v1"}),
		edgeRuleRequest("headers", "/**", map[string]any{
			"response_headers": []map[string]any{{"name": "X-Traced", "value": "yes", "action": "set"}},
		}),
		edgeRuleRequest("ip", "/admin/**", map[string]any{"deny": []string{"0.0.0.0/0", "::/0"}},
			map[string]any{"match_methods": []string{"GET"}}),
		edgeRuleRequest("maintenance", "/billing/**", map[string]any{"retry_after_seconds": 5, "message": "billing upgrade"}),
		edgeRuleRequest("validate", "/users", map[string]any{
			"schema":        map[string]any{"type": "object", "required": []string{"email"}, "properties": map[string]any{"email": map[string]any{"type": "string"}}},
			"content_types": []string{"application/json"},
		}, map[string]any{"match_methods": []string{"POST"}}),
	} {
		createEdgeRuleViaAPI(t, f, slug, req)
	}
	cli := buildGregaleCLI(t)
	jsonType := []string{"Content-Type:application/json"}

	for _, tc := range []struct {
		name, method, path, body string
		headers                  []string
	}{
		{name: "redirect", method: http.MethodGet, path: "/old"},
		{name: "rewrite", method: http.MethodGet, path: "/api/users"},
		{name: "headers only", method: http.MethodGet, path: "/"},
		{name: "ip refuses GET", method: http.MethodGet, path: "/admin/x"},
		{name: "ip refuses HEAD", method: http.MethodHead, path: "/admin/x"},
		{name: "ip lets POST through", method: http.MethodPost, path: "/admin/x"},
		{name: "maintenance", method: http.MethodGet, path: "/billing/x"},
		{name: "validate refuses", method: http.MethodPost, path: "/users", body: `{}`, headers: jsonType},
		{name: "validate passes", method: http.MethodPost, path: "/users", body: `{"email":"a@example.com"}`, headers: jsonType},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sim := traceRequest(t, f, cli, tc.method, tc.path, tc.body, tc.headers)
			if sim.Status != "complete" {
				t.Fatalf("trace is %s (%s): %s", sim.Status, sim.Outcome, sim.Reason)
			}
			headers := map[string]string{}
			for _, h := range tc.headers {
				name, value, _ := strings.Cut(h, ":")
				headers[name] = value
			}
			var body any
			if tc.body != "" {
				body = json.RawMessage(tc.body)
			}
			before := len(f.vmmd.Requests())
			hdr, respBody, code := edgeRetry(t, tc.method+" "+tc.path, func() (http.Header, []byte, int) {
				return doReqHeaders(t, f.h, f.host, tc.method, tc.path, body, headers)
			})
			if sim.StatusCode != 0 {
				assertEdgeAnswerMatchesTrace(t, tc.method, sim, hdr, respBody, code)
				if reached := len(f.vmmd.Requests()) - before; reached != 0 {
					t.Errorf("trace says the edge answers, but the app received %d requests", reached)
				}
				return
			}
			if code != http.StatusOK {
				t.Fatalf("trace says the request reaches the app, gateway answered %d: %s", code, respBody)
			}
			if got := f.vmmd.LastRequest().GetRequestUri(); got != sim.FinalPath {
				t.Errorf("app received %q, trace's final path is %q", got, sim.FinalPath)
			}
			for _, op := range sim.ResponseHeaderOps {
				if op.Action == "set" && hdr.Get(op.Name) != op.Value {
					t.Errorf("response header %s = %q, trace sets %q", op.Name, hdr.Get(op.Name), op.Value)
				}
			}
		})
	}
}

// traceRequest runs `gregale edge rule trace` for one request and returns
// its simulation.
func traceRequest(t *testing.T, f *normalPathFixture, cli, method, path, body string, headers []string) edgeruletrace.Simulation {
	t.Helper()
	args := []string{"--json", "edge", "rule", "trace", "--app", f.app.Slug, "--url", "http://" + f.host + path,
		"--method", method, "--client-ip", "127.0.0.1"}
	for _, h := range headers {
		args = append(args, "--header", h)
	}
	if body != "" {
		file := filepath.Join(t.TempDir(), "body.json")
		if err := os.WriteFile(file, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		args = append(args, "--body-file", file)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, cli, args...)
	command.Env = append(os.Environ(), "FAAS_API="+f.h.APIDURL, "FAAS_TOKEN="+f.key)
	output, err := command.Output()
	if err != nil {
		var stderr []byte
		if exitErr := (*exec.ExitError)(nil); errors.As(err, &exitErr) {
			stderr = exitErr.Stderr
		}
		t.Fatalf("gregale %s: %v\n%s%s", strings.Join(args, " "), err, output, stderr)
	}
	var result edgeruletrace.Result
	if err := json.Unmarshal(output, &result); err != nil {
		t.Fatalf("decode trace: %v\n%s", err, output)
	}
	return result.Simulation
}

// assertEdgeAnswerMatchesTrace compares the gateway's own answer with the
// trace's. A HEAD answer has no body, so its problem code is not compared.
func assertEdgeAnswerMatchesTrace(t *testing.T, method string, sim edgeruletrace.Simulation, hdr http.Header, body []byte, code int) {
	t.Helper()
	if code != sim.StatusCode {
		t.Fatalf("gateway answered %d, trace says %d (%s): %s", code, sim.StatusCode, sim.Outcome, body)
	}
	if sim.Location != "" && hdr.Get("Location") != sim.Location {
		t.Errorf("Location = %q, trace says %q", hdr.Get("Location"), sim.Location)
	}
	if sim.RetryAfterSeconds != 0 && hdr.Get("Retry-After") != strconv.Itoa(sim.RetryAfterSeconds) {
		t.Errorf("Retry-After = %q, trace says %d", hdr.Get("Retry-After"), sim.RetryAfterSeconds)
	}
	if method != http.MethodHead && sim.ProblemCode != "" && problemCode(body) != sim.ProblemCode {
		t.Errorf("problem code = %q, trace says %q: %s", problemCode(body), sim.ProblemCode, body)
	}
}
