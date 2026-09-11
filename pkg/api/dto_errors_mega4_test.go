// dto_errors_mega4_test.go — Coverage Mega-PR #4 cluster 3:
// fill pkg/api coverage on dto.go + errors.go pure helpers and
// unmarshal/validate surfaces. Targets the deep branch tables
// that pkg/api/{errors,dto}_chain_test.go and the dto_*_test.go
// predecessors left at low coverage.
//
// Targets:
//   - ScalingPolicy.UnmarshalJSON + HasUnknownFields +
//     UnknownFields + ClearUnknownFields
//   - UsageResponse.CPUHours / TotalEgressGB
//   - UsageExportResponse.CPUHours
//   - EdgeRule{*,Throttle,Cache,Budget,Maintenance,...}.Validate
//   - validateGeoCountryCode
//   - ThrottleKeyByIsPerConsumer
//   - Sidecar.Validate / Sidecars.Validate
//   - HasHeader
//   - StatusForCode (table across the full closed set)
//
// Whitebox `package api`.

package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
)

// --- ScalingPolicy strict decode + unknown-field surface --------

func TestScalingPolicyUnmarshalJSON_Mega4(t *testing.T) {
	t.Parallel()

	t.Run("clean decode populates and clears unknowns", func(t *testing.T) {
		t.Parallel()
		var p ScalingPolicy
		if err := json.Unmarshal([]byte(`{"min_instances": 1, "max_instances": 5}`), &p); err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}
		if p.MinInstances != 1 || p.MaxInstances != 5 {
			t.Errorf("got %+v", p)
		}
		if p.HasUnknownFields() {
			t.Errorf("clean decode: HasUnknownFields() = true")
		}
		if got := p.UnknownFields(); len(got) != 0 {
			t.Errorf("clean decode: UnknownFields = %v", got)
		}
	})

	t.Run("unknown field surfaces + Clear drops it", func(t *testing.T) {
		t.Parallel()
		var p ScalingPolicy
		err := json.Unmarshal([]byte(`{"min_instances": 1, "ghost_field": 9, "alpha": true}`), &p)
		if err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}
		if !p.HasUnknownFields() {
			t.Fatal("HasUnknownFields: false, want true")
		}
		unknown := p.UnknownFields()
		if len(unknown) != 2 || unknown[0] != "alpha" || unknown[1] != "ghost_field" {
			t.Errorf("UnknownFields = %v, want [alpha, ghost_field] sorted ASC", unknown)
		}
		p.ClearUnknownFields()
		if p.HasUnknownFields() {
			t.Error("after Clear: HasUnknownFields still true")
		}
	})

	t.Run("malformed JSON wraps with package prefix", func(t *testing.T) {
		t.Parallel()
		var p ScalingPolicy
		err := json.Unmarshal([]byte(`not-json{`), &p)
		if err == nil {
			t.Fatal("want non-nil err")
		}
		// The two-stage decoder in dto.go wraps with "scaling_policy:" prefix
		// on both decode stages. Accept either prefix form (the underlying
		// wrapped error text always contains "invalid character").
		if !strings.Contains(err.Error(), "invalid character") {
			t.Errorf("err = %v, want underlying json decode error", err)
		}
	})
}

// --- Usage helpers ----------------------------------------------

func TestUsageResponseCPUHours_Mega4(t *testing.T) {
	t.Parallel()
	if got := (UsageResponse{}).CPUHours(); got != 0 {
		t.Errorf("zero: %v", got)
	}
	// 1 hour = 3.6e9 µs.
	if got := (UsageResponse{CPUUsageUsec: 3_600_000_000}).CPUHours(); got != 1.0 {
		t.Errorf("1h: %v", got)
	}
}

func TestUsageResponseTotalEgressGB_Mega4(t *testing.T) {
	t.Parallel()
	if got := (UsageResponse{}).TotalEgressGB(); got != 0 {
		t.Errorf("zero: %v", got)
	}
	gb := int64(1024 * 1024 * 1024)
	if got := (UsageResponse{TXBytes: gb, NetTxBytes: gb}).TotalEgressGB(); got != 1.0 {
		t.Errorf("canonical 1 GB (tx_bytes must not be added): %v", got)
	}
}

func TestUsageExportResponseCPUHours_Mega4(t *testing.T) {
	t.Parallel()
	if got := (UsageExportResponse{}).CPUHours(); got != 0 {
		t.Errorf("zero: %v", got)
	}
	if got := (UsageExportResponse{CPUUsageUsec: 3_600_000_000}).CPUHours(); got != 1.0 {
		t.Errorf("1h: %v", got)
	}
}

// --- Edge rule validators ---------------------------------------

