// spec: §4.1.2 — edge-rule conditions use the inbound snapshot; only live
// enforcing rules tune traffic, and invalidation covers serving host selectors.
package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestHeaderConditionsKeepTheInboundSnapshot(t *testing.T) {
	r := httptest.NewRequest("GET", "http://api.example.com/private", nil)
	r.Header.Set("X-Access-Tier", "private")
	program, err := api.CompileEdgeRuleMatch(&api.EdgeRuleMatchExpr{Field: "header:x-access-tier", Op: "eq", Value: "private"})
	if err != nil {
		t.Fatal(err)
	}
	m := NewEdgeRuleMatchContext(r, nil, nil, nil)
	ctx := WithEdgeRuleMatchContext(WithEdgeRuleRequestHeaders(context.Background(), r.Header), m)
	rules := []EdgeRuleThrottleResolved{{EdgeRuleCondition: EdgeRuleCondition{Match: program}}}
	r.Header.Del("X-Access-Tier")
	if len(ApplicableEdgeRules(ctx, rules, func(r *EdgeRuleThrottleResolved) string { return r.AccountID }, r.URL.Path, r.Method)) != 1 {
		t.Fatal("request header edits changed the condition snapshot")
	}
	if EdgeRuleRequestHeaders(ctx).Get("X-Access-Tier") != "private" {
		t.Fatal("fixed selector snapshot changed")
	}
	m.requestID = "generated-id"
	if got := m.event("rule", "app", false).RequestID; got != "generated-id" {
		t.Fatalf("event lost generated request ID: %q", got)
	}
}

func TestCircuitTuningSkipsLogAndExpiredRules(t *testing.T) {
	rules := []EdgeRuleCircuitBreakerResolved{
		{EdgeRuleCondition: EdgeRuleCondition{LogOnly: true}, MinRequests: 1},
		{MinRequests: 2, ExpiresAt: time.Now().Add(-time.Hour)},
		{MinRequests: 20, FailureThreshold: 0.5},
	}
	if cfg, ok := circuitConfigFromRules(rules); !ok || cfg.MinRequests != 20 {
		t.Fatalf("inactive rule changed tuning: %+v %v", cfg, ok)
	}
	if _, ok := circuitConfigFromRules(rules[:2]); ok {
		t.Fatal("inactive rules should leave platform defaults")
	}
}

func TestCachedCircuitTuningStopsAtExpiry(t *testing.T) {
	var clock atomic.Int64
	clock.Store(time.Now().UnixNano())
	expires := time.Unix(0, clock.Load()).Add(time.Second)
	c := NewCircuitConfigs(func(context.Context, string) ([]EdgeRuleCircuitBreakerResolved, error) {
		return []EdgeRuleCircuitBreakerResolved{{MinRequests: 7, ExpiresAt: expires}}, nil
	}, nil)
	c.now = func() time.Time { return time.Unix(0, clock.Load()) }
	c.m["app"] = &circuitConfigEntry{}
	c.load("app")
	if cfg, ok := c.For("app"); !ok || cfg.MinRequests != 7 {
		t.Fatalf("live rule: %+v %v", cfg, ok)
	}
	clock.Add(int64(2 * time.Second))
	if _, ok := c.For("app"); ok {
		t.Fatal("cached tuning survived its expiration")
	}
}

func TestStoreWildcardPatternsInvalidateServingHosts(t *testing.T) {
	for _, pattern := range []string{"api_.example.com", "api%.example.com"} {
		inv := edgeRuleInvalidation{accountID: "account", appID: "app", patterns: []string{pattern}}
		if !inv.covers(EdgeRuleCacheKey("api1.example.com", "app"), "account", "") {
			t.Errorf("invalidation missed %q", pattern)
		}
	}
}

func TestIntegerParameterSpellingsRespectValidationMode(t *testing.T) {
	for _, mode := range []string{api.ValidateModeBlock, api.ValidateModeObserve, api.ValidateModeWarn} {
		for _, value := range []string{"01", "+1", "-01"} {
			t.Run(mode+"/"+value, func(t *testing.T) {
				v := &schemaValidator{}
				rule := &EdgeRuleValidateResolved{ID: "rule", ValidateMode: mode, Parameters: &EdgeRuleValidateParamsResolved{Query: paramSchema(t, v, `{"type":"object","properties":{"page":{"type":"integer","minimum":1}}}`)}}
				h := &Handler{validator: v}
				r := httptest.NewRequest(http.MethodGet, "http://example.test/?page="+url.QueryEscape(value), nil)
				rec := httptest.NewRecorder()
				blocked := h.validateEdgeRuleParameters(rec, r, &statusRecorder{ResponseWriter: rec}, rule)
				wantBlocked := value == "-01" && mode == api.ValidateModeBlock
				if blocked != wantBlocked || rec.Code == http.StatusInternalServerError {
					t.Fatalf("blocked=%v status=%d, expected blocked=%v: %s", blocked, rec.Code, wantBlocked, rec.Body)
				}
			})
		}
	}
}
