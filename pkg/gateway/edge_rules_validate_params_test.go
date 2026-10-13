package gateway

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/edgevalidate"
)

// schemaValidator is a Validator backed by real compiled schemas, keyed by
// digest like the production adapter.
type schemaValidator struct {
	schemas map[[32]byte]*edgevalidate.CompiledSchema
	calls   int
}

func (v *schemaValidator) add(t *testing.T, schema string) [32]byte {
	t.Helper()
	compiled, err := edgevalidate.Compile([]byte(schema), false)
	if err != nil {
		t.Fatalf("compile %s: %v", schema, err)
	}
	if v.schemas == nil {
		v.schemas = map[[32]byte]*edgevalidate.CompiledSchema{}
	}
	v.schemas[compiled.Digest] = compiled
	return compiled.Digest
}

func (v *schemaValidator) Validate(_ context.Context, req *EdgeValidateIn, rule *EdgeRuleValidateResolved) (*EdgeValidateResult, error) {
	v.calls++
	digest := rule.SchemaDigest
	if req.Digest != nil {
		digest = *req.Digest
	}
	fieldErr, err := v.schemas[digest].Validate(req.Body)
	if err != nil {
		return nil, err
	}
	if fieldErr != nil {
		return &EdgeValidateResult{FirstError: &EdgeValidateFieldError{Field: fieldErr.Field, Expected: fieldErr.Expected, Got: fieldErr.Got}}, nil
	}
	return &EdgeValidateResult{OK: true}, nil
}

func paramSchema(t *testing.T, v *schemaValidator, schema string) EdgeRuleParamSchemaResolved {
	t.Helper()
	kinds, err := api.EdgeRuleParamKinds(json.RawMessage(schema))
	if err != nil {
		t.Fatalf("kinds: %v", err)
	}
	return EdgeRuleParamSchemaResolved{Set: true, Digest: v.add(t, schema), Kinds: kinds}
}

func TestApplyEdgeRuleValidate_Parameters(t *testing.T) {
	v := &schemaValidator{}
	rule := &EdgeRuleValidateResolved{
		ID: "rule-params", AccountID: "acct-1", AppID: "app-1",
		ValidateMode: api.ValidateModeBlock, NoBodySchema: true,
		Parameters: &EdgeRuleValidateParamsResolved{
			PathTemplate: "/users/{id}/orders",
			Path:         paramSchema(t, v, `{"type":"object","properties":{"id":{"type":"integer","minimum":1}},"required":["id"]}`),
			Query:        paramSchema(t, v, `{"type":"object","properties":{"limit":{"type":"integer","maximum":100},"tag":{"type":"array","items":{"type":"string"}}},"additionalProperties":false}`),
			Headers:      paramSchema(t, v, `{"type":"object","properties":{"x-tenant":{"type":"string","pattern":"^t-"}},"required":["x-tenant"]}`),
		},
	}
	app := App{ID: "app-1", AccountID: "acct-1", Plan: api.PlanPro}
	for _, tc := range []struct {
		name, url, tenant string
		wantBlocked       bool
		wantField         string
	}{
		{name: "valid", url: "/users/7/orders?limit=10&tag=a&tag=b", tenant: "t-1"},
		{name: "path not an integer", url: "/users/abc/orders", tenant: "t-1", wantBlocked: true, wantField: "path/id"},
		{name: "path below minimum", url: "/users/0/orders", tenant: "t-1", wantBlocked: true, wantField: "path/id"},
		{name: "query over maximum", url: "/users/7/orders?limit=500", tenant: "t-1", wantBlocked: true, wantField: "query/limit"},
		{name: "unknown query parameter", url: "/users/7/orders?debug=1", tenant: "t-1", wantBlocked: true, wantField: "query"},
		{name: "missing header", url: "/users/7/orders", wantBlocked: true, wantField: "headers"},
		{name: "bad header", url: "/users/7/orders", tenant: "x-1", wantBlocked: true, wantField: "headers/x-tenant"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := &Handler{edgeRules: stubEdgeRuleMatcher{validate: rule}, validator: v, metrics: NewMetrics()}
			r := httptest.NewRequest(http.MethodGet, "http://h.example.com"+tc.url, nil)
			if tc.tenant != "" {
				r.Header.Set("X-Tenant", tc.tenant)
			}
			rec := httptest.NewRecorder()
			got := h.applyEdgeRuleValidate(rec, r, app, &statusRecorder{ResponseWriter: rec})
			if got != tc.wantBlocked {
				t.Fatalf("blocked = %v, want %v; body=%s", got, tc.wantBlocked, rec.Body.String())
			}
			if !tc.wantBlocked {
				return
			}
			var problem struct {
				Errors []api.FieldError `json:"errors"`
			}
			_ = json.Unmarshal(rec.Body.Bytes(), &problem)
			if rec.Code != http.StatusUnprocessableEntity || len(problem.Errors) != 1 || !strings.HasPrefix(problem.Errors[0].Field, tc.wantField) {
				t.Fatalf("status=%d errors=%+v, want 422 on %s", rec.Code, problem.Errors, tc.wantField)
			}
		})
	}
}

// Parameter checks run before the body is read; an observe-mode mismatch
// still lets the body check run.
func TestApplyEdgeRuleValidate_ParametersThenBody(t *testing.T) {
	v := &schemaValidator{}
	rule := &EdgeRuleValidateResolved{
		ID: "rule-both", AccountID: "acct-1", AppID: "app-1",
		ValidateMode: api.ValidateModeObserve,
		SchemaDigest: v.add(t, `{"type":"object","required":["name"]}`),
		Parameters: &EdgeRuleValidateParamsResolved{
			Query: paramSchema(t, v, `{"type":"object","properties":{"dry":{"type":"boolean"}}}`),
		},
	}
	h := &Handler{edgeRules: stubEdgeRuleMatcher{validate: rule}, validator: v, metrics: NewMetrics()}
	r := httptest.NewRequest(http.MethodPost, "http://h.example.com/items?dry=maybe", io.NopCloser(strings.NewReader(`{}`)))
	r.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	if h.applyEdgeRuleValidate(rec, r, App{ID: "app-1", AccountID: "acct-1", Plan: api.PlanPro}, &statusRecorder{ResponseWriter: rec}) {
		t.Fatal("observe mode rejected the request")
	}
	if v.calls != 2 {
		t.Fatalf("validator calls = %d, want 2 (query then body)", v.calls)
	}
	body, _ := io.ReadAll(r.Body)
	if string(body) != `{}` {
		t.Fatalf("body not restored for the proxy leg: %q", body)
	}
}