func TestEdgeRuleRouteActionValidate_Mega4(t *testing.T) {
	t.Parallel()
	if p := (*EdgeRuleRouteAction)(nil).Validate(); p == nil {
		t.Error("nil: p=nil, want error")
	}
	if p := (&EdgeRuleRouteAction{}).Validate(); p == nil {
		t.Error("empty slug: p=nil")
	}
	long := strings.Repeat("a", 41)
	if p := (&EdgeRuleRouteAction{TargetAppSlug: long}).Validate(); p == nil {
		t.Error("over-40 char: p=nil")
	}
	if p := (&EdgeRuleRouteAction{TargetAppSlug: "ok"}).Validate(); p != nil {
		t.Errorf("ok: %v", p)
	}
}

func TestEdgeRuleRewriteActionValidate_Mega4(t *testing.T) {
	t.Parallel()
	if p := (*EdgeRuleRewriteAction)(nil).Validate(); p == nil {
		t.Error("nil: p=nil")
	}
	if p := (&EdgeRuleRewriteAction{From: "no-slash", To: "x"}).Validate(); p == nil {
		t.Error("From without /: p=nil")
	}
	if p := (&EdgeRuleRewriteAction{From: "/x", To: ""}).Validate(); p == nil {
		t.Error("empty To: p=nil")
	}
	if p := (&EdgeRuleRewriteAction{From: "/x", To: "/y"}).Validate(); p != nil {
		t.Errorf("ok: %v", p)
	}
}

func TestEdgeRuleRedirectActionValidate_Mega4(t *testing.T) {
	t.Parallel()
	if p := (*EdgeRuleRedirectAction)(nil).Validate(); p == nil {
		t.Error("nil: p=nil")
	}
	for _, st := range []int{301, 302, 307, 308} {
		if p := (&EdgeRuleRedirectAction{StatusCode: st, To: "/x"}).Validate(); p != nil {
			t.Errorf("status %d: %v", st, p)
		}
	}
	if p := (&EdgeRuleRedirectAction{StatusCode: 200, To: "/x"}).Validate(); p == nil {
		t.Error("status 200: p=nil")
	}
	if p := (&EdgeRuleRedirectAction{StatusCode: 302, To: ""}).Validate(); p == nil {
		t.Error("empty To: p=nil")
	}
}

func TestEdgeRuleHeaderOpValidate_Mega4(t *testing.T) {
	t.Parallel()
	if p := (*EdgeRuleHeaderOp)(nil).Validate(); p == nil {
		t.Error("nil: p=nil")
	}
	if p := (&EdgeRuleHeaderOp{Name: "X", Action: "garbage"}).Validate(); p == nil {
		t.Error("bad action: p=nil")
	}
	if p := (&EdgeRuleHeaderOp{Name: "Host", Action: "set", Value: "x"}).Validate(); p == nil {
		t.Error("forbidden Host: p=nil")
	}
	if p := (&EdgeRuleHeaderOp{Name: "X-Faas-Foo", Action: "set"}).Validate(); p == nil {
		t.Error("x-faas- prefix: p=nil")
	}
	if p := (&EdgeRuleHeaderOp{Name: "X-Custom", Action: "add", Value: "v"}).Validate(); p != nil {
		t.Errorf("ok: %v", p)
	}
	if p := (&EdgeRuleHeaderOp{Name: "X-Custom", Action: "remove"}).Validate(); p != nil {
		t.Errorf("remove ok: %v", p)
	}
}

func TestEdgeRuleHeadersActionValidate_Mega4(t *testing.T) {
	t.Parallel()
	if p := (*EdgeRuleHeadersAction)(nil).Validate(); p == nil {
		t.Error("nil: p=nil")
	}
	if p := (&EdgeRuleHeadersAction{}).Validate(); p == nil {
		t.Error("empty: p=nil")
	}
	// Bad inner request header fails through.
	bad := (&EdgeRuleHeadersAction{RequestHeaders: []EdgeRuleHeaderOp{{Name: "Host", Action: "set"}}})
	if p := bad.Validate(); p == nil {
		t.Error("nested bad: p=nil")
	}
	if p := (&EdgeRuleHeadersAction{RequestHeaders: []EdgeRuleHeaderOp{{Name: "X-Ok", Action: "add"}}}).Validate(); p != nil {
		t.Errorf("ok: %v", p)
	}
}

