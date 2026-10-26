package outboundai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// Integrations are account-scoped and company apps share their creator's
// AccountID: a creator removed from a company keeps the integration, but no
// longer reads, rates or replays the company app's calls through it.
func TestRemovedCreatorLosesCompanyAppAICalls(t *testing.T) {
	service, store, acct, integrationID := newTestService(t)
	ctx := context.Background()
	if w := serve(service.PutPolicy, acct, http.MethodPut, "/v1/outbound/integrations/x/ai", integrationID,
		`{"ai_provider":"openai","request_log_enabled":true,"request_log_content":true}`); w.Code != http.StatusNoContent {
		t.Fatalf("policy = %d %s", w.Code, w.Body.String())
	}
	owner, err := store.CreateAccount(ctx, "owner-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	org, err := store.CreateOrg(ctx, state.Org{Slug: "ai-co", Name: "AI Co", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	for member, role := range map[string]state.OrgRole{owner.ID: state.OrgRoleOwner, acct.ID: state.OrgRoleDeveloper} {
		if err := store.AddOrgMember(ctx, org.ID, member, role, nil); err != nil {
			t.Fatal(err)
		}
	}
	company, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, OrgID: org.ID, Slug: "ai-co-api", Type: state.AppTypeApp, Runtime: "node22"})
	if err != nil {
		t.Fatal(err)
	}
	personal, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "ai-mine", Type: state.AppTypeApp, Runtime: "node22"})
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC().Truncate(time.Second)
	calls := map[string]string{company.ID: uuid.NewString(), personal.ID: uuid.NewString()}
	for app, call := range calls {
		store.SeedOutboundAITrace(integrationID, state.OutboundAITrace{TraceID: "trace-" + app, Caller: app, FirstAt: at, LastAt: at, Calls: 1})
		store.SeedOutboundAIRequestLog(integrationID, state.OutboundAIRequestLogEntry{ID: call, CreatedAt: at, Caller: app,
			TraceID: "trace-" + app, Model: "gpt-5", Status: 200, Outcome: "known", Attempts: 1})
		store.SeedOutboundAIRequestContent(integrationID, state.OutboundAIRequestContent{LogID: call, CreatedAt: at,
			RequestPath: "/v1/chat/completions", RequestBody: []byte(`{"model":"gpt-5","messages":[]}`)})
	}
	replayBody, err := json.Marshal(api.CreateOutboundAIReplayRequest{Targets: []api.OutboundAIReplayTarget{{IntegrationID: integrationID}}})
	if err != nil {
		t.Fatal(err)
	}
	call := func(handler func(http.ResponseWriter, *http.Request, state.Account), method string, values map[string]string, body string) int {
		r := httptest.NewRequest(method, "/v1/outbound/integrations/x/ai", strings.NewReader(body))
		r.SetPathValue("integration", integrationID)
		for key, value := range values {
			r.SetPathValue(key, value)
		}
		w := httptest.NewRecorder()
		handler(w, r, acct)
		return w.Code
	}
	listed := func(handler func(http.ResponseWriter, *http.Request, state.Account)) map[string]bool {
		t.Helper()
		w := serve(handler, acct, http.MethodGet, "/v1/outbound/integrations/x/ai", integrationID, "")
		var page struct {
			Items []struct {
				AppID  *string `json:"app_id"`
				Caller string  `json:"caller"`
			} `json:"items"`
		}
		if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &page) != nil {
			t.Fatalf("list = %d %s", w.Code, w.Body.String())
		}
		seen := map[string]bool{}
		for _, item := range page.Items {
			if item.AppID != nil {
				seen[*item.AppID] = true
			}
			if item.Caller != "" {
				seen[item.Caller] = true
			}
		}
		return seen
	}
	// reads checks every surface for one app's call: the trace list, the
	// request log, the trace, the captured content, a rating and a replay.
	reads := func(app string, want bool) {
		t.Helper()
		if got := listed(service.ListTraces)[app]; got != want {
			t.Errorf("trace list shows %s = %v, want %v", app, got, want)
		}
		if got := listed(service.ListRequestLog)[app]; got != want {
			t.Errorf("request log shows %s = %v, want %v", app, got, want)
		}
		for name, code := range map[string]int{
			"trace":    call(service.GetTrace, http.MethodGet, map[string]string{"trace": "trace-" + app}, ""),
			"content":  call(service.GetRequestContent, http.MethodGet, map[string]string{"request": calls[app]}, ""),
			"feedback": call(service.PutRequestFeedback, http.MethodPut, map[string]string{"request": calls[app]}, `{"rating":"bad"}`),
			"replay":   call(service.CreateReplay, http.MethodPost, map[string]string{"request": calls[app]}, string(replayBody)),
		} {
			if hidden := code == http.StatusNotFound; hidden == want {
				t.Errorf("%s of %s = %d, visible want %v", name, app, code, want)
			}
		}
	}
	reads(company.ID, true)
	reads(personal.ID, true)
	if err := store.RemoveOrgMember(ctx, org.ID, acct.ID); err != nil {
		t.Fatal(err)
	}
	reads(company.ID, false)
	reads(personal.ID, true)
}
