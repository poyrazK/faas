// adr: 685
package reposcan

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestComposeHealthyJobDependencyRejected(t *testing.T) {
	reasons := DependencyValidationReasons([]Workload{
		{Name: "web", DependsOn: []string{"migrate"}, DependsOnConditions: map[string]string{"migrate": api.ComposeDependencyHealthy}},
		{Name: "migrate", Class: ClassJob},
	}, nil)
	if len(reasons) != 1 || !strings.Contains(reasons[0], "job completion gates are unsupported") {
		t.Fatalf("job artifact readiness certified service health: %v", reasons)
	}
}
