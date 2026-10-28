package apid

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/apid/monetization"
	"github.com/onebox-faas/faas/pkg/apid/realtimepreview"
	"gopkg.in/yaml.v3"
)

var allVisible = Visibility{Monetization: true, RealtimePreview: true}

type specShape struct {
	Paths      map[string]any            `yaml:"paths" json:"paths"`
	Components map[string]map[string]any `yaml:"components" json:"components"`
}

func decodeSpec(t *testing.T, raw []byte) specShape {
	t.Helper()
	var spec specShape
	if err := yaml.Unmarshal(raw, &spec); err != nil {
		t.Fatalf("decode spec: %v", err)
	}
	return spec
}

// The anonymous spec must not advertise the durable entity preview, while the
// embedded source keeps it for SDK generation and contract tooling. API
// monetization and the realtime preview are published here so only the
// durable entity filter is measured.
func TestPublicOpenAPIHidesDurableEntityPaths(t *testing.T) {
	full := decodeSpec(t, OpenAPIYAML())
	public := decodeSpec(t, PublicOpenAPIYAMLFor(allVisible))
	hidden := 0
	for path := range full.Paths {
		if hiddenPublicPath(path) {
			hidden++
			if _, ok := public.Paths[path]; ok {
				t.Errorf("public spec still lists %s", path)
			}
			continue
		}
		if got, ok := public.Paths[path]; !ok {
			t.Errorf("public spec dropped customer path %s", path)
		} else if !reflect.DeepEqual(got, full.Paths[path]) {
			t.Errorf("public spec changed customer path %s", path)
		}
	}
	for kind, group := range public.Components {
		for name, body := range group {
			if !reflect.DeepEqual(body, full.Components[kind][name]) {
				t.Errorf("public spec changed component %s/%s", kind, name)
			}
		}
	}
	if hidden == 0 {
		t.Fatal("embedded spec has no durable entity paths; the filter test is vacuous")
	}
	if len(public.Paths) != len(full.Paths)-hidden {
		t.Errorf("public paths = %d, want %d", len(public.Paths), len(full.Paths)-hidden)
	}
}

// The operator trace ring is never customer surface, whatever tier tracing
// has: no capability's default may publish it.
func TestPublicOpenAPIHidesOperatorTraceRing(t *testing.T) {
	const path = "/v1/traces/{trace_id}"
	if _, ok := decodeSpec(t, OpenAPIYAML()).Paths[path]; !ok {
		t.Fatalf("embedded spec has no %s; the test is vacuous", path)
	}
	everything := func(string) bool { return false }
	for name, raw := range map[string][]byte{
		"served": PublicOpenAPIYAMLFor(allVisible),
		"sdk":    FilterSpec(OpenAPIYAML(), everything, Visibility{}),
	} {
		if _, ok := decodeSpec(t, raw).Paths[path]; ok {
			t.Errorf("%s spec lists the operator trace ring %s", name, path)
		}
	}
}

// Components only the hidden routes use go too; shared ones stay.
func TestPublicOpenAPIPrunesExclusiveComponents(t *testing.T) {
	full := decodeSpec(t, OpenAPIYAML())
	public := decodeSpec(t, PublicOpenAPIYAML())
	for _, name := range []string{"DurableEntityInvokeRequest", "DurableEntityInspectResponse", "DurableEntityStateExport"} {
		if _, ok := full.Components["schemas"][name]; !ok {
			t.Fatalf("embedded spec lacks %s; update this test", name)
		}
		if _, ok := public.Components["schemas"][name]; ok {
			t.Errorf("public spec still defines %s", name)
		}
	}
	for _, name := range []string{"Problem", "DurableEntityValidatorDeploymentInfo"} {
		if _, ok := public.Components["schemas"][name]; !ok {
			t.Errorf("public spec dropped shared schema %s", name)
		}
	}
}

// adr: 20261018090210 — until API monetization is released the served spec
// lists none of its paths, schemas only they use or webhook events; once it
// is released they all return.
func TestPublicOpenAPIFollowsMonetizationRelease(t *testing.T) {
	full := decodeSpec(t, OpenAPIYAML())
	hidden := decodeSpec(t, PublicOpenAPIYAMLFor(Visibility{RealtimePreview: true}))
	shown := decodeSpec(t, PublicOpenAPIYAMLFor(allVisible))
	monetized := 0
	for path := range full.Paths {
		if !monetization.Path(path) || hiddenPublicPath(path) {
			continue
		}
		monetized++
		if _, ok := hidden.Paths[path]; ok {
			t.Errorf("hidden spec lists %s", path)
		}
		if _, ok := shown.Paths[path]; !ok {
			t.Errorf("released spec dropped %s", path)
		}
	}
	if monetized < 40 {
		t.Fatalf("only %d monetization paths in the embedded spec; the route list is stale", monetized)
	}
	for _, name := range append([]string{"APIConsumerRateCardResponse", "BillingRunResponse"}, monetization.Schemas...) {
		if _, ok := full.Components["schemas"][name]; !ok {
			t.Fatalf("embedded spec lacks %s; update this test", name)
		}
		if _, ok := hidden.Components["schemas"][name]; ok {
			t.Errorf("hidden spec still defines %s", name)
		}
		if _, ok := shown.Components["schemas"][name]; !ok {
			t.Errorf("released spec dropped %s", name)
		}
	}
	hiddenYAML, shownYAML := string(PublicOpenAPIYAMLFor(Visibility{RealtimePreview: true})), string(PublicOpenAPIYAMLFor(allVisible))
	for _, event := range monetization.WebhookEvents {
		if strings.Contains(hiddenYAML, "- "+event+"\n") || strings.Contains(hiddenYAML, "["+event) {
			t.Errorf("hidden spec still offers webhook event %s", event)
		}
		if !strings.Contains(shownYAML, event) {
			t.Errorf("released spec lacks webhook event %s", event)
		}
	}
	if _, ok := decodeSpec(t, PublicOpenAPIYAML()).Paths["/v1/apps/{slug}/rate-cards"]; ok {
		t.Error("PublicOpenAPIYAML must hide monetization")
	}
}

