package gateway

// adr: 20261125230541 — an MCP JSON-RPC request the consumer's plan refuses
// gets a JSON-RPC answer carrying the upgrade link: URL elicitation when
// the client declared it, a tool error or JSON-RPC error otherwise.

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const urlCapsMeta = `"_meta":{"io.modelcontextprotocol/clientCapabilities":{"elicitation":{"url":{}}}}`

func TestMCPPlanRefusal(t *testing.T) {
	app := App{ID: "app-1", AccountID: "acct-1"}
	link := "https://api.example/v1/consumer-signup/app/upgrade?token=t"
	type answer struct {
		Result *struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
			IsError bool `json:"isError"`
		} `json:"result"`
		Error *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
			Data    struct {
				Code         string `json:"code"`
				UpgradeURL   string `json:"upgrade_url"`
				Elicitations []struct {
					Mode, ElicitationID, URL, Message string
				} `json:"elicitations"`
			} `json:"data"`
		} `json:"error"`
		ID json.RawMessage `json:"id"`
	}
	refuse := func(t *testing.T, scope, linkURL, body string, mark bool) (*httptest.ResponseRecorder, answer) {
		t.Helper()
		store := &fakeConsumerPlanStore{policy: ConsumerPlanPolicy{MaxUnitsPerMonth: 10, MaxRequestsPerMinute: 5},
			decision: ConsumerPlanDecision{Scope: scope, Limit: 10, Observed: 10, RetryAfterSeconds: 42}}
		h := (&Handler{apiMonetization: monetizationOn}).WithConsumerPlanStore(store).WithUpgradeLinks(&fakeUpgradeLinker{url: linkURL})
		r := planRequest("MCP /tools/search")
		r.Body = io.NopCloser(strings.NewReader(body))
		if mark {
			h.markMCPRequest(r, app)
		}
		w := httptest.NewRecorder()
		if h.enforceConsumerPlan(w, r, &statusRecorder{}, app, false) {
			t.Fatal("admitted")
		}
		var a answer
		if refused := r.Context().Value(mcpPlanRefusedKey{}) == true; refused != (w.Code == http.StatusOK) {
			t.Fatalf("telemetry mark = %v for status %d", refused, w.Code)
		}
		if w.Code == http.StatusOK {
			if err := json.Unmarshal(w.Body.Bytes(), &a); err != nil {
				t.Fatalf("body %s: %v", w.Body, err)
			}
		}
		return w, a
	}
	call := `{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"search"}}`
	callURL := `{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"search",` + urlCapsMeta + `}}`

	t.Run("tool error names the limit and link", func(t *testing.T) {
		w, a := refuse(t, "month", link, call, true)
		if w.Code != http.StatusOK || a.Result == nil || !a.Result.IsError || string(a.ID) != "7" ||
			!strings.Contains(a.Result.Content[0].Text, "10 units per month") || !strings.Contains(a.Result.Content[0].Text, link) {
			t.Fatalf("%d %s", w.Code, w.Body)
		}
		if w.Header().Get(api.ErrorCodeHeader) != api.CodeConsumerPlanLimitExceeded || w.Header().Get("Retry-After") != "42" {
			t.Fatalf("headers %v", w.Header())
		}
	})
	t.Run("URL elicitation when declared", func(t *testing.T) {
		_, a := refuse(t, "month", link, callURL, true)
		if a.Error == nil || a.Error.Code != mcpURLElicitationRequired || len(a.Error.Data.Elicitations) != 1 {
			t.Fatalf("%+v", a)
		}
		e := a.Error.Data.Elicitations[0]
		if e.Mode != "url" || e.URL != link || e.ElicitationID == "" || !strings.Contains(e.Message, "units per month") || a.Error.Data.UpgradeURL != link {
			t.Fatalf("elicitation %+v", e)
		}
	})
	t.Run("no link falls back to the tool error", func(t *testing.T) {
		_, a := refuse(t, "month", "", callURL, true)
		if a.Result == nil || !a.Result.IsError || strings.Contains(a.Result.Content[0].Text, "Upgrade") {
			t.Fatalf("%+v", a)
		}
	})
	t.Run("other methods get a JSON-RPC error", func(t *testing.T) {
		_, a := refuse(t, "minute", link, `{"jsonrpc":"2.0","id":"x","method":"tools/list"}`, true)
		if a.Error == nil || a.Error.Code != mcpPlanRefusedCode || a.Error.Data.UpgradeURL != link || a.Error.Data.Code != api.CodeConsumerPlanLimitExceeded ||
			!strings.Contains(a.Error.Message, "clears in 42 s") || string(a.ID) != `"x"` {
			t.Fatalf("%+v", a)
		}
	})
	for name, tc := range map[string]struct {
		body string
		mark bool
	}{
		"notification":     {`{"jsonrpc":"2.0","method":"notifications/initialized"}`, true},
		"ambiguous id":     {`{"jsonrpc":"2.0","id":1,"ID":2,"method":"tools/call","params":{"name":"search"}}`, true},
		"batch":            {`[` + call + `]`, true},
		"not an MCP route": {call, false},
	} {
		t.Run(name+" keeps the 429", func(t *testing.T) {
			if w, _ := refuse(t, "month", link, tc.body, tc.mark); w.Code != http.StatusTooManyRequests {
				t.Fatalf("%d %s", w.Code, w.Body)
			}
		})
	}
}

