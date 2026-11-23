package verifycontract

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

const sample = `version: 1
timeout_seconds: 20
checks:
  - name: health
    http:
      path: /healthz
  - name: products
    http:
      path: /api/products?limit=1
      headers: {Accept: application/json}
      expect:
        status: 200
        max_latency_ms: 800
        headers: {content-type: json}
        json:
          /items/0/id: 1
          /ok: true
  - name: create-order
    advisory: true
    http:
      method: post
      path: /api/orders
      json: {sku: abc, qty: 1}
      expect:
        status: [200, 201]
  - name: search-tool
    mcp:
      tool: search
      arguments: {query: shoes, filters: {size: 42}}
      expected_result: {count: 3}
  - name: orphan-orders
    sql:
      database: main
      query: SELECT count(*) FROM orders WHERE customer_id IS NULL
      expect: {value: "0"}
`

func TestParseYAMLRoundTripsThroughJSON(t *testing.T) {
	c, err := ParseYAML([]byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	if c.Timeout() != 20 || len(c.Checks) != 5 {
		t.Fatalf("timeout=%d checks=%d", c.Timeout(), len(c.Checks))
	}
	if got := c.Checks[1].HTTP.Expect.Status; len(got) != 1 || got[0] != 200 {
		t.Fatalf("single status = %v", got)
	}
	if !c.Checks[2].Mutating() || c.Checks[0].Mutating() || c.Checks[3].Mutating() || c.Checks[4].Mutating() {
		t.Fatal("only the POST check is mutating")
	}
	raw, sum, err := c.Canonical()
	if err != nil || len(sum) != 64 {
		t.Fatalf("canonical: %v %q", err, sum)
	}
	again, err := ParseJSON(raw)
	if err != nil {
		t.Fatalf("json form does not validate: %v\n%s", err, raw)
	}
	raw2, sum2, _ := again.Canonical()
	if sum2 != sum {
		t.Fatalf("canonical form not stable:\n%s\n%s", raw, raw2)
	}
}

func TestValidateRefusesUnsafeOrAmbiguousContracts(t *testing.T) {
	cases := map[string]string{
		"version":          "version: 2\nchecks: [{name: a, http: {path: /}}]",
		"no checks":        "version: 1\nchecks: []",
		"unknown field":    "version: 1\nchecks: [{name: a, http: {path: /, verb: GET}}]",
		"two kinds":        "version: 1\nchecks: [{name: a, http: {path: /}, sql: {database: main, query: select 1, expect: {rows: 1}}}]",
		"dup names":        "version: 1\nchecks: [{name: a, http: {path: /}}, {name: a, http: {path: /x}}]",
		"bad name":         "version: 1\nchecks: [{name: A B, http: {path: /}}]",
		"relative path":    "version: 1\nchecks: [{name: a, http: {path: healthz}}]",
		"absolute url":     "version: 1\nchecks: [{name: a, http: {path: //evil.example/x}}]",
		"host header":      "version: 1\nchecks: [{name: a, http: {path: /, headers: {Host: x}}}]",
		"platform header":  "version: 1\nchecks: [{name: a, http: {path: /, headers: {X-Faas-Platform-Smoke-Token: x}}}]",
		"get body":         "version: 1\nchecks: [{name: a, http: {path: /, body: x}}]",
		"body and json":    "version: 1\nchecks: [{name: a, http: {method: POST, path: /, body: x, json: {a: 1}}}]",
		"bad method":       "version: 1\nchecks: [{name: a, http: {method: TRACE, path: /}}]",
		"bad status":       "version: 1\nchecks: [{name: a, http: {path: /, expect: {status: 700}}}]",
		"bad pointer":      "version: 1\nchecks: [{name: a, http: {path: /, expect: {json: {items: 1}}}}]",
		"timeout":          "version: 1\ntimeout_seconds: 600\nchecks: [{name: a, http: {path: /}}]",
		"mcp two targets":  "version: 1\nchecks: [{name: a, mcp: {tool: x, prompt: y, expected_result: 1}}]",
		"mcp tool no exp":  "version: 1\nchecks: [{name: a, mcp: {tool: x}}]",
		"sql no expect":    "version: 1\nchecks: [{name: a, sql: {database: main, query: select 1}}]",
		"sql bad database": "version: 1\nchecks: [{name: a, sql: {database: 'Main DB', query: select 1, expect: {rows: 1}}}]",
		"two documents":    "version: 1\nchecks: [{name: a, http: {path: /}}]\n---\nversion: 1",
	}
	for name, doc := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseYAML([]byte(doc)); !errors.Is(err, ErrInvalid) {
				t.Fatalf("want ErrInvalid, got %v", err)
			}
		})
	}
}

func TestEvaluateHTTP(t *testing.T) {
	c, err := ParseYAML([]byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	expect := c.Checks[1].HTTP.Expect
	ok := HTTPResponse{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}},
		Body: []byte(`{"ok":true,"items":[{"id":1.0,"name":"secret-name"}]}`), Latency: 100 * time.Millisecond}
	if f := EvaluateHTTP(expect, ok); len(f) != 0 {
		t.Fatalf("expected pass, got %v", f)
	}
	bad := HTTPResponse{StatusCode: 500, Header: http.Header{}, Body: []byte(`{"ok":false,"items":[{"id":"secret-name"}]}`), Latency: 2 * time.Second}
	f := EvaluateHTTP(expect, bad)
	joined := strings.Join(f, "; ")
	for _, want := range []string{"status 500", "latency 2000 ms", "Content-Type is missing", `"/items/0/id" is a different string`, `"/ok" is false`} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q in %q", want, joined)
		}
	}
	if strings.Contains(joined, "secret-name") {
		t.Fatalf("failures quote response text: %q", joined)
	}
	if f := EvaluateHTTP(HTTPExpect{}, HTTPResponse{StatusCode: 204}); len(f) != 0 {
		t.Fatalf("default expectation is 2xx: %v", f)
	}
	if f := EvaluateHTTP(HTTPExpect{JSON: map[string]any{"": 1}}, HTTPResponse{StatusCode: 200, Body: []byte("{"), BodyTruncated: true}); len(f) != 1 || !strings.Contains(f[0], "exceeds") {
		t.Fatalf("truncated body: %v", f)
	}
}

