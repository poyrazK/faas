package liveproxy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/livequery/liveproto"
)

var (
	keyA = "gdsk_" + strings.Repeat("A", 43)
	keyB = "gdsk_" + strings.Repeat("b", 43)
)

func keyHash(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

func accessRoute() Route {
	noSubjects := false
	return Route{Database: "db_1", DataAPIRole: "shop_api", Access: &api.DataAPIAccessConfig{Version: 1, Schemas: []string{"api", "catalog"},
		PublicRead: []string{"api.products", "api.categories"},
		ServerKeys: []api.DataAPIServerKey{
			{Slot: "a", SHA256: keyHash(keyA)},
			{Slot: "b", SHA256: keyHash(keyB), Permissions: &api.DataAPIServerKeyPermissionSet{Access: "read", Relations: []string{"api.orders"}, Subjects: &noSubjects}},
		}}}
}

// A read with a user token reads only schemas the Data API exposes: the
// binding login may hold grants on a private schema the Data API keeps
// unexposed (the support template's base tables behind security-invoker views).
func TestAdmitTokenSchemaFollowsTheDataAPIExposure(t *testing.T) {
	reported := accessRoute() // exposes api, catalog
	unreported := Route{Database: "db_1", DataAPIRole: "shop_api"}
	for _, tc := range []struct {
		name       string
		route      Route
		schema     string
		wantSchema string
		refused    bool
	}{
		{"default schema", reported, "", "api", false},
		{"second exposed schema", reported, "catalog", "catalog", false},
		{"private schema", reported, "support_private", "", true},
		{"system schema", reported, "pg_catalog", "", true},
		{"unreported default", unreported, "", "", false},
		{"unreported api", unreported, "api", "api", false},
		{"unreported private schema", unreported, "support_private", "", true},
	} {
		f := liveproto.ClientFrame{Type: liveproto.TypeSubscribe, Subscription: "s", Database: "db_1", Rest: "cases?select=id", Schema: tc.schema, Token: "jwt"}
		code, msg := admitTokenSchema(tc.route, &f)
		if (code != "") != tc.refused || code != "" && code != liveproto.CodeForbidden {
			t.Errorf("%s: code=%q msg=%q, refused want %v", tc.name, code, msg, tc.refused)
		}
		if !tc.refused && f.Schema != tc.wantSchema {
			t.Errorf("%s: compiles schema %q, want %q", tc.name, f.Schema, tc.wantSchema)
		}
	}
}

// adr: 20261016151947 — anonymous and server-key live reads follow the Data
// API's reported rules and reach the hub as claims, never as the key.
func TestAdmitKeylessFollowsTheDataAPIRules(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	admit := func(f liveproto.ClientFrame, route Route) (liveproto.ClientFrame, string) {
		f.Type = liveproto.TypeSubscribe
		code, _ := admitKeyless(route, &f, f.Rest, f.Schema, now)
		return f, code
	}
	got, code := admit(liveproto.ClientFrame{Anonymous: true, Rest: "products?select=id,categories(name)"}, accessRoute())
	if code != "" || got.Access != liveproto.AccessAnonymous || string(got.Claims) != `{"gregale_access":"anonymous"}` || got.Anonymous || got.Schema != "api" || got.ClaimsExpires != now.Add(api.LiveQueryKeySessionMax).Unix() {
		t.Fatalf("anonymous public read: %q %+v", code, got)
	}
	got, code = admit(liveproto.ClientFrame{ServerKey: keyA, Subject: "user-7", Rest: "notes?select=id"}, accessRoute())
	if code != "" || got.Access != liveproto.AccessServer || string(got.Claims) != `{"gregale_access":"server","gregale_server_key":"a","sub":"user-7"}` || got.ServerKey != "" || got.Subject != "" {
		t.Fatalf("server key: %q %+v", code, got)
	}
	if got, code = admit(liveproto.ClientFrame{ServerKey: keyB, Rest: "orders"}, accessRoute()); code != "" || string(got.Claims) != `{"gregale_access":"server","gregale_server_key":"b"}` {
		t.Fatalf("restricted key within its relations: %q %+v", code, got)
	}
	for name, tc := range map[string]struct {
		frame liveproto.ClientFrame
		route Route
		code  string
	}{
		"unlisted relation":    {liveproto.ClientFrame{Anonymous: true, Rest: "orders"}, accessRoute(), liveproto.CodeForbidden},
		"unlisted embedding":   {liveproto.ClientFrame{Anonymous: true, Rest: "products?select=id,orders(id)"}, accessRoute(), liveproto.CodeForbidden},
		"nested embedding":     {liveproto.ClientFrame{Anonymous: true, Rest: "products?select=id,categories(name,orders(id))"}, accessRoute(), liveproto.CodeForbidden},
		"other schema":         {liveproto.ClientFrame{Anonymous: true, Rest: "products", Schema: "catalog"}, accessRoute(), liveproto.CodeForbidden},
		"unexposed schema":     {liveproto.ClientFrame{Anonymous: true, Rest: "products", Schema: "private"}, accessRoute(), liveproto.CodeForbidden},
		"no reported access":   {liveproto.ClientFrame{Anonymous: true, Rest: "products"}, Route{Database: "db_1", DataAPIRole: "shop_api"}, liveproto.CodeForbidden},
		"unknown key":          {liveproto.ClientFrame{ServerKey: "gdsk_" + strings.Repeat("z", 43), Rest: "notes"}, accessRoute(), liveproto.CodeSealRejected},
		"key outside its set":  {liveproto.ClientFrame{ServerKey: keyB, Rest: "orders?select=id,notes(id)"}, accessRoute(), liveproto.CodeForbidden},
		"key without subjects": {liveproto.ClientFrame{ServerKey: keyB, Subject: "user-7", Rest: "orders"}, accessRoute(), liveproto.CodeForbidden},
		"malformed read":       {liveproto.ClientFrame{Anonymous: true, Rest: "products?select=("}, accessRoute(), liveproto.CodeInvalidQuery},
	} {
		if _, code := admit(tc.frame, tc.route); code != tc.code {
			t.Errorf("%s: code %q, want %q", name, code, tc.code)
		}
	}
}

// fakeHub records the frames realtimed sends to a database's hub.
func fakeHub(t *testing.T) (string, chan liveproto.ClientFrame) {
	t.Helper()
	frames := make(chan liveproto.ClientFrame, 32)
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		for {
			_, data, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var f liveproto.ClientFrame
			if json.Unmarshal(data, &f) == nil {
				frames <- f
			}
		}
	}))
	t.Cleanup(server.Close)
	return "ws" + strings.TrimPrefix(server.URL, "http") + "/live", frames
}

