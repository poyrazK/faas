package main_test

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// Prod hunt r26: a 3-service project in an account of 69 apps listed 66
// untouched apps row by row, and the removal header named an internal
// function (SoftDeleteAppCascade).
func TestProjectPlanSummarisesManyUnaffectedApps(t *testing.T) {
	plan := api.PlanResponse{
		WillDeploy: []api.PlanAffectedApp{{Action: "create", Slug: "web"}},
		Removed:    []string{"old"},
	}
	for i := range 66 {
		plan.Unaffected = append(plan.Unaffected, api.PlanAffectedApp{Action: "noop", Slug: fmt.Sprintf("other-%02d", i), ID: "id"})
	}
	var out bytes.Buffer
	main.PrintAffectedText(&out, plan, nil)
	got := out.String()
	if strings.Contains(got, "other-00") || !strings.Contains(got, "66 apps; none change") {
		t.Fatalf("unaffected apps not summarised:\n%s", got)
	}
	if strings.Contains(got, "SoftDeleteAppCascade") || !strings.Contains(got, "Removed (apps this deploy deletes):") {
		t.Fatalf("removal header:\n%s", got)
	}

	few := api.PlanResponse{Unaffected: plan.Unaffected[:3]}
	out.Reset()
	main.PrintAffectedText(&out, few, nil)
	if !strings.Contains(out.String(), "other-02") {
		t.Fatalf("a few unaffected apps should stay listed:\n%s", out.String())
	}
}