func TestEvaluateSQL(t *testing.T) {
	zero, two, name := "0", 2, "alice"
	if f := EvaluateSQL(SQLExpect{Value: &zero}, 1, true, &zero); len(f) != 0 {
		t.Fatal(f)
	}
	three := "3"
	if f := EvaluateSQL(SQLExpect{Value: &zero, Rows: &two}, 1, true, &three); len(f) != 2 || !strings.Contains(f[1], "is 3") {
		t.Fatal(f)
	}
	if f := EvaluateSQL(SQLExpect{Value: &zero}, 1, true, &name); len(f) != 1 || strings.Contains(f[0], "alice") {
		t.Fatalf("text values must not be quoted: %v", f)
	}
	if f := EvaluateSQL(SQLExpect{Null: true}, 0, false, nil); len(f) != 1 {
		t.Fatal(f)
	}
}

type fakeExec struct {
	http    func(HTTPCheck) (HTTPResponse, error)
	mcp     func(MCPCheck, bool) error
	sql     func(SQLCheck) (SQLResult, error)
	httpRan []string
}

func (f *fakeExec) HTTP(_ context.Context, c HTTPCheck) (HTTPResponse, error) {
	f.httpRan = append(f.httpRan, c.EffectiveMethod()+" "+c.Path)
	return f.http(c)
}
func (f *fakeExec) MCP(_ context.Context, c MCPCheck, allow bool) error { return f.mcp(c, allow) }
func (f *fakeExec) SQL(_ context.Context, _ string, c SQLCheck) (SQLResult, error) {
	return f.sql(c)
}

func TestRunSkipsMutationsInProductionAndBlocksOnRequiredFailures(t *testing.T) {
	c, err := ParseYAML([]byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	zero := "0"
	var mcpAllowed []bool
	exec := &fakeExec{
		http: func(h HTTPCheck) (HTTPResponse, error) {
			return HTTPResponse{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: []byte(`{"ok":true,"items":[{"id":1}]}`)}, nil
		},
		mcp: func(_ MCPCheck, allow bool) error { mcpAllowed = append(mcpAllowed, allow); return nil },
		sql: func(SQLCheck) (SQLResult, error) { return SQLResult{RowCount: 1, HasFirst: true, First: &zero}, nil },
	}
	report := Run(context.Background(), c, exec, RunOptions{Production: true, ContractSHA256: "abc"})
	if report.Status != StatusPassed || report.Environment != EnvironmentProduction {
		t.Fatalf("report = %+v", report)
	}
	if report.Checks[2].Status != StatusSkipped || report.Checks[2].Code != CodeMutationInProduction {
		t.Fatalf("POST check in production = %+v", report.Checks[2])
	}
	for _, ran := range exec.httpRan {
		if strings.HasPrefix(ran, "POST") {
			t.Fatal("mutating check was sent in production")
		}
	}
	if len(mcpAllowed) != 1 || mcpAllowed[0] {
		t.Fatalf("MCP tools must be read-only in production: %v", mcpAllowed)
	}

	exec.sql = func(SQLCheck) (SQLResult, error) {
		return SQLResult{}, &CheckError{Code: CodeDatabaseNotFound, Message: "no managed Postgres database named main"}
	}
	report = Run(context.Background(), c, exec, RunOptions{})
	if report.Status != StatusFailed || len(report.Blocking()) != 1 || report.Blocking()[0] != "orphan-orders" {
		t.Fatalf("report = %+v", report)
	}
	if report.Checks[4].Status != StatusError || report.Checks[4].Code != CodeDatabaseNotFound {
		t.Fatalf("sql check = %+v", report.Checks[4])
	}
	if len(exec.httpRan) != 5 {
		t.Fatalf("non-production run should send the POST: %v", exec.httpRan)
	}
}

func TestRunAdvisoryFailureDoesNotBlock(t *testing.T) {
	c, err := ParseYAML([]byte("version: 1\nchecks:\n  - {name: soft, advisory: true, http: {path: /slow}}\n"))
	if err != nil {
		t.Fatal(err)
	}
	exec := &fakeExec{http: func(HTTPCheck) (HTTPResponse, error) { return HTTPResponse{StatusCode: 503}, nil }}
	report := Run(context.Background(), c, exec, RunOptions{})
	if report.Status != StatusPassed || report.Checks[0].Status != StatusFailed || report.Checks[0].HTTPStatus != 503 {
		t.Fatalf("report = %+v", report)
	}
}

func TestRunBudgetExpiryErrorsRemainingChecks(t *testing.T) {
	c, err := ParseYAML([]byte("version: 1\ntimeout_seconds: 1\nchecks:\n  - {name: a, http: {path: /a}}\n  - {name: b, http: {path: /b}}\n"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	exec := &fakeExec{http: func(HTTPCheck) (HTTPResponse, error) { cancel(); return HTTPResponse{}, context.Canceled }}
	report := Run(ctx, c, exec, RunOptions{})
	if report.Status != StatusFailed || report.Checks[0].Code != CodeTimeout || report.Checks[1].Code != CodeTimeout {
		t.Fatalf("report = %+v", report)
	}
}