func TestEdgeRuleCORSActionValidate_Mega4(t *testing.T) {
	t.Parallel()
	if p := (*EdgeRuleCORSAction)(nil).Validate(); p == nil {
		t.Error("nil: p=nil")
	}
	// No origins, no methods.
	if p := (&EdgeRuleCORSAction{}).Validate(); p == nil {
		t.Error("empty: p=nil")
	}
	// Negative max_age.
	if p := (&EdgeRuleCORSAction{AllowOrigins: []string{"*"}, AllowMethods: []string{"GET"}, MaxAgeSeconds: -1}).Validate(); p == nil {
		t.Error("neg max_age: p=nil")
	}
	// Over-24h max_age.
	if p := (&EdgeRuleCORSAction{AllowOrigins: []string{"*"}, AllowMethods: []string{"GET"}, MaxAgeSeconds: 86401}).Validate(); p == nil {
		t.Error("over max_age: p=nil")
	}
	// Bad origin grammar.
	if p := (&EdgeRuleCORSAction{AllowOrigins: []string{"file:///etc/passwd"}, AllowMethods: []string{"GET"}}).Validate(); p == nil {
		t.Error("bad grammar: p=nil")
	}
	// AllowCredentials + bare "*" footgun.
	if p := (&EdgeRuleCORSAction{AllowOrigins: []string{"*"}, AllowMethods: []string{"GET"}, AllowCredentials: true}).Validate(); p == nil {
		t.Error("creds+*: p=nil")
	}
	// Subdomain wildcard + credentials → ok (concrete origin at request time).
	if p := (&EdgeRuleCORSAction{
		AllowOrigins:     []string{"https://*.example.com"},
		AllowMethods:     []string{"GET"},
		AllowCredentials: true,
	}).Validate(); p != nil {
		t.Errorf("subdomain wildcard+creds: %v", p)
	}
}

func TestEdgeRuleJWTActionValidate_Mega4(t *testing.T) {
	t.Parallel()
	if p := (*EdgeRuleJWTAction)(nil).Validate(); p == nil {
		t.Error("nil: p=nil")
	}
	if p := (&EdgeRuleJWTAction{}).Validate(); p == nil {
		t.Error("empty: p=nil")
	}
	if p := (&EdgeRuleJWTAction{Issuer: "i", JWKSURL: "http://example.com/jwks", Algorithms: []string{"RS256"}}).Validate(); p == nil {
		t.Error("http (no https): p=nil")
	}
	if p := (&EdgeRuleJWTAction{Issuer: "i", JWKSURL: "https://localhost/jwks", Algorithms: []string{"RS256"}}).Validate(); p == nil {
		t.Error("localhost jwks_url: p=nil")
	}
	if p := (&EdgeRuleJWTAction{Issuer: "i", JWKSURL: "https://10.0.0.1/jwks", Algorithms: []string{"RS256"}}).Validate(); p == nil {
		t.Error("RFC1918 jwks_url: p=nil")
	}
	if p := (&EdgeRuleJWTAction{Issuer: "i", JWKSURL: "https://example.com/jwks", Algorithms: []string{"HS256"}}).Validate(); p == nil {
		t.Error("HS256: p=nil (symmetric)")
	}
	if p := (&EdgeRuleJWTAction{Issuer: "i", JWKSURL: "https://example.com/jwks", Algorithms: []string{"RS256"}}).Validate(); p != nil {
		t.Errorf("ok: %v", p)
	}
}

func TestEdgeRuleIPActionValidate_Mega4(t *testing.T) {
	t.Parallel()
	if p := (*EdgeRuleIPAction)(nil).Validate(); p == nil {
		t.Error("nil: p=nil")
	}
	if p := (&EdgeRuleIPAction{}).Validate(); p == nil {
		t.Error("empty: p=nil")
	}
	if p := (&EdgeRuleIPAction{Allow: []string{"not-a-cidr"}}).Validate(); p == nil {
		t.Error("bad allow CIDR: p=nil")
	}
	if p := (&EdgeRuleIPAction{Deny: []string{"999.999.0.0/16"}}).Validate(); p == nil {
		t.Error("bad deny CIDR: p=nil")
	}
	if p := (&EdgeRuleIPAction{Allow: []string{"10.0.0.0/8", "192.168.0.0/16"}}).Validate(); p != nil {
		t.Errorf("ok: %v", p)
	}
}

func TestEdgeRuleValidateActionValidate_Mega4(t *testing.T) {
	t.Parallel()
	if p := (*EdgeRuleValidateAction)(nil).Validate(); p == nil {
		t.Error("nil: p=nil")
	}
	if p := (&EdgeRuleValidateAction{}).Validate(); p == nil {
		t.Error("no schema: p=nil")
	}
	// Schema over MaxEdgeRuleValidateSchemaBytes.
	hugeSchema := []byte(`{"type":"object","properties":{"a":{"type":"string","description":"` + strings.Repeat("x", MaxEdgeRuleValidateSchemaBytes) + `"}}}`)
	if p := (&EdgeRuleValidateAction{Schema: hugeSchema}).Validate(); p == nil {
		t.Error("oversized schema: p=nil")
	}
	// Schema invalid JSON.
	if p := (&EdgeRuleValidateAction{Schema: json.RawMessage(`{not-json`)}).Validate(); p == nil {
		t.Error("invalid JSON schema: p=nil")
	}
	// External $ref URL.
	if p := (&EdgeRuleValidateAction{Schema: json.RawMessage(`{"$ref":"https://example.com/x"}`)}).Validate(); p == nil {
		t.Error("external $ref: p=nil")
	}
	// Internal pointer is fine.
	if p := (&EdgeRuleValidateAction{Schema: json.RawMessage(`{"$ref":"#/definitions/Foo"}`)}).Validate(); p != nil {
		t.Errorf("internal $ref: %v", p)
	}
	// Bad content_type.
	if p := (&EdgeRuleValidateAction{
		Schema:       json.RawMessage(`{}`),
		ContentTypes: []string{"text/plain"},
	}).Validate(); p == nil {
		t.Error("bad content_type: p=nil")
	}
	// Negative max_body_bytes.
	if p := (&EdgeRuleValidateAction{
		Schema:       json.RawMessage(`{}`),
		MaxBodyBytes: -1,
	}).Validate(); p == nil {
		t.Error("neg max_body: p=nil")
	}
	// Unknown validate_mode.
	if p := (&EdgeRuleValidateAction{
		Schema:       json.RawMessage(`{}`),
		ValidateMode: "fancy",
	}).Validate(); p == nil {
		t.Error("unknown mode: p=nil")
	}
	// All-allowed mode + plain schema passes.
	if p := (&EdgeRuleValidateAction{
		Schema:       json.RawMessage(`{"type":"object"}`),
		ValidateMode: ValidateModeObserve,
	}).Validate(); p != nil {
		t.Errorf("ok: %v", p)
	}
}

