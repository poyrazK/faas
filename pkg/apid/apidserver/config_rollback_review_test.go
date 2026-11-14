// adr: 20261105152500
package apidserver_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestReviewedConfigRollbackReceiptAndStaleGuard(t *testing.T) {
	f := newConfigGuardFixture(t, "reviewed-rollback", true)
	target := f.write(http.MethodPut, "/env/MODE?guard=false", api.PutAppEnvRequest{Value: "before"}, http.StatusOK)
	newer := f.write(http.MethodPut, "/env/MODE?guard=false", api.PutAppEnvRequest{Value: "newer"}, http.StatusOK)
	proof := strings.Repeat("a", 64)
	request := api.ConfigRollbackRequest{Revision: target.Revision, ExpectedRevision: &target.Revision, ExpectedAppID: f.app.ID, RequestID: uuid.NewString(), ReceiptProof: proof}
	path := "/v1/apps/" + f.app.Slug + "/config/rollback"
	stale := f.e.Do(t, http.MethodPost, path, request, nil)
	if stale.Code != http.StatusConflict {
		t.Fatalf("stale rollback=%d %s", stale.Code, stale.Body.String())
	}
	if value, _ := f.envValue("MODE"); value != "newer" {
		t.Fatalf("overwrote newer value %q", value)
	}
	request.ExpectedRevision = &newer.Revision
	accepted := f.e.Do(t, http.MethodPost, path, request, nil)
	if accepted.Code != http.StatusOK || strings.Contains(accepted.Body.String(), proof) {
		t.Fatalf("rollback=%d %s", accepted.Code, accepted.Body.String())
	}
	var result api.ConfigRollbackResponse
	if err := json.Unmarshal(accepted.Body.Bytes(), &result); err != nil || result.Change == nil || result.Change.Revision != newer.Revision+1 {
		t.Fatalf("response=%+v %v", result, err)
	}
	read := f.e.Do(t, http.MethodGet, path+"/requests/"+request.RequestID, nil, map[string]string{api.EnvBatchReceiptProofHeader: proof})
	var receipt api.ConfigRollbackReceipt
	if read.Code != http.StatusOK || json.Unmarshal(read.Body.Bytes(), &receipt) != nil || receipt.ConfigRevision != result.Change.Revision || !receipt.Changed || strings.Contains(read.Body.String(), proof) || strings.Contains(read.Body.String(), "before") {
		t.Fatalf("receipt=%d %s", read.Code, read.Body.String())
	}
	wrong := f.e.Do(t, http.MethodGet, path+"/requests/"+request.RequestID, nil, map[string]string{api.EnvBatchReceiptProofHeader: strings.Repeat("b", 64)})
	if wrong.Code != http.StatusNotFound {
		t.Fatalf("wrong proof=%d", wrong.Code)
	}
	if again := f.e.Do(t, http.MethodPost, path, request, nil); again.Code != http.StatusConflict {
		t.Fatalf("replayed=%d %s", again.Code, again.Body.String())
	}
}
