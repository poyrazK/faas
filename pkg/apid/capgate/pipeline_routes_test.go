package capgate

import (
	"github.com/onebox-faas/faas/pkg/productcap"
	"testing"
)

func TestPipelineProgressAndRerunsRemainPreview(t *testing.T) {
	catalog, err := productcap.Load()
	if err != nil {
		t.Fatal(err)
	}
	g := New(catalog, nil)
	for _, path := range []string{"GET /v1/execution-pipelines/{id}/events", "POST /v2/execution-pipelines/{id}/reruns"} {
		capability, ok := g.Capability(path)
		if !ok || capability != "disposable-runs" || g.ReleasedByDefault(capability) {
			t.Fatalf("unreleased pipeline route escaped preview gate: %s %s", path, capability)
		}
	}
}
