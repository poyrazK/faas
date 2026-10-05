package gregalemanifest

import (
	"os"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestEventWorkflowExampleManifest(t *testing.T) {
	raw, err := os.ReadFile("../../examples/event-workflows/gregale.yaml")
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := ParseBytes(raw)
	if err != nil {
		t.Fatal(err)
	}
	if err := manifest.ValidateForPlan(api.PlanHobby); err != nil {
		t.Fatal(err)
	}
	if err := manifest.ValidateForPlan(api.PlanFree); err == nil {
		t.Fatal("Free plan accepted event workflows")
	}
}