func TestKeylessSubscriptionsReachTheHubAsClaimsAndRenewWithChecks(t *testing.T) {
	hub, toHub := fakeHub(t)
	p := New(nil, nil)
	defer p.Close()
	route := accessRoute()
	route.URL = hub
	var current = route
	router := func(_ context.Context, key RouteKey) (Route, error) {
		if key.Database == "db_1" {
			return current, nil
		}
		return Route{}, ErrNoRoute
	}
	toClient := make(chan []byte, 32)
	s := p.Attach(router, func(f []byte) error { toClient <- f; return nil })
	defer s.Close()
	send := func(f liveproto.ClientFrame) {
		data, _ := json.Marshal(f)
		s.Receive(context.Background(), data)
	}
	next := func() liveproto.ClientFrame {
		t.Helper()
		select {
		case f := <-toHub:
			return f
		case <-time.After(5 * time.Second):
			t.Fatal("no frame reached the hub")
		}
		return liveproto.ClientFrame{}
	}
	clientError := func() liveproto.Error {
		t.Helper()
		deadline := time.After(5 * time.Second)
		for {
			select {
			case data := <-toClient:
				var e liveproto.Error
				if json.Unmarshal(data, &e) == nil && e.Type == liveproto.TypeError && e.Code != liveproto.CodeUnavailable {
					return e
				}
			case <-deadline:
				t.Fatal("no error reached the client")
			}
		}
	}

	send(liveproto.ClientFrame{Type: liveproto.TypeSubscribe, Subscription: "public", Database: "db_1", Rest: "products?select=id", Anonymous: true})
	f := next()
	if f.Access != liveproto.AccessAnonymous || f.Role != "shop_api" || string(f.Claims) != `{"gregale_access":"anonymous"}` || f.Anonymous || f.Schema != "api" {
		t.Fatalf("anonymous subscribe reached the hub as %+v", f)
	}
	send(liveproto.ClientFrame{Type: liveproto.TypeSubscribe, Subscription: "backend", Database: "db_1", Rest: "notes?select=id", ServerKey: keyA})
	raw, _ := json.Marshal(next())
	if strings.Contains(string(raw), "gdsk_") || !strings.Contains(string(raw), `"access":"server"`) {
		t.Fatalf("server-key subscribe reached the hub as %s", raw)
	}

	send(liveproto.ClientFrame{Type: liveproto.TypeRenew, Subscription: "public", ServerKey: keyA})
	if e := clientError(); e.Code != liveproto.CodeRenewMismatch || e.Closed {
		t.Fatalf("renewing an anonymous read with a key: %+v", e)
	}
	send(liveproto.ClientFrame{Type: liveproto.TypeRenew, Subscription: "public", Anonymous: true})
	if f := next(); f.Type != liveproto.TypeRenew || f.Access != liveproto.AccessAnonymous || f.Database != "" || f.Rest != "" {
		t.Fatalf("anonymous renewal reached the hub as %+v", f)
	}
	// The owner made products private: the next renewal is refused.
	narrowed := route
	narrowed.Access = &api.DataAPIAccessConfig{Version: 1, Schemas: []string{"api"}}
	current = narrowed
	send(liveproto.ClientFrame{Type: liveproto.TypeRenew, Subscription: "public", Anonymous: true})
	if e := clientError(); e.Code != liveproto.CodeForbidden {
		t.Fatalf("renewal after the allowlist narrowed: %+v", e)
	}
}
