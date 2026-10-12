package apidserver_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/onebox-faas/faas/pkg/durableentity"
	entitysocket "github.com/onebox-faas/faas/pkg/durableentity/socket"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestDurableEntitySocketBrowserGrantScopeAndRevocation(t *testing.T) {
	e, app, dispatch, bucket := entityAPIFixture(t, false)
	enabled := true
	if _, err := e.store.UpdateApp(t.Context(), app.ID, state.UpdateAppParams{SetWebSocketEnabled: true, WebSocketEnabled: &enabled}); err != nil {
		t.Fatal(err)
	}
	e.s.Durable().Sockets = entitysocket.New()
	e.s.Durable().SessionGrants = &entitysocket.Grants{Store: bucket}
	session := uuid.NewString()
	input := map[string]any{"namespace": "counters", "key": "a", "session_id": session, "generation": 0, "origin": "https://example.com", "commands": []string{"open", "message", "status"}, "ttl_seconds": 60}
	response := e.do(t, "POST", "/v1/apps/"+app.Slug+"/entities/session-grants", input, nil)
	var issued struct {
		ID         string `json:"grant_id"`
		Credential string `json:"credential"`
	}
	if response.Code != 201 || json.Unmarshal(response.Body.Bytes(), &issued) != nil {
		t.Fatalf("mint: %d %s", response.Code, response.Body.String())
	}
	server := httptest.NewServer(e.h)
	defer server.Close()
	dial := func(origin string) (*websocket.Conn, *http.Response, error) {
		d := websocket.Dialer{Subprotocols: []string{entitysocket.BrowserProtocol, entitysocket.CredentialProtocol + issued.Credential}}
		return d.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/v1/apps/"+app.Slug+"/entities/session-connect", http.Header{"Origin": []string{origin}})
	}
	c, rejected, err := dial("https://attacker.example")
	if rejected != nil {
		rejected.Body.Close()
	}
	if err == nil {
		c.Close()
		t.Fatal("foreign origin accepted")
	} else if rejected == nil || rejected.StatusCode != 403 {
		t.Fatalf("foreign origin: %v %v", rejected, err)
	}
	otherSession := uuid.NewString()
	for _, frame := range []entitysocket.Frame{
		{ProtocolVersion: 1, RequestID: "session/g1/" + session + "/open", Payload: json.RawMessage(`{"kind":"open","session_id":"` + session + `","generation":1}`)},
		{ProtocolVersion: 1, RequestID: "session/g0/" + otherSession + "/open", Payload: json.RawMessage(`{"kind":"open","session_id":"` + otherSession + `","generation":0}`)},
		{ProtocolVersion: 1, RequestID: "session/g0/reclaim/" + uuid.NewString(), Payload: json.RawMessage(`{"kind":"reclaim","generation":0,"operation_id":"` + uuid.NewString() + `"}`)},
	} {
		c, response, err := dial("https://example.com")
		if response != nil {
			response.Body.Close()
		}
		if err != nil {
			t.Fatal(err)
		}
		if err = c.WriteJSON(frame); err != nil {
			t.Fatal(err)
		}
		var denied entitysocket.Reply
		if err = c.ReadJSON(&denied); err != nil {
			t.Fatal(err)
		}
		c.Close()
		if denied.Status != 403 || dispatch.calls.Load() != 0 {
			t.Fatal("scope escape dispatched", denied)
		}
	}
	conn, handshake, err := dial("https://example.com")
	if handshake != nil {
		handshake.Body.Close()
	}
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if conn.Subprotocol() != entitysocket.BrowserProtocol {
		t.Fatal("credential echoed in protocol")
	}
	// The fixture guest is a counter. This verifies host admission rather
	// than lifecycle semantics or native infrastructure.
	open := entitysocket.Frame{ProtocolVersion: 1, RequestID: "session/g0/" + session + "/open", Payload: json.RawMessage(`{"kind":"open","session_id":"` + session + `","generation":0}`)}
	if err = conn.WriteJSON(open); err != nil {
		t.Fatal(err)
	}
	var reply entitysocket.Reply
	if err = conn.ReadJSON(&reply); err != nil {
		t.Fatal(err)
	}
	if dispatch.calls.Load() != 1 {
		t.Fatalf("permitted command not dispatched: %+v", reply)
	}
	revoke := e.do(t, "DELETE", "/v1/apps/"+app.Slug+"/entities/session-grants/"+issued.ID, nil, nil)
	if revoke.Code != 204 {
		t.Fatal(revoke.Code, revoke.Body.String())
	}
	if err = conn.WriteJSON(open); err != nil {
		t.Fatal(err)
	}
	if err = conn.ReadJSON(&reply); err != nil {
		t.Fatal(err)
	}
	if reply.Status != 403 || dispatch.calls.Load() != 1 {
		t.Fatal("revocation did not fence next dispatch", reply)
	}
	c, deniedResponse, err := dial("https://example.com")
	if deniedResponse != nil {
		deniedResponse.Body.Close()
	}
	if err == nil {
		c.Close()
		t.Fatal("revoked grant reconnected")
	}
}
func TestDurableEntitySocketBrowserGrantCommandsAndStorage(t *testing.T) {
	_, _, _, bucket := entityAPIFixture(t, false)
	grants := &entitysocket.Grants{Store: bucket}
	session := uuid.NewString()
	g, token, err := grants.Mint(t.Context(), entitysocket.Grant{Entity: durableentity.ID{AccountID: "account", AppID: "app", Namespace: "rooms", Key: "document"}, IssuerID: "issuer", SessionID: session, Origin: "https://example.com", Commands: []string{"open", "message", "status"}}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = grants.Authenticate(t.Context(), token, "https://example.com"); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		payload, id string
		allow       bool
	}{
		{`{"kind":"open","session_id":"` + session + `","generation":0}`, "session/g0/" + session + "/open", true},
		{`{"kind":"open","session_id":"` + session + `","generation":1}`, "session/g1/" + session + "/open", false},
		{`{"kind":"open","session_id":"` + uuid.NewString() + `","generation":0}`, "session/g0/" + session + "/open", false},
		{`{"kind":"advance","generation":0,"tick":999,"operation_id":"` + uuid.NewString() + `"}`, "control", false},
		{`{"kind":"reclaim","generation":0,"operation_id":"` + uuid.NewString() + `"}`, "control", false},
		{`{"kind":"close","session_id":"` + session + `","generation":0,"sequence":1}`, "session/g0/" + session + "/close/1", false},
		{`{"kind":"message","session_id":"` + session + `","generation":0,"sequence":1,"payload":null}`, "session/g0/" + session + "/message/1", true},
		{`{"kind":"open","session_id":"` + session + `"}`, "session/" + session + "/open", false},
	} {
		if g.Authorize(entitysocket.Frame{RequestID: test.id, Payload: json.RawMessage(test.payload)}) != test.allow {
			t.Fatal(test)
		}
	}
	if err = grants.Revoke(t.Context(), g.ID, "other-account", "app"); err == nil {
		t.Fatal("cross account revoke")
	}
	g.ExpiresAt = time.Now().Add(-time.Second)
	body, _ := json.Marshal(g)
	_, version, err := bucket.Get(t.Context(), "session-grants/v1/"+g.ID, 16384)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = bucket.Put(t.Context(), "session-grants/v1/"+g.ID, body, version); err != nil {
		t.Fatal(err)
	}
	if _, err = grants.Authenticate(t.Context(), token, "https://example.com"); err == nil {
		t.Fatal("expired grant accepted")
	}
	if strings.Contains(string(body), token) {
		t.Fatal("plaintext token persisted")
	}
}
func TestDurableEntitySocketBrowserFlagRequiresServiceSockets(t *testing.T) {
	e := entityAPISetup(t)
	if err := e.s.ConfigureDurableEntities(t.Context(), func(name string) string {
		if name == "FAAS_DURABLE_ENTITY_BROWSER_SESSIONS_ENABLED" {
			return "1"
		}
		return ""
	}); err == nil {
		t.Fatal("browser preview enabled without sockets")
	}
}

