// spec: §4.1

package gateway

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// TestEdgeRuleResponseHeaderOps_Accumulate — kind=headers, kind=cors and
// validate-warn each install response header ops on the same request's
// statusRecorder. installHeaderOps used to assign, so the last rule to fire
// discarded the others: a matched CORS rule dropped every kind=headers op
// (e.g. Strict-Transport-Security), and validate-warn dropped
// Access-Control-Allow-Origin, so browsers blocked the response.
func TestEdgeRuleResponseHeaderOps_Accumulate(t *testing.T) {
	app := App{ID: "app-1", AccountID: "acct-1", Plan: api.PlanPro}
	headers := &EdgeRuleHeadersResolved{
		ID: "rule-headers", AccountID: "acct-1", AppID: "app-1",
		ResponseHeaders: []EdgeRuleHeaderOp{{Action: "set", Name: "Strict-Transport-Security", Value: "max-age=63072000"}},
	}
	cors := &EdgeRuleCORSResolved{
		ID: "rule-cors", AccountID: "acct-1", AppID: "app-1",
		AllowOrigins: []string{"https://app.example.com"}, AllowMethods: []string{"POST"},
	}
	validate := &EdgeRuleValidateResolved{
		ID: "rule-validate", AccountID: "acct-1", AppID: "app-1",
		ValidateMode: api.ValidateModeWarn,
	}
	cases := []struct {
		name    string
		matcher stubEdgeRuleMatcher
		want    map[string]string
	}{
		{
			name:    "headers then cors",
			matcher: stubEdgeRuleMatcher{headers: headers, cors: cors},
			want: map[string]string{
				"Strict-Transport-Security":   "max-age=63072000",
				"Access-Control-Allow-Origin": "https://app.example.com",
			},
		},
		{
			name:    "cors then validate warn",
			matcher: stubEdgeRuleMatcher{cors: cors, validate: validate},
			want: map[string]string{
				"Access-Control-Allow-Origin": "https://app.example.com",
				"X-Validation-Warning":        "rule-validate",
			},
		},
		{
			name:    "all three",
			matcher: stubEdgeRuleMatcher{headers: headers, cors: cors, validate: validate},
			want: map[string]string{
				"Strict-Transport-Security":   "max-age=63072000",
				"Access-Control-Allow-Origin": "https://app.example.com",
				"X-Validation-Warning":        "rule-validate",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := &Handler{
				edgeRules:     tc.matcher,
				validator:     &stubValidatorApply{},
				edgeRuleAudit: &countingAudit{},
				metrics:       NewMetrics(),
			}
			rec := httptest.NewRecorder()
			srec := &statusRecorder{ResponseWriter: rec}
			r := httptest.NewRequest(http.MethodPost, "http://h.example.com/api/x",
				io.NopCloser(strings.NewReader(`{"name": 123}`)))
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("Origin", "https://app.example.com")

			// Pipeline order (handler.go): headers, cors, validate.
			h.applyEdgeRuleHeaders(rec, r, app, srec)
			if h.applyEdgeRuleCORS(rec, r, app, srec) {
				t.Fatal("CORS short-circuited a non-preflight request")
			}
			if h.applyEdgeRuleValidate(rec, r, app, srec) {
				t.Fatal("validate warn short-circuited the request")
			}
			srec.WriteHeader(http.StatusOK)

			for name, want := range tc.want {
				if got := rec.Header().Get(name); got != want {
					t.Errorf("%s = %q, want %q (headerOps=%v)", name, got, want, srec.headerOps)
				}
			}
		})
	}
}