// A session whose initialize declared URL elicitation gets it on later
// requests that carry no capabilities of their own.
func TestMCPSessionURLElicitation(t *testing.T) {
	h := &Handler{}
	app := App{ID: "app-1"}
	r := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"capabilities":{"elicitation":{"url":{}}}}}`))
	h.markMCPRequest(r, app)
	rec := httptest.NewRecorder()
	w := h.wrapMCPInitialize(rec, r)
	if w == http.ResponseWriter(rec) {
		t.Fatal("initialize not wrapped")
	}
	w.Header().Set("Mcp-Session-Id", "s-1")
	_, _ = w.Write([]byte(`{}`))

	later := func(appID, session string) bool {
		r := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"x"}}`))
		r.Header.Set("Mcp-Session-Id", session)
		h.markMCPRequest(r, App{ID: appID})
		req, _ := r.Context().Value(mcpRequestKey{}).(mcpRequest)
		return req.urlElicitation
	}
	if !later("app-1", "s-1") || later("app-1", "s-2") || later("app-2", "s-1") {
		t.Fatal("session capability not scoped to the app's session")
	}

	plain := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"capabilities":{"elicitation":{}}}}`))
	h.markMCPRequest(plain, app)
	if h.wrapMCPInitialize(rec, plain) != http.ResponseWriter(rec) {
		t.Fatal("initialize without URL elicitation wrapped")
	}
}

// Sessions are bounded per app, so one app's callers cannot crowd out
// another's; expired sessions and apps make room.
func TestMCPSessionCapabilitiesBounded(t *testing.T) {
	var c mcpSessionCapabilities
	now := time.Date(2026, 11, 26, 9, 0, 0, 0, time.UTC)
	c.remember("other", "o1", now)
	for i := range mcpSessionsPerApp + 10 {
		c.remember("app", fmt.Sprint("s", i), now)
	}
	if len(c.apps["app"]) != mcpSessionsPerApp || !c.urlElicitation("app", fmt.Sprint("s", mcpSessionsPerApp+9), now) {
		t.Fatalf("full app holds %d sessions or dropped the newest", len(c.apps["app"]))
	}
	if !c.urlElicitation("other", "o1", now) {
		t.Fatal("one app's sessions crowded out another's")
	}
	later := now.Add(mcpSessionTTL)
	c.remember("app", "new", later)
	if !c.urlElicitation("app", "new", later) || len(c.apps["app"]) != 1 {
		t.Fatalf("expired sessions not dropped: %d", len(c.apps["app"]))
	}
	for i := len(c.apps); i < oauthConsumerCacheMax; i++ {
		c.remember(fmt.Sprint("app", i), "s", now)
	}
	c.remember("late", "s", now)
	if c.urlElicitation("late", "s", now) {
		t.Fatal("app admitted past the app bound with no expired app")
	}
	c.remember("late", "s", later)
	if !c.urlElicitation("late", "s", later) {
		t.Fatal("expired apps not dropped for a new one")
	}
}