func TestDurableEntitySocketBrowserIssuerRevocation(t *testing.T) {
	e, app, dispatch, bucket := entityAPIFixture(t, false)
	enabled := true
	if _, err := e.store.UpdateApp(t.Context(), app.ID, state.UpdateAppParams{SetWebSocketEnabled: true, WebSocketEnabled: &enabled}); err != nil {
		t.Fatal(err)
	}
	e.s.Durable().Sockets = entitysocket.New()
	e.s.Durable().SessionGrants = &entitysocket.Grants{Store: bucket}
	keys, err := e.store.ListAPIKeys(t.Context(), e.acct.ID)
	if err != nil || len(keys) != 1 {
		t.Fatal(err)
	}
	session := uuid.NewString()
	_, token, err := e.s.Durable().SessionGrants.Mint(t.Context(), entitysocket.Grant{Entity: durableentity.ID{AccountID: e.acct.ID, AppID: app.ID, EnvironmentID: app.ID, Namespace: "counters", Key: "a"}, IssuerID: keys[0].ID, SessionID: session, Origin: "https://example.com", Commands: []string{"open"}}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(e.h)
	defer server.Close()
	d := websocket.Dialer{Subprotocols: []string{entitysocket.BrowserProtocol, entitysocket.CredentialProtocol + token}}
	conn, handshake, err := d.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/v1/apps/"+app.Slug+"/entities/session-connect", http.Header{"Origin": []string{"https://example.com"}})
	if handshake != nil {
		handshake.Body.Close()
	}
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err = e.store.DeleteAPIKeyReturning(t.Context(), e.acct.ID, keys[0].ID); err != nil {
		t.Fatal(err)
	}
	if err = conn.WriteJSON(entitysocket.Frame{ProtocolVersion: 1, RequestID: "session/g0/" + session + "/open", Payload: json.RawMessage(`{"kind":"open","session_id":"` + session + `","generation":0}`)}); err != nil {
		t.Fatal(err)
	}
	var denied entitysocket.Reply
	if err = conn.ReadJSON(&denied); err != nil {
		t.Fatal(err)
	}
	if denied.Status != 403 || dispatch.calls.Load() != 0 {
		t.Fatal("revoked issuer dispatched", denied)
	}
}
