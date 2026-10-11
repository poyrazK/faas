package routelifecycle

import (
	"net/http"
	"testing"
)

func TestMetadata(t *testing.T) {
	operation := map[string]any{"deprecated": true, "x-gregale-deprecated-at": "2026-10-01T00:00:00Z", "x-gregale-sunset-at": "2026-12-01T00:00:00Z", "x-gregale-successor": "https://example.com/v2"}
	m, err := Parse(operation)
	if err != nil {
		t.Fatal(err)
	}
	h := http.Header{"Link": []string{"<https://example.com/help>; rel=\"help\""}}
	m.Apply(h)
	if h.Get("Deprecation") != "@1790812800" || h.Get("Sunset") != "Tue, 01 Dec 2026 00:00:00 GMT" || len(h.Values("Link")) != 2 {
		t.Fatalf("headers: %v", h)
	}
	for _, tc := range []struct {
		key   string
		value any
	}{
		{"deprecated", false}, {"x-gregale-deprecated-at", true}, {"x-gregale-sunset-at", "2026-09-01T00:00:00Z"},
		{"x-gregale-successor", ""}, {"x-gregale-successor", "https://user:pass@example.com"}, {"x-gregale-successor", "https://example.com/>\r\nX: injected"},
	} {
		copy := make(map[string]any)
		for k, v := range operation {
			copy[k] = v
		}
		copy[tc.key] = tc.value
		if _, err := Parse(copy); err == nil {
			t.Errorf("accepted %s=%v", tc.key, tc.value)
		}
	}
	if _, err := Parse(map[string]any{"deprecated": true}); err != nil {
		t.Fatal(err)
	}
}
