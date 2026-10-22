package mcpadmission

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
	"net/http/httptest"
	"testing"
)

func TestReceiptOwnershipAndHistory(t *testing.T) {
	testReceiptHistory(t, state.NewMemStore())
}
func TestPgReceiptOwnershipAndHistory(t *testing.T) {
	testReceiptHistory(t, state.NewPgStore(pgtest.OpenMigrated(t)))
}
func testReceiptHistory(t *testing.T, store state.Store) {
	ctx := context.Background()
	acct, err := store.CreateAccount(ctx, "history@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "history", Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1, IdleTimeoutS: 30, Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:abc", Status: state.DeployPending})
	if err != nil {
		t.Fatal(err)
	}
	accountUUID := uuid.MustParse(acct.ID)
	ids := []string{}
	for i := 0; i < 75; i++ {
		id := uuid.NewString()
		ids = append(ids, id)
		data, _ := json.Marshal(map[string]any{"mcp_admission": api.MCPAdmissionEvidence{Version: 1, ID: id, DeploymentID: dep.ID, Outcome: "accepted"}})
		_, err := store.AppendDeploymentAudit(ctx, state.DeploymentAudit{DeploymentID: uuid.MustParse(dep.ID), AccountID: &accountUUID, Kind: state.DeployMCPAdmission, Actor: "test", Data: data})
		if err != nil {
			t.Fatal(err)
		}
	}
	mux := func(path, id string, receipt bool, account state.Account) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", path, nil)
		r.SetPathValue("id", id)
		w := httptest.NewRecorder()
		Handler(store, receipt)(w, r, account)
		return w
	}
	w := mux("/", ids[0], true, acct)
	if w.Code != 200 {
		t.Fatalf("old exact receipt: %d %s", w.Code, w.Body.String())
	}
	other := acct
	other.ID = uuid.NewString()
	if w := mux("/", ids[0], true, other); w.Code != 404 {
		t.Fatalf("cross-account exact: %d", w.Code)
	}
	if w := mux("/", dep.ID, false, other); w.Code != 404 {
		t.Fatalf("cross-account history: %d", w.Code)
	}
	w = mux("/?limit=50", dep.ID, false, acct)
	var page api.MCPAdmissionHistoryResponse
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 50 || page.NextCursor == "" {
		t.Fatalf("page one: %+v %s", page, w.Body.String())
	}
	w = mux("/?limit=50&cursor="+page.NextCursor, dep.ID, false, acct)
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	// Clear omitted fields when decoding a new response.
	var last api.MCPAdmissionHistoryResponse
	_ = json.Unmarshal(w.Body.Bytes(), &last)
	if len(last.Items) != 25 || last.NextCursor != "" || last.Items[24].ID != ids[0] {
		t.Fatalf("last page: %+v", last)
	}
	w = mux("/?outcome=rejected", dep.ID, false, acct)
	var filtered api.MCPAdmissionHistoryResponse
	_ = json.Unmarshal(w.Body.Bytes(), &filtered)
	if len(filtered.Items) != 0 {
		t.Fatal("outcome filter ignored")
	}

}
func TestParameters(t *testing.T) {
	for _, query := range []string{"limit=0", "limit=51", "cursor=-1", "cursor=bad", "outcome=private"} {
		r := httptest.NewRequest("GET", "/?"+query, nil)
		if _, _, _, err := parameters(r); err == nil {
			t.Fatalf("accepted %s", query)
		}
	}
}
