package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestGetEnvBatchReceiptIsPassiveAndScoped(t *testing.T) {
	id := uuid.NewString()
	proof := strings.Repeat("a", 64)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/apps/alpha/env/batch/requests/"+id || r.URL.Query().Get("scope") != "staging" || r.URL.Query().Has("guard") || r.Header.Get(EnvBatchReceiptProofHeader) != proof {
			t.Errorf("unexpected receipt request: %s %s", r.Method, r.URL.String())
		}
		_ = json.NewEncoder(w).Encode(EnvBatchResponse{RequestID: id, AppID: "app", Scope: "staging", EnvironmentID: "env", UpdatedKeys: []string{"MODE"}, DeletedKeys: []string{}, ConfigChange: ConfigChangeReceipt{Revision: 4}})
	}))
	defer server.Close()
	receipt, err := NewClient(server.URL, "token").WithoutConfigGuard().GetEnvBatchReceipt(t.Context(), "alpha", "staging", id, proof)
	if err != nil || receipt.RequestID != id || receipt.EnvironmentID != "env" || receipt.ConfigChange.Revision != 4 {
		t.Fatalf("receipt=%+v err=%v", receipt, err)
	}
}

func TestEnvBatchIdentityValidation(t *testing.T) {
	zero := int64(0)
	base := EnvBatchRequest{RequestID: uuid.NewString(), ExpectedRevision: &zero, Values: map[string]string{"MODE": "safe"}}
	limits := MustLimitsFor(PlanPro)
	if p := base.Validate(limits); p != nil {
		t.Fatal(p)
	}
	invalidApp := base
	invalidApp.ExpectedAppID = "not-a-uuid"
	if invalidApp.Validate(limits) == nil {
		t.Fatal("invalid app identity accepted")
	}
	missingApp := base
	missingApp.ExpectedEnvironmentID = uuid.NewString()
	if missingApp.Validate(limits) == nil {
		t.Fatal("environment identity without app accepted")
	}
	valid := base
	valid.ExpectedAppID, valid.ExpectedEnvironmentID = uuid.NewString(), uuid.NewString()
	valid.ReceiptProof = strings.Repeat("a", 64)
	if p := valid.Validate(limits); p != nil {
		t.Fatal(p)
	}
	invalidProof := valid
	invalidProof.ReceiptProof = "not-a-proof"
	if invalidProof.Validate(limits) == nil {
		t.Fatal("invalid receipt proof accepted")
	}
}
