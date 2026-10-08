package openapidiff

import (
	"encoding/json"
	"strings"
	"testing"
)

func routePairFixture(t *testing.T, document string) *Spec {
	t.Helper()
	spec, err := LoadBytes([]byte(document))
	if err != nil {
		t.Fatal(err)
	}
	return spec
}

func TestCompareRoutePairAlignsRenamedRouteParameters(t *testing.T) {
	baseline := routePairFixture(t, `{"openapi":"3.1.0","paths":{"/v1/users/{id}":{"parameters":[{"name":"id","in":"path","required":true,"schema":{"type":"string"}}],"get":{"responses":{"200":{"description":"ok","content":{"application/json":{"schema":{"type":"object","properties":{"id":{"type":"string"},"name":{"type":"string"}},"required":["id"]}}}}}}}}}`)
	candidate := routePairFixture(t, `{"openapi":"3.1.0","paths":{"/v2/accounts/{accountID}":{"parameters":[{"name":"accountID","in":"path","required":true,"schema":{"type":"string"}}],"get":{"responses":{"200":{"description":"ok","content":{"application/json":{"schema":{"type":"object","properties":{"id":{"type":"string"},"name":{"type":"string"}},"required":["id"]}}}}}}}}}`)

	got, err := CompareRoutePair(baseline, "GET", "/v1/users/{id}", candidate, "GET", "/v2/accounts/{accountID}")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "no_supported_breaks" || len(got.Findings) != 0 {
		t.Fatalf("renamed route and path parameter comparison = %+v", got)
	}
}

func TestCompareRoutePairFindsMethodRequestAndResponseBreaks(t *testing.T) {
	baseline := routePairFixture(t, `{"openapi":"3.1.0","paths":{"/legacy/{id}":{"parameters":[{"name":"id","in":"path","required":true,"schema":{"type":"string"}}],"get":{"responses":{"200":{"description":"ok","content":{"application/json":{"schema":{"type":"object","properties":{"id":{"type":"string"},"name":{"type":"string"}},"required":["id"]}}}}}}}}}`)
	candidate := routePairFixture(t, `{"openapi":"3.1.0","paths":{"/accounts/{accountID}":{"parameters":[{"name":"accountID","in":"path","required":true,"schema":{"type":"string"}}],"post":{"requestBody":{"required":true,"content":{"application/json":{"schema":{"type":"object","properties":{"email":{"type":"string"}},"required":["email"],"example":"private-request-value"}}}},"responses":{"200":{"description":"ok","content":{"application/json":{"schema":{"type":"object","properties":{"id":{"type":"string"}},"required":["id"]}}}}}}}}}`)

	got, err := CompareRoutePair(baseline, "GET", "/legacy/{id}", candidate, "POST", "/accounts/{accountID}")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "breaking" {
		t.Fatalf("status = %q, want breaking: %+v", got.Status, got)
	}
	for _, code := range []string{"method_changed", "response_field_removed", "request_body_required"} {
		if !routePairHasCode(got, code) {
			t.Errorf("findings %+v omit %s", got.Findings, code)
		}
	}
	body, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "private-request-value") {
		t.Fatalf("report leaked a schema example: %s", body)
	}
}

func TestCompareRoutePairTreatsMissingContractAsUnknown(t *testing.T) {
	baseline := routePairFixture(t, `{"openapi":"3.1.0","paths":{"/legacy":{"get":{"responses":{"200":{"description":"ok"}}}}}}`)
	candidate := routePairFixture(t, `{"openapi":"3.1.0","paths":{"/other":{"get":{"responses":{"200":{"description":"ok"}}}}}}`)
	got, err := CompareRoutePair(baseline, "GET", "/legacy", candidate, "GET", "/missing")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "unknown" || !routePairHasCode(got, "successor_operation_not_found") {
		t.Fatalf("missing successor contract was not inconclusive: %+v", got)
	}
}

func TestCompareRoutePairSeparatesSecurityWideningFromClientBreak(t *testing.T) {
	open := routePairFixture(t, `{"openapi":"3.1.0","paths":{"/legacy":{"get":{"responses":{"200":{"description":"ok"}}}}}}`)
	authenticated := routePairFixture(t, `{"openapi":"3.1.0","security":[{"ApiKey":[]}],"components":{"securitySchemes":{"ApiKey":{"type":"apiKey","in":"header","name":"X-Key"}}},"paths":{"/next":{"get":{"responses":{"200":{"description":"ok"}}}}}}`)
	got, err := CompareRoutePair(open, "GET", "/legacy", authenticated, "GET", "/next")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "breaking" || !routePairHasCode(got, "authentication_required") {
		t.Fatalf("new auth requirement was not reported as client breaking: %+v", got)
	}

	got, err = CompareRoutePair(authenticated, "GET", "/next", open, "GET", "/legacy")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "review_required" || !routePairHasCode(got, "anonymous_access_added") {
		t.Fatalf("widened access should require security review: %+v", got)
	}
}

func routePairHasCode(result RoutePairComparison, code string) bool {
	for _, finding := range result.Findings {
		if finding.Code == code {
			return true
		}
	}
	return false
}
