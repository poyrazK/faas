package gregalemanifest

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestWorkflowForEachExampleManifest(t *testing.T) {
	raw, err := os.ReadFile("../../examples/foreach/gregale.yaml")
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
	loop := manifest.Workflows[0].Steps[0].ForEach
	if loop == nil || loop.Items != "input.recipients" || loop.Action.Timeout != 30*time.Second || loop.Action.Retry.MaxAttempts != 3 {
		t.Fatalf("manifest lost iteration: %+v", loop)
	}
	var input map[string]string
	if json.Unmarshal(loop.Action.Input, &input) != nil || input["recipient"] != "{{input.item}}" || input["invoice_id"] != "{{input.input.invoice_id}}" {
		t.Fatalf("manifest lost mapping: %s", loop.Action.Input)
	}
	if err := manifest.ValidateForPlan(api.PlanFree); err == nil {
		t.Fatal("Free plan accepted iteration")
	}
}