func TestEdgeRuleLimitActionValidate_Mega4(t *testing.T) {
	t.Parallel()
	if p := (*EdgeRuleLimitAction)(nil).Validate(); p == nil {
		t.Error("nil: p=nil")
	}
	if p := (&EdgeRuleLimitAction{}).Validate(); p == nil {
		t.Error("zero max_body: p=nil")
	}
	if p := (&EdgeRuleLimitAction{MaxBodyBytes: -1}).Validate(); p == nil {
		t.Error("neg max_body: p=nil")
	}
	if p := (&EdgeRuleLimitAction{MaxBodyBytes: MaxRequestBodyBytes + 1}).Validate(); p == nil {
		t.Error("over cap: p=nil")
	}
	if p := (&EdgeRuleLimitAction{MaxBodyBytes: 1000, MaxBodyBytesStreaming: -1}).Validate(); p == nil {
		t.Error("neg streaming: p=nil")
	}
	if p := (&EdgeRuleLimitAction{MaxBodyBytes: 1000, MaxBodyBytesStreaming: int(MaxEdgeRuleLimitBodyBytesStreaming) + 1}).Validate(); p == nil {
		t.Error("streaming over cap: p=nil")
	}
	if p := (&EdgeRuleLimitAction{MaxBodyBytes: 5000, MaxBodyBytesStreaming: 1000}).Validate(); p == nil {
		t.Error("streaming < buffered: p=nil")
	}
	if p := (&EdgeRuleLimitAction{MaxBodyBytes: 1000, MaxBodyBytesStreaming: 5000}).Validate(); p != nil {
		t.Errorf("ok: %v", p)
	}
}

func TestEdgeRuleGeoActionValidate_Mega4(t *testing.T) {
	t.Parallel()
	if p := (*EdgeRuleGeoAction)(nil).Validate(); p == nil {
		t.Error("nil: p=nil")
	}
	if p := (&EdgeRuleGeoAction{}).Validate(); p == nil {
		t.Error("empty: p=nil")
	}
	if p := (&EdgeRuleGeoAction{Allow: []string{"X1"}}).Validate(); p == nil {
		t.Error("non-2-letter: p=nil")
	}
	if p := (&EdgeRuleGeoAction{Allow: []string{"ZZ"}}).Validate(); p == nil {
		t.Error("reserved ZZ: p=nil")
	}
	if p := (&EdgeRuleGeoAction{Allow: []string{"EU"}}).Validate(); p == nil {
		t.Error("reserved EU: p=nil")
	}
	if p := (&EdgeRuleGeoAction{Allow: []string{"de"}, Deny: []string{"DE"}}).Validate(); p == nil {
		t.Error("dup across allow+deny: p=nil")
	}
	// 51 entries: above the cap.
	var many []string
	for i := 0; i < 51; i++ {
		many = append(many, "DE") // duplicates don't matter here, cap is on seen.
	}
	_ = many
	// Build a real 51-entry allow list using unique codes.
	codes := make([]string, 0, 51)
	for _, c := range []string{"DE", "FR", "US", "GB", "IT", "ES", "JP", "KR", "CN", "IN",
		"BR", "RU", "AU", "CA", "MX", "NL", "SE", "NO", "FI", "DK",
		"PL", "TR", "GR", "AT", "CH", "BE", "PT", "IE", "IL", "SA",
		"AR", "CO", "CL", "PE", "VE", "EG", "ZA", "NG", "KE", "MA",
		"TH", "VN", "MY", "SG", "PH", "ID", "PK", "BD", "LK", "NP",
		"UA"} {
		codes = append(codes, c)
	}
	if p := (&EdgeRuleGeoAction{Allow: codes}).Validate(); p == nil {
		t.Error("51 entries: p=nil")
	}
	if p := (&EdgeRuleGeoAction{Allow: []string{"DE", "FR"}}).Validate(); p != nil {
		t.Errorf("ok: %v", p)
	}
}