// adr: 20261019102657 — until every account sees the realtime preview the
// served spec lists none of its routes, the preview-only fields of released
// schemas, the retained delivery value or the schemas only they use.
func TestPublicOpenAPIFollowsRealtimePreview(t *testing.T) {
	full := decodeSpec(t, OpenAPIYAML())
	hidden := decodeSpec(t, PublicOpenAPIYAMLFor(Visibility{Monetization: true}))
	shown := decodeSpec(t, PublicOpenAPIYAMLFor(allVisible))
	routes := 0
	for path, item := range full.Paths {
		for method := range item.(map[string]any) {
			if !realtimepreview.Route(method, path) {
				continue
			}
			routes++
			if op, _ := hidden.Paths[path].(map[string]any); op[method] != nil {
				t.Errorf("hidden spec lists %s %s", method, path)
			}
			if op, _ := shown.Paths[path].(map[string]any); op[method] == nil {
				t.Errorf("published spec dropped %s %s", method, path)
			}
		}
	}
	if want := len(realtimepreview.Routes()); routes != want {
		t.Fatalf("embedded spec has %d of the %d preview routes", routes, want)
	}
	for schema, fields := range realtimepreview.Fields {
		for _, field := range fields {
			if !hasProperty(full, schema, field) {
				t.Fatalf("embedded spec lacks %s.%s; update realtimepreview.Fields", schema, field)
			}
			if hasProperty(hidden, schema, field) {
				t.Errorf("hidden spec still has %s.%s", schema, field)
			}
			if !hasProperty(shown, schema, field) {
				t.Errorf("published spec dropped %s.%s", schema, field)
			}
		}
	}
	for _, component := range append([]string{"schemas/RealtimeChannelRule"}, realtimepreview.Components...) {
		kind, name, _ := strings.Cut(component, "/")
		if _, ok := full.Components[kind][name]; !ok {
			t.Fatalf("embedded spec lacks %s; update realtimepreview.Components", component)
		}
		if _, ok := hidden.Components[kind][name]; ok {
			t.Errorf("hidden spec still defines %s", component)
		}
		if _, ok := shown.Components[kind][name]; !ok {
			t.Errorf("published spec dropped %s", component)
		}
	}
	listed := func(spec, event string) bool {
		return strings.Contains(spec, "- "+event+"\n") || strings.Contains(spec, "["+event) || strings.Contains(spec, ", "+event)
	}
	hiddenYAML, shownYAML := string(PublicOpenAPIYAMLFor(Visibility{Monetization: true})), string(PublicOpenAPIYAMLFor(allVisible))
	for _, event := range realtimepreview.WebhookEvents {
		if listed(hiddenYAML, event) {
			t.Errorf("hidden spec still offers webhook event %s", event)
		}
		if !listed(shownYAML, event) {
			t.Errorf("published spec does not offer webhook event %s", event)
		}
	}
	if _, ok := hidden.Components["schemas"]["ManagedRealtimeEndpointResponse"]; !ok {
		t.Error("hidden spec dropped the released endpoint schema")
	}
	method, path, _ := strings.Cut(realtimepreview.RetainedDeliveryRoute, " ")
	for name, spec := range map[string]specShape{"hidden": hidden, "published": shown} {
		values := deliveryValues(t, spec, strings.ToLower(method), path)
		if got, want := strings.Contains(values, realtimepreview.RetainedDelivery), name == "published"; got != want {
			t.Errorf("%s spec delivery values = %s", name, values)
		}
	}
}

func hasProperty(spec specShape, schema, field string) bool {
	body, _ := spec.Components["schemas"][schema].(map[string]any)
	properties, _ := body["properties"].(map[string]any)
	_, ok := properties[field]
	return ok
}

