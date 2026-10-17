package apidserver_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/reposcan"
)

func TestScanPlanEmptyArrays(t *testing.T) {
	workload, err := json.Marshal(apidserver.ToPlanWorkload(reposcan.Workload{Name: "gateway"}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(workload), `"ports":[]`) {
		t.Fatalf("ports must be an array: %s", workload)
	}
	crons, err := json.Marshal(apidserver.ProjectWorkloadCrons([]reposcan.Workload{{Name: "gateway"}}))
	if err != nil {
		t.Fatal(err)
	}
	if string(crons) != "[]" {
		t.Fatalf("crons=%s; want []", crons)
	}
}
