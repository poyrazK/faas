package reposcan

import (
	"reflect"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestPartialDependencyReferencesAreValidatedWithoutBeingDeployed(t *testing.T) {
	selected := []Workload{{Name: "web", DependsOn: []string{"api"}}}
	references := []Workload{{Name: "api", Class: ClassHTTP}}
	if reasons := DependencyValidationReasons(selected, nil, references); len(reasons) != 0 {
		t.Fatal(reasons)
	}
	order, err := DependencyOrder(selected, nil, references)
	if err != nil || !reflect.DeepEqual(order, []string{"web"}) {
		t.Fatalf("order: %v %v", order, err)
	}
	if len(DependencyValidationReasons(selected, nil)) == 0 {
		t.Fatal("unknown source dependency accepted")
	}
	selected[0].DependsOnConditions = map[string]string{"api": api.ComposeDependencyHealthy}
	references[0].Class = ClassJob
	if len(DependencyValidationReasons(selected, nil, references)) == 0 {
		t.Fatal("job health condition bypassed through a reference")
	}
}
