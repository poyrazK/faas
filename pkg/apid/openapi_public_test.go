package apid

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

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
// embedded source keeps it for SDK generation and contract tooling.
func TestPublicOpenAPIHidesDurableEntityPaths(t *testing.T) {
	full := decodeSpec(t, OpenAPIYAML())
	public := decodeSpec(t, PublicOpenAPIYAML())
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
	if got := string(publicSpec([]byte("paths: [unterminated"))); !strings.HasPrefix(got, "error:") {
		t.Errorf("malformed spec returned %q, want an error document", got)
	}
}
