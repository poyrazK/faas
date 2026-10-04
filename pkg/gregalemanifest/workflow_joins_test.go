package gregalemanifest

import (
	"os"
	"slices"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestWorkflowJoinExampleManifest(t *testing.T) {
	raw, err := os.ReadFile("../../examples/branch-joins/gregale.yaml")
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
	if len(manifest.Workflows) != 1 || manifest.Workflows[0].Steps[2].Join == nil || !slices.Equal(manifest.Workflows[0].Steps[2].Join.OutputFrom, []string{"receipt", "remind"}) {
		t.Fatal("manifest parsing lost ordered branch join")
	}
	if err := manifest.ValidateForPlan(api.PlanFree); err == nil {
		t.Fatal("Free plan accepted joined workflow")
	}
}