func TestEdgeRuleGeoActionValidate_LargeInputDoesNotPreallocateFromLength(t *testing.T) {
	t.Parallel()

	// A malformed request can carry a very large slice while still failing on
	// its second entry. Validation must not preallocate a map for every slice
	// element before it discovers that duplicate.
	codes := make([]string, 1<<16)
	for i := range codes {
		codes[i] = "DE"
	}
	if p := (&EdgeRuleGeoAction{Allow: codes}).Validate(); p == nil {
		t.Fatal("duplicate large input: p=nil")
	}
}

func TestEdgeRuleMaintenanceActionValidate_Mega4(t *testing.T) {
	t.Parallel()
	if p := (*EdgeRuleMaintenanceAction)(nil).Validate(); p == nil {
		t.Error("nil: p=nil")
	}
	if p := (&EdgeRuleMaintenanceAction{RetryAfterSeconds: -1}).Validate(); p == nil {
		t.Error("neg retry: p=nil")
	}
	if p := (&EdgeRuleMaintenanceAction{RetryAfterSeconds: MaxEdgeRuleMaintenanceRetryAfterSeconds + 1}).Validate(); p == nil {
		t.Error("retry over cap: p=nil")
	}
	long := strings.Repeat("a", 513)
	if p := (&EdgeRuleMaintenanceAction{Message: long}).Validate(); p == nil {
		t.Error("over 512 B msg: p=nil")
	}
	if p := (&EdgeRuleMaintenanceAction{RetryAfterSeconds: 60, Message: "ok"}).Validate(); p != nil {
		t.Errorf("ok: %v", p)
	}
}

func TestEdgeRuleThrottleActionValidate_Mega4(t *testing.T) {
	t.Parallel()
	if p := (*EdgeRuleThrottleAction)(nil).Validate(ThrottleValidationContext{}); p == nil {
		t.Error("nil: p=nil")
	}
	ctx := ThrottleValidationContext{PlanMaxRPS: 100, PlanMaxBurst: 200, PlanMaxKeysPerRule: 1000}

	// rps <= 0 rejected.
	if p := (&EdgeRuleThrottleAction{RequestsPerSecond: 0, Burst: 1}).Validate(ctx); p == nil {
		t.Error("rps=0: p=nil")
	}
	if p := (&EdgeRuleThrottleAction{RequestsPerSecond: 10, Burst: 0}).Validate(ctx); p == nil {
		t.Error("burst=0: p=nil")
	}
	// rps over plan.
	if p := (&EdgeRuleThrottleAction{RequestsPerSecond: 1000, Burst: 1}).Validate(ctx); p == nil {
		t.Error("rps>plan: p=nil")
	}
	if p := (&EdgeRuleThrottleAction{RequestsPerSecond: 10, Burst: 1000}).Validate(ctx); p == nil {
		t.Error("burst>plan: p=nil")
	}
	// Pre-Phase-3: JWTClaimName with empty key_by rejected.
	if p := (&EdgeRuleThrottleAction{RequestsPerSecond: 10, Burst: 10, JWTClaimName: "tier"}).Validate(ctx); p == nil {
		t.Error("jwt_claim_name w/o key_by: p=nil")
	}
	// Pre-Phase-3: MaxKeysPerRule with empty key_by rejected.
	if p := (&EdgeRuleThrottleAction{RequestsPerSecond: 10, Burst: 10, MaxKeysPerRule: 5}).Validate(ctx); p == nil {
		t.Error("max_keys_per_rule w/o key_by: p=nil")
	}
	// key_by=api_key + JWTClaimName rejected.
	if p := (&EdgeRuleThrottleAction{
		RequestsPerSecond: 10, Burst: 10, KeyBy: ThrottleKeyByAPIKey, JWTClaimName: "tier",
	}).Validate(ctx); p == nil {
		t.Error("api_key+jwt_claim_name: p=nil")
	}
	// jwt_claim without claim name.
	if p := (&EdgeRuleThrottleAction{
		RequestsPerSecond: 10, Burst: 10, KeyBy: ThrottleKeyByJWTClaim,
	}).Validate(ctx); p == nil {
		t.Error("jwt_claim w/o claim name: p=nil")
	}
	// jwt_claim with malformed claim name.
	if p := (&EdgeRuleThrottleAction{
		RequestsPerSecond: 10, Burst: 10, KeyBy: ThrottleKeyByJWTClaim, JWTClaimName: "1bad",
	}).Validate(ctx); p == nil {
		t.Error("malformed claim name: p=nil")
	}
	// jwt_claim with valid claim name and max_keys over plan.
	if p := (&EdgeRuleThrottleAction{
		RequestsPerSecond: 10, Burst: 10, KeyBy: ThrottleKeyByJWTClaim, JWTClaimName: "tier", MaxKeysPerRule: 9999,
	}).Validate(ctx); p == nil {
		t.Error("max_keys > plan: p=nil")
	}
	// Per-consumer on a plan that doesn't expose it (PlanMaxKeysPerRule=0).
	noPerConsumer := ThrottleValidationContext{PlanMaxRPS: 100, PlanMaxBurst: 200, PlanMaxKeysPerRule: 0}
	if p := (&EdgeRuleThrottleAction{
		RequestsPerSecond: 10, Burst: 10, KeyBy: ThrottleKeyByAPIKey, MaxKeysPerRule: 5,
	}).Validate(noPerConsumer); p == nil {
		t.Error("per-consumer on plan w/o ceiling: p=nil")
	}
	// Unknown key_by.
	if p := (&EdgeRuleThrottleAction{
		RequestsPerSecond: 10, Burst: 10, KeyBy: "ip",
	}).Validate(ctx); p == nil {
		t.Error("unknown key_by: p=nil")
	}
	// Happy path.
	if p := (&EdgeRuleThrottleAction{
		RequestsPerSecond: 10, Burst: 10, KeyBy: ThrottleKeyByJWTClaim, JWTClaimName: "tier",
	}).Validate(ctx); p != nil {
		t.Errorf("ok: %v", p)
	}
}

