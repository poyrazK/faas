package apid

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/apid/monetization"
	"gopkg.in/yaml.v3"
)

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
// monetization is published here so only the preview filter is measured.
func TestPublicOpenAPIHidesDurableEntityPaths(t *testing.T) {
	full := decodeSpec(t, OpenAPIYAML())
	public := decodeSpec(t, PublicOpenAPIYAMLFor(true))
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
	hidden := decodeSpec(t, PublicOpenAPIYAMLFor(false))
	shown := decodeSpec(t, PublicOpenAPIYAMLFor(true))
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
	hiddenYAML, shownYAML := string(PublicOpenAPIYAMLFor(false)), string(PublicOpenAPIYAMLFor(true))
	for _, event := range monetization.WebhookEvents {
		if strings.Contains(hiddenYAML, "- "+event+"\n") || strings.Contains(hiddenYAML, "["+event) {
			t.Errorf("hidden spec still offers webhook event %s", event)
		}
		if !strings.Contains(shownYAML, event) {
			t.Errorf("released spec lacks webhook event %s", event)
		}
	}
	if string(PublicOpenAPIYAML()) != hiddenYAML {
		t.Error("PublicOpenAPIYAML must be the hidden variant")
	}
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
		if !strings.Contains(rec.Body.String(), openAPIJSONSHA256For(release)) {
			t.Errorf("released=%t: docs page names another document", release)
		}
	}
}

// Every component reference left in the public spec must resolve.
func TestPublicOpenAPIHasNoDanglingRefs(t *testing.T) {
	var doc any
	if err := yaml.Unmarshal(PublicOpenAPIYAML(), &doc); err != nil {
		t.Fatalf("decode public spec: %v", err)
	}
	components := decodeSpec(t, PublicOpenAPIYAML()).Components
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
	if got := string(publicSpec([]byte("paths: [unterminated"), false)); !strings.HasPrefix(got, "error:") {
		t.Errorf("malformed spec returned %q, want an error document", got)
	}
}
