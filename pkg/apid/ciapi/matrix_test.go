package ciapi_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/ciapi"
	"github.com/onebox-faas/faas/pkg/ci"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestCIMatrixAPIValidationStorageAndDisabledAdmission(t *testing.T) {
	account := state.Account{ID: uuid.NewString()}
	cfg := ci.ConfigFromEnv(func(name string) string {
		if name == ci.InternalEnv {
			return "1"
		}
		if name == ci.AccountsEnv {
			return account.ID
		}
		return ""
	})
	h := ciapi.Handler{Config: cfg, Store: ci.NewMemoryStore()}
	source := "version: 1\nrepository: gregale/example\njobs:\n  test:\n    image: registry.example/ci@sha256:" + strings.Repeat("a", 64) + "\n    matrix: {alpha: {env: {MODE: alpha}}, beta: {env: {MODE: beta}}}\n    steps: [{name: unit, run: stored}]\n"
	raw, _ := json.Marshal(map[string]string{"definition": source})
	call := func(action ciapi.Action) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(raw))
		r.SetPathValue("name", "matrix")
		w := httptest.NewRecorder()
		action(h, w, r, account)
		return w
	}
	validated := call(ciapi.Handler.Validate)
	var plan ci.Plan
	if err := json.Unmarshal(validated.Body.Bytes(), &plan); validated.Code != http.StatusOK || err != nil || len(plan.Jobs) != 2 || plan.Jobs[0].Name != "test-alpha" || plan.Jobs[1].Name != "test-beta" {
		t.Fatalf("matrix API plan: %d %s", validated.Code, validated.Body)
	}
	saved := call(ciapi.Handler.PutRevision)
	var revision ci.Revision
	if err := json.Unmarshal(saved.Body.Bytes(), &revision); saved.Code != http.StatusOK || err != nil || len(revision.Definition.Jobs["test"].Matrix) != 2 {
		t.Fatal("API did not retain immutable matrix definition")
	}
	if again := call(ciapi.Handler.PutRevision); !bytes.Equal(saved.Body.Bytes(), again.Body.Bytes()) {
		t.Fatal("matrix revision PUT lost stable identity")
	}
	capabilities := call(ciapi.Handler.Capabilities)
	var limits struct {
		MaxJobs     int `json:"max_jobs"`
		MaxVariants int `json:"max_matrix_variants_per_job"`
	}
	if err := json.Unmarshal(capabilities.Body.Bytes(), &limits); err != nil || limits.MaxJobs != api.CIMaxJobs || limits.MaxVariants != api.CIMaxMatrixVariantsPerJob {
		t.Fatal("matrix limits were not advertised")
	}
	def, err := ci.Parse([]byte(source))
	if err != nil {
		t.Fatal(err)
	}
	job := def.Jobs["test"]
	job.Matrix = make(map[string]ci.MatrixVariant)
	for i := range api.CIMaxMatrixVariantsPerJob {
		job.Matrix[fmt.Sprintf("v%d", i)] = ci.MatrixVariant{}
	}
	def.Jobs["test"], def.Jobs["other"] = job, job
	def.Jobs["extra"] = ci.Job{Image: job.Image, Steps: job.Steps}
	large, _ := json.Marshal(def)
	raw, _ = json.Marshal(map[string]string{"definition": string(large)})
	overflow := call(ciapi.Handler.Validate)
	var problem api.Problem
	if err := json.Unmarshal(overflow.Body.Bytes(), &problem); overflow.Code != http.StatusBadRequest || err != nil || problem.Limit == nil || *problem.Limit != api.CIMaxJobs || problem.Observed == nil || *problem.Observed != api.CIMaxJobs+1 || problem.DocsURL != "/docs/ops/internal-ci" {
		t.Fatalf("matrix limit response: %d %s", overflow.Code, overflow.Body)
	}
	if denied := call(ciapi.Handler.SubmitExecution); denied.Code != http.StatusNotFound {
		t.Fatal("matrix enabled disabled compute admission")
	}
	h.Config = ci.Config{}
	if denied := call(ciapi.Handler.Validate); denied.Code != http.StatusNotFound {
		t.Fatal("matrix escaped the internal CI gate")
	}
}