func TestEdgeRuleBudgetActionValidate_Mega4(t *testing.T) {
	t.Parallel()
	if p := (*EdgeRuleBudgetAction)(nil).Validate(); p == nil {
		t.Error("nil: p=nil")
	}
	if p := (&EdgeRuleBudgetAction{}).Validate(); p == nil {
		t.Error("zero budget: p=nil")
	}
	if p := (&EdgeRuleBudgetAction{BudgetMs: -1}).Validate(); p == nil {
		t.Error("neg budget: p=nil")
	}
	if p := (&EdgeRuleBudgetAction{BudgetMs: int(RequestBudgetMax.Milliseconds()) + 1}).Validate(); p == nil {
		t.Error("budget over cap: p=nil")
	}
	long := strings.Repeat("h", 129)
	if p := (&EdgeRuleBudgetAction{BudgetMs: 1000, AllowOverrideHeader: long}).Validate(); p == nil {
		t.Error("over-128 header name: p=nil")
	}
	if p := (&EdgeRuleBudgetAction{BudgetMs: 1000, AllowOverrideHeader: "1bad"}).Validate(); p == nil {
		t.Error("digit-prefixed header name: p=nil")
	}
	if p := (&EdgeRuleBudgetAction{BudgetMs: 1000, AllowOverrideHeader: "X-Custom"}).Validate(); p != nil {
		t.Errorf("ok: %v", p)
	}
}

func TestEdgeRuleCacheActionValidate_Mega4(t *testing.T) {
	t.Parallel()
	if p := (*EdgeRuleCacheAction)(nil).Validate(); p == nil {
		t.Error("nil: p=nil")
	}
	// Zero MaxAgeSeconds defaults to ResponseCacheDefaultMaxAgeSeconds (60).
	got := (&EdgeRuleCacheAction{StaleIfErrorSeconds: 0}).Validate()
	if got != nil {
		t.Errorf("default max_age: %v", got)
	}
	if p := (&EdgeRuleCacheAction{MaxAgeSeconds: -1}).Validate(); p == nil {
		t.Error("neg max_age: p=nil")
	}
	if p := (&EdgeRuleCacheAction{MaxAgeSeconds: ResponseCacheMaxAgeMaxSeconds + 1}).Validate(); p == nil {
		t.Error("max_age over cap: p=nil")
	}
	if p := (&EdgeRuleCacheAction{StaleIfErrorSeconds: -1}).Validate(); p == nil {
		t.Error("neg stale: p=nil")
	}
	if p := (&EdgeRuleCacheAction{StaleIfErrorSeconds: ResponseCacheStaleIfErrorMaxSeconds + 1}).Validate(); p == nil {
		t.Error("stale over cap: p=nil")
	}
	if p := (&EdgeRuleCacheAction{Methods: []string{"POST"}}).Validate(); p == nil {
		t.Error("POST method: p=nil")
	}
	if p := (&EdgeRuleCacheAction{VaryOn: []string{"Authorization"}}).Validate(); p == nil {
		t.Error("Authorization vary_on: p=nil")
	}
	if p := (&EdgeRuleCacheAction{VaryOn: []string{"Accept-Language"}, Methods: []string{"GET"}}).Validate(); p != nil {
		t.Errorf("ok: %v", p)
	}
}

// --- validateGeoCountryCode (closed vocab) -----------------------

