package eventcontract

import "testing"

// adr: 20261026140000 — app.<UUID> belongs to the app in every UUID spelling.
func TestAppSource(t *testing.T) {
	const id = "0f8fad5b-d9cb-469f-a165-70867728950e"
	for _, c := range []struct {
		source string
		want   string
	}{
		{"app." + id, id},
		{"app.0F8FAD5B-D9CB-469F-A165-70867728950E", id},
		{"app.{" + id + "}", id},
		{"app.urn:uuid:" + id, id},
		{"app.0f8fad5bd9cb469fa16570867728950e", id},
		{"app.orders", ""},
		{"app.", ""},
		{"apps." + id, ""},
		{"billing." + id, ""},
	} {
		got, ok := AppSource(c.source)
		if ok != (c.want != "") || got != c.want {
			t.Errorf("AppSource(%q) = %q, %v; want %q", c.source, got, ok, c.want)
		}
	}
}
