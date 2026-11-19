package gregalemanifest

import (
	"strings"
	"testing"
)

// gregale.yaml and gregale.toml turn production protection on or off
// (ADR-20261116120000 §5); unquoted on and off stay strings.
func TestManifestProtection(t *testing.T) {
	for doc, want := range map[string]string{
		"protection: on\n":     "on",
		"protection: off\n":    "off",
		"protection: \"on\"\n": "on",
		"labels:\n  a: b\n":    "",
	} {
		m, err := ParseBytes([]byte(doc))
		if err != nil {
			t.Fatalf("%q: %v", doc, err)
		}
		if m.Protection != want {
			t.Fatalf("%q: protection = %q, want %q", doc, m.Protection, want)
		}
		if err := m.Validate(); err != nil {
			t.Fatalf("%q: Validate = %v", doc, err)
		}
	}
	tomlManifest, err := ParseTOMLBytes([]byte("protection = \"off\"\n"))
	if err != nil || tomlManifest.Protection != "off" {
		t.Fatalf("toml protection = %+v, %v", tomlManifest, err)
	}
	for _, doc := range []string{"protection: maybe\n", "protection: true\n"} {
		m, err := ParseBytes([]byte(doc))
		if err != nil {
			continue
		}
		if err := m.Validate(); err == nil || !strings.Contains(err.Error(), "protection") {
			t.Errorf("%q: Validate = %v, want a protection error", doc, err)
		}
	}
}