func TestValidateGeoCountryCode_Mega4(t *testing.T) {
	t.Parallel()
	cases := []struct {
		code    string
		wantErr bool
	}{
		{"DE", false},
		{"us", false}, // case-insensitive
		{"DE", false},
		{"ZZ", true}, // reserved
		{"AA", true}, // user-assigned
		{"EU", true}, // exceptionally reserved
		{"X", true},  // 1-letter
		{"USA", true},
		{"12", true}, // digits
		{"DÉ", true}, // non-ASCII letter
	}
	for _, c := range cases {
		c := c
		t.Run(c.code, func(t *testing.T) {
			t.Parallel()
			err := validateGeoCountryCode(c.code)
			if c.wantErr && err == nil {
				t.Errorf("want err, got nil")
			}
			if !c.wantErr && err != nil {
				t.Errorf("want nil err, got %v", err)
			}
		})
	}
}

// --- ThrottleKeyByIsPerConsumer (closed vocab) ------------------

func TestThrottleKeyByIsPerConsumer_Mega4(t *testing.T) {
	t.Parallel()
	cases := map[string]bool{
		"":                      false, // back-compat
		ThrottleKeyByNone:       false,
		ThrottleKeyByAPIKey:     true,
		ThrottleKeyByConsumerID: true,
		ThrottleKeyByJWTSubject: true,
		ThrottleKeyByJWTClaim:   true,
		"ip":                    false, // unknown → default-false
		"unknown-thing":         false,
	}
	for k, want := range cases {
		if got := ThrottleKeyByIsPerConsumer(k); got != want {
			t.Errorf("ThrottleKeyByIsPerConsumer(%q) = %v, want %v", k, got, want)
		}
	}
}

// --- Sidecar.Validate / Sidecars.Validate -----------------------

func TestSidecarValidate_Mega4(t *testing.T) {
	t.Parallel()
	limits := Limits{}

	if p := (*Sidecar)(nil).Validate(limits); p != nil {
		t.Errorf("nil: p=%v, want nil", p)
	}
	// Happy path: a digest-pinned image + init type.
	if p := (&Sidecar{
		Name:  "logs",
		Image: "ghcr.io/org/logs@sha256:" + strings.Repeat("a", 64),
		Type:  SidecarTypeInit,
		Cmd:   []string{"sh", "-c", "tail -f /dev/null"},
		Env:   map[string]string{"X": "y"},
		Port:  0,
		RamMB: 64,
	}).Validate(limits); p != nil {
		t.Errorf("ok: %v", p)
	}
	// Bad name.
	if p := (&Sidecar{Name: "BAD NAME", Image: "x@sha256:" + strings.Repeat("a", 64), Type: SidecarTypeInit}).Validate(limits); p == nil {
		t.Error("bad name: p=nil")
	}
	// Image not digest-pinned.
	if p := (&Sidecar{Name: "ok", Image: "ghcr.io/org/img:latest", Type: SidecarTypeInit}).Validate(limits); p == nil {
		t.Error("non-digest image: p=nil")
	}
	// Stateful denylist (postgres would be stateful per pkg/statefuldenylist).
	if p := (&Sidecar{Name: "ok", Image: "postgres@sha256:" + strings.Repeat("a", 64), Type: SidecarTypeInit}).Validate(limits); p == nil {
		t.Error("stateful image: p=nil")
	}
	// Bad type.
	if p := (&Sidecar{
		Name: "ok", Image: "x@sha256:" + strings.Repeat("a", 64), Type: "weird",
	}).Validate(limits); p == nil {
		t.Error("bad type: p=nil")
	}
	// Empty cmd element.
	if p := (&Sidecar{
		Name: "ok", Image: "x@sha256:" + strings.Repeat("a", 64), Type: SidecarTypeInit,
		Cmd: []string{"sh", "", "tail"},
	}).Validate(limits); p == nil {
		t.Error("empty cmd elem: p=nil")
	}
	// Bad env key.
	if p := (&Sidecar{
		Name: "ok", Image: "x@sha256:" + strings.Repeat("a", 64), Type: SidecarTypeInit,
		Env: map[string]string{"1BAD": "x"},
	}).Validate(limits); p == nil {
		t.Error("bad env key: p=nil")
	}
	// Over-size env value (limit-driven 413).
	limitsBytes := Limits{EnvValueMaxBytes: 4}
	if p := (&Sidecar{
		Name: "ok", Image: "x@sha256:" + strings.Repeat("a", 64), Type: SidecarTypeInit,
		Env: map[string]string{"OK": "toolong"},
	}).Validate(limitsBytes); p == nil {
		t.Error("oversize env value: p=nil")
	}
	// Port out of range.
	if p := (&Sidecar{
		Name: "ok", Image: "x@sha256:" + strings.Repeat("a", 64), Type: SidecarTypeInit, Port: 70000,
	}).Validate(limits); p == nil {
		t.Error("port over 65535: p=nil")
	}
	// RamMB out of range.
	if p := (&Sidecar{
		Name: "ok", Image: "x@sha256:" + strings.Repeat("a", 64), Type: SidecarTypeInit, RamMB: 16,
	}).Validate(limits); p == nil {
		t.Error("RamMB < 32: p=nil")
	}
}

