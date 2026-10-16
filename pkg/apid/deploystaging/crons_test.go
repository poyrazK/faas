package deploystaging

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestCronStagingHTTP(t *testing.T) {
	store := state.NewMemStore()
	acct, err := store.CreateAccount(t.Context(), "cron-api@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(t.Context(), state.App{AccountID: acct.ID, Slug: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	h := CronHandlers{Store: store, LoadApp: func(w http.ResponseWriter, r *http.Request, a state.Account, slug string) (state.App, bool) {
		return app, true
	}}
	req := api.DeploymentCronStagingRequest{SessionID: strings.Repeat("a", 32), Operation: "stage", ExpectedAppID: app.ID, Before: []api.DeploymentCronConfig{}, Desired: []api.DeploymentCronConfig{{ID: uuid.NewString(), Schedule: "0 * * * *", Path: "/tick", Enabled: true, Timezone: "UTC"}}}
	post := func(a state.Account, body any) *httptest.ResponseRecorder {
		data, _ := json.Marshal(body)
		r := httptest.NewRequest("POST", "/v1/apps/demo/deployment-cron-staging", bytes.NewReader(data))
		r.SetPathValue("slug", "demo")
		w := httptest.NewRecorder()
		h.Apply(w, r, a)
		return w
	}
	for _, op := range []string{"stage", "stage", "restore", "restore"} {
		req.Operation = op
		w := post(acct, req)
		if w.Code != 200 {
			t.Fatalf("%s: %d %s", op, w.Code, w.Body)
		}
	}
	req.Operation = "stage"
	if w := post(acct, req); w.Code != 409 {
		t.Fatalf("late stage=%d", w.Code)
	}
	free := acct
	free.Plan = api.PlanFree
	if w := post(free, req); w.Code != 402 {
		t.Fatalf("free=%d", w.Code)
	}
	other := acct
	other.ID = uuid.NewString()
	if w := post(other, req); w.Code != 404 {
		t.Fatalf("account isolation=%d", w.Code)
	}
	invalid := map[string]any{}
	data, _ := json.Marshal(req)
	_ = json.Unmarshal(data, &invalid)
	invalid["command"] = []string{"secret"}
	if w := post(acct, invalid); w.Code != 422 && w.Code != 400 {
		t.Fatalf("unknown field=%d", w.Code)
	}
}
