package reposcan

import (
	"slices"
	"strings"
	"testing"
	"testing/fstest"
)

// ADR-20261026170512: the plan warns about a service the others cannot call
// by name, and about two services that share one DNS name.
func TestScanWarnsAboutServiceNames(t *testing.T) {
	compose := "services:\n" +
		"  billing_api:\n    image: ghcr.io/example/billing:1\n" +
		"  billing-api:\n    image: ghcr.io/example/billing:2\n" +
		"  web_ui:\n    image: ghcr.io/example/web:1\n" +
		"  _worker:\n    image: ghcr.io/example/worker:1\n"
	result, err := Scan(fstest.MapFS{"compose.yaml": &fstest.MapFile{Data: []byte(compose)}})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, warning := range result.Warnings {
		if strings.Contains(warning, "called") {
			names = append(names, warning)
		}
	}
	joined := strings.Join(names, "\n")
	if !strings.Contains(joined, `services "billing-api" and "billing_api" are both called billing-api.internal`) {
		t.Fatalf("no collision warning:\n%s", joined)
	}
	if !strings.Contains(joined, `service "_worker" cannot be called by name`) {
		t.Fatalf("no uncallable-name warning:\n%s", joined)
	}
	if slices.ContainsFunc(names, func(w string) bool { return strings.Contains(w, "web_ui") }) {
		t.Fatalf("web_ui is callable as web-ui, but was warned about:\n%s", joined)
	}
}