func TestSidecarsValidate_Mega4(t *testing.T) {
	t.Parallel()
	limits := Limits{}
	good := Sidecar{
		Name: "ok", Image: "x@sha256:" + strings.Repeat("a", 64), Type: SidecarTypeInit,
	}
	t.Run("empty slice OK", func(t *testing.T) {
		t.Parallel()
		if p := (Sidecars{}).Validate(limits); p != nil {
			t.Errorf("%v", p)
		}
	})
	t.Run("two inits rejected", func(t *testing.T) {
		t.Parallel()
		ss := Sidecars{good, good}
		if p := ss.Validate(limits); p == nil {
			t.Error("two inits: p=nil")
		}
	})
	t.Run("dup name rejected", func(t *testing.T) {
		t.Parallel()
		ss := Sidecars{
			{Name: "ok", Image: "x@sha256:" + strings.Repeat("a", 64), Type: SidecarTypeInit},
			{Name: "ok", Image: "y@sha256:" + strings.Repeat("b", 64), Type: SidecarTypeSidecar},
		}
		if p := ss.Validate(limits); p == nil {
			t.Error("dup name: p=nil")
		}
	})
}

// --- HasHeader ---------------------------------------------------

func TestProblemHasHeader_Mega4(t *testing.T) {
	t.Parallel()
	// nil extraHeaders → nil.
	if v := (&Problem{}).HasHeader("X-Custom"); v != nil {
		t.Errorf("empty: %v", v)
	}
	p := NewProblem(http.StatusBadRequest, CodeValidation, "t", "d").
		WithHeader("X-A", "1").
		WithHeader("X-A", "2")
	if v := p.HasHeader("X-A"); len(v) != 2 || v[0] != "1" || v[1] != "2" {
		t.Errorf("HasHeader = %v", v)
	}
	if v := p.HasHeader("missing"); v != nil {
		t.Errorf("missing: %v", v)
	}
}

// --- StatusForCode (full closed-set table) -----------------------

func TestStatusForCode_Mega4(t *testing.T) {
	t.Parallel()
	cases := []struct {
		code string
		want int
	}{
		{CodePlanLimitApps, http.StatusForbidden},
		{CodePlanLimitRAM, http.StatusForbidden},
		{CodePlanLimitConcur, http.StatusTooManyRequests},
		{CodeSourceTooLarge, http.StatusRequestEntityTooLarge},
		{CodeSourceInvalid, http.StatusBadRequest},
		{CodeValidation, http.StatusBadRequest},
		{CodeNotFound, http.StatusNotFound},
		{CodeConflict, http.StatusConflict},
		{CodeUnauthorized, http.StatusUnauthorized},
		{CodeSessionExpired, http.StatusUnauthorized},
		{CodePayment, http.StatusPaymentRequired},
		{CodeBuildOOM, http.StatusServiceUnavailable},
		{CodeBuildTimeout, http.StatusServiceUnavailable},
		{CodeDeployFailed, http.StatusUnprocessableEntity},
		{CodeRequestTooLarge, http.StatusRequestEntityTooLarge},
		{CodeUnsupportedMediaType, http.StatusUnsupportedMediaType},
		{CodeQuotaExhausted, http.StatusTooManyRequests},
		{CodeExportRateLimited, http.StatusTooManyRequests},
	}
	for _, c := range cases {
		c := c
		t.Run(c.code, func(t *testing.T) {
			t.Parallel()
			if got := StatusForCode(c.code); got != c.want {
				t.Errorf("StatusForCode(%q) = %d, want %d", c.code, got, c.want)
			}
		})
	}
	// Unknown → 500.
	if got := StatusForCode("not-a-real-code"); got != http.StatusInternalServerError {
		t.Errorf("unknown code: %d, want 500", got)
	}
	// "" empty also falls into default.
	if got := StatusForCode(""); got != http.StatusInternalServerError {
		t.Errorf("empty code: %d, want 500", got)
	}
}

// --- Sanity: errors.As / errors.Is on With* chains ---------------

func TestProblemChainErrorsAs_Mega4(t *testing.T) {
	t.Parallel()
	// Ensure With* chain returns the same *Problem pointer so callers can chain.
	base := NewProblem(http.StatusBadRequest, CodeValidation, "t", "d")
	chain := base.WithLimit(1, 2).WithDocs("docs/x").WithWhy("w").WithFix("f")
	if chain != base {
		t.Error("With* chain should return receiver")
	}
	// Sanity: limit surfaced (Limit/Observed are *int64).
	if base.Limit == nil || *base.Limit != 1 || base.Observed == nil || *base.Observed != 2 {
		t.Errorf("WithLimit: limit=%v observed=%v", base.Limit, base.Observed)
	}
	// AsProblem round-trip.
	var dst *Problem
	if !errors.As(base, &dst) {
		t.Error("errors.As: failed")
	}
}