// deliveryValues returns the delivery query parameter's enum on a route.
func deliveryValues(t *testing.T, spec specShape, method, path string) string {
	t.Helper()
	item, _ := spec.Paths[path].(map[string]any)
	op, _ := item[method].(map[string]any)
	params, _ := op["parameters"].([]any)
	for _, raw := range params {
		param, _ := raw.(map[string]any)
		if param["name"] == "delivery" {
			schema, _ := param["schema"].(map[string]any)
			body, _ := json.Marshal(schema["enum"])
			return string(body)
		}
	}
	t.Fatalf("%s %s has no delivery parameter", method, path)
	return ""
}

func TestOpenAPIHandlersFollowMonetization(t *testing.T) {
	var on bool
	o := OpenAPI{Monetization: func() bool { return on }}
	for _, release := range []bool{false, true} {
		on = release
		rec := httptest.NewRecorder()
		o.ServeJSON(rec, httptest.NewRequest(http.MethodGet, "/v1/openapi.json", nil))
		var spec specShape
		if err := json.Unmarshal(rec.Body.Bytes(), &spec); err != nil {
			t.Fatalf("decode JSON spec: %v", err)
		}
		if _, ok := spec.Paths["/v1/apps/{slug}/rate-cards"]; ok != release {
			t.Errorf("released=%t: rate cards listed=%t", release, ok)
		}
		rec = httptest.NewRecorder()
		o.ServeDocs(rec, httptest.NewRequest(http.MethodGet, "/docs", nil))
		if !strings.Contains(rec.Body.String(), openAPIJSONSHA256For(Visibility{Monetization: release})) {
			t.Errorf("released=%t: docs page names another document", release)
		}
	}
}

// Every component reference left in the public spec must resolve.
func TestPublicOpenAPIHasNoDanglingRefs(t *testing.T) {
	assertNoDanglingRefs(t, PublicOpenAPIYAML())
}

// adr: 20261023115741 — heavy filtering keeps every component a surviving
// path or component references, including components no path references.
func TestFilterSpecLeavesNoDanglingRefs(t *testing.T) {
	n := 0
	assertNoDanglingRefs(t, FilterSpec(openapiYAML, func(string) bool { n++; return n%2 == 0 }, Visibility{}))
}

// adr: 20261023115741 — the served spec and API reference leave out the
// paths Hidden reports, and the docs page names the document it serves.
func TestOpenAPIHandlersHideCapabilityPaths(t *testing.T) {
	hidden := func(path string) bool { return strings.HasPrefix(path, "/v1/apps/{slug}/realtime") }
	o := OpenAPI{Hidden: func() (string, func(string) bool) { return "realtime", hidden }}
	rec := httptest.NewRecorder()
	o.ServeJSON(rec, httptest.NewRequest(http.MethodGet, "/v1/openapi.json", nil))
	var spec specShape
	if err := json.Unmarshal(rec.Body.Bytes(), &spec); err != nil {
		t.Fatalf("decode JSON spec: %v", err)
	}
	if _, ok := spec.Paths["/v1/apps"]; !ok {
		t.Fatal("a released path left the served spec")
	}
	for path := range spec.Paths {
		if hidden(path) {
			t.Errorf("hidden path %s is served", path)
		}
	}
	docs := httptest.NewRecorder()
	o.ServeDocs(docs, httptest.NewRequest(http.MethodGet, "/docs", nil))
	sum := sha256.Sum256(rec.Body.Bytes())
	if !strings.Contains(docs.Body.String(), hex.EncodeToString(sum[:])) {
		t.Error("the docs page names another document than the served spec")
	}
}

func assertNoDanglingRefs(t *testing.T, raw []byte) {
	t.Helper()
	var doc any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("decode spec: %v", err)
	}
	components := decodeSpec(t, raw).Components
	var walk func(any)
	walk = func(v any) {
		switch v := v.(type) {
		case map[string]any:
			for _, child := range v {
				walk(child)
			}
		case []any:
			for _, child := range v {
				walk(child)
			}
		case string:
			ref, ok := componentRef(v)
			if !ok {
				return
			}
			kind, name, _ := strings.Cut(strings.TrimPrefix(ref, "#/components/"), "/")
			if _, ok := components[kind][name]; !ok {
				t.Errorf("dangling reference %s", v)
			}
		}
	}
	walk(doc)
}

func TestServeOpenAPISpecJSONHidesDurableEntityPaths(t *testing.T) {
	rec := httptest.NewRecorder()
	ServeOpenAPISpecJSON(rec, httptest.NewRequest(http.MethodGet, "/v1/openapi.json", nil))
	var spec specShape
	if err := json.Unmarshal(rec.Body.Bytes(), &spec); err != nil {
		t.Fatalf("decode JSON spec: %v", err)
	}
	for path := range spec.Paths {
		if hiddenPublicPath(path) {
			t.Errorf("JSON spec lists hidden path %s", path)
		}
	}
	if len(spec.Paths) == 0 {
		t.Fatal("JSON spec has no paths")
	}
}

func TestPublicSpecFailsClosed(t *testing.T) {
	if got := string(publicSpec([]byte("paths: [unterminated"), Visibility{})); !strings.HasPrefix(got, "error:") {
		t.Errorf("malformed spec returned %q, want an error document", got)
	}
}
