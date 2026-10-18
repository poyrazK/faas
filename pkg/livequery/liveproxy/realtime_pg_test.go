package liveproxy

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/onebox-faas/faas/pkg/livequery"
	"github.com/onebox-faas/faas/pkg/livequery/liveproto"
	"github.com/onebox-faas/faas/pkg/livequery/liveserver"
	"github.com/onebox-faas/faas/pkg/livequery/seal"
	"github.com/onebox-faas/faas/pkg/realtime"
	"github.com/onebox-faas/faas/pkg/state"
)

type allowJWT struct{}

func (allowJWT) Authorize(context.Context, string, realtime.AuthPolicy) (string, error) {
	return "user-1", nil
}

type noHistory struct{}

func (noHistory) ReadChannelHistory(context.Context, string, string, int64, int) (state.ManagedRealtimeChannelHistory, error) {
	return state.ManagedRealtimeChannelHistory{}, nil
}

// hooks accept the connection; live frames never reach channel policy.
type hooks struct{ realtime.NopHooks }

func (hooks) AuthorizeChannel(context.Context, realtime.Event) (bool, error) { return true, nil }

// adr: 20261016151947 — the whole path: a browser's v2 realtime connection carries a
// sealed live query through realtimed's proxy to the database's hub and
// back, with every frame checked by the reference client.
func TestRealtimeCarriesLiveQueries(t *testing.T) {
	dsn, pool := openDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	for _, sql := range []string{
		`CREATE TABLE messages (id bigint PRIMARY KEY, channel_id bigint NOT NULL, body text)`,
	} {
		if _, err := pool.Exec(ctx, sql); err != nil {
			t.Fatal(err)
		}
	}
	reader := readerRole(t, pool, "messages")
	if err := livequery.Install(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `CREATE PUBLICATION gregale_live FOR ALL TABLES`); err != nil {
		t.Fatal(err)
	}
	capture, err := livequery.OpenCapture(ctx, dsn, fmt.Sprintf("lqr_%d", time.Now().UnixNano()), "gregale_live")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = capture.Close() }()
	engine := livequery.NewEngine(pool, capture)
	engine.VerifyEachRound = true
	text, _ := seal.GenerateSecret()
	secret, _ := seal.ParseSecret(text)
	ring, _ := seal.NewKeyring(seal.Key{Database: "db_1", Secret: secret})
	hub := liveserver.New(liveserver.Config{Database: "db_1", Keyring: ring, Engine: engine, Idle: 5 * time.Millisecond, MaxPerConnection: 1000})
	stopped := make(chan error, 1)
	go func() { stopped <- hub.Run(ctx) }()
	defer func() { cancel(); <-stopped }()
	hubMux := http.NewServeMux()
	hubMux.Handle("/live", liveserver.WebSocketTrusting(hub, func(*http.Request) bool { return true }, func(*http.Request) bool { return true }))
	hubSrv := httptest.NewServer(hubMux)
	defer hubSrv.Close()

	proxy := New(nil, nil)
	defer proxy.Close()
	m := realtime.NewManager(realtime.Config{JWTAuthorizer: allowJWT{}, ResumePreview: true, HistoryReader: noHistory{}, Live: ForRealtime(proxy)}, hooks{})
	defer func() { _ = m.Close() }()
	if err := m.RegisterEndpoint(realtime.Endpoint{
		ID: "ep", AppID: "app-1", AccountID: "acct-1", CallbackURL: "https://app.example",
		AllowedOrigins: []string{"https://app.example"},
		ClientAuth: realtime.AuthPolicy{Mode: realtime.AuthModeOIDCJWT, Issuer: "https://issuer.example",
			JWKSURL: "https://issuer.example/.well-known/jwks.json", Algorithms: []string{"RS256"}},
		LiveRoutes: []realtime.LiveRoute{{KID: secret.KID, Database: "db_1", URL: "ws" + strings.TrimPrefix(hubSrv.URL, "http") + "/live", SealedRole: reader}},
	}); err != nil {
		t.Fatal(err)
	}
	rtSrv := httptest.NewServer(m.Handler())
	defer rtSrv.Close()
	dialer := websocket.Dialer{Subprotocols: []string{realtime.ResumeSubprotocol}}
	ws, resp, err := dialer.Dial("ws"+strings.TrimPrefix(rtSrv.URL, "http")+realtime.ManagedPathPrefix+"ep",
		http.Header{"Authorization": {"Bearer test-jwt"}})
	if err != nil {
		var detail string
		if resp != nil {
			body := make([]byte, 512)
			n, _ := resp.Body.Read(body)
			detail = fmt.Sprintf("%s %s", resp.Status, body[:n])
		}
		t.Fatalf("%v: %s", err, detail)
	}
	_ = resp.Body.Close()
	defer ws.Close()

	channel := "1"
	sealed, err := secret.Seal(seal.Query{Database: "db_1", SQL: `SELECT id, body FROM messages WHERE channel_id = $1 ORDER BY id DESC LIMIT 2`,
		Params: []*string{&channel}}, time.Now(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(liveproto.ClientFrame{Type: liveproto.TypeSubscribe, Subscription: "feed", Sealed: sealed})
	if err := ws.WriteMessage(websocket.TextMessage, data); err != nil {
		t.Fatal(err)
	}
	result := &liveproto.Result{}
	until := func(what string, cond func() bool) {
		t.Helper()
		_ = ws.SetReadDeadline(time.Now().Add(30 * time.Second))
		for !cond() {
			_, data, err := ws.ReadMessage()
			if err != nil {
				t.Fatalf("waiting for %s: %v", what, err)
			}
			frame, err := liveproto.ParseServerFrame(data)
			if err != nil {
				t.Fatalf("bad frame %s: %v", data, err)
			}
			if err := result.Apply(frame); err != nil {
				t.Fatalf("client refused %s: %v", data, err)
			}
		}
	}
	until("the initial result", result.Ready)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO messages VALUES (1, 1, 'a'), (2, 1, 'b'), (3, 1, 'c'), (4, 2, 'other')`); err != nil {
		t.Fatal(err)
	}
	var xid uint64
	if err := tx.QueryRow(ctx, `SELECT pg_current_xact_id()::text::bigint`).Scan(&xid); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	until("the write", func() bool { return result.Sees(xid) })
	var got []string
	for _, r := range result.Rows() {
		got = append(got, *r.Values[0]+":"+*r.Values[1])
	}
	if strings.Join(got, ",") != "3:c,2:b" {
		t.Fatalf("feed %v", got)
	}

	// An endpoint whose app is bound to no live database (or whose account
	// is outside the rollout) has no live routes: realtimed answers the
	// live frame like an unknown one, and nothing reaches the proxy.
	unbound := realtime.Endpoint{
		ID: "unbound", AppID: "app-2", AccountID: "acct-1", CallbackURL: "https://app.example",
		AllowedOrigins: []string{"https://app.example"},
		ClientAuth: realtime.AuthPolicy{Mode: realtime.AuthModeOIDCJWT, Issuer: "https://issuer.example",
			JWKSURL: "https://issuer.example/.well-known/jwks.json", Algorithms: []string{"RS256"}},
	}
	if err := m.RegisterEndpoint(unbound); err != nil {
		t.Fatal(err)
	}
	other, resp, err := dialer.Dial("ws"+strings.TrimPrefix(rtSrv.URL, "http")+realtime.ManagedPathPrefix+"unbound",
		http.Header{"Authorization": {"Bearer test-jwt"}})
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	defer other.Close()
	frameOn := func(conn *websocket.Conn, subscription string) map[string]any {
		t.Helper()
		_ = conn.SetReadDeadline(time.Now().Add(30 * time.Second))
		for {
			_, data, err := conn.ReadMessage()
			if err != nil {
				t.Fatalf("waiting for %s: %v", subscription, err)
			}
			var f map[string]any
			if json.Unmarshal(data, &f) == nil && f["subscription"] == subscription {
				return f
			}
		}
	}
	data, _ = json.Marshal(liveproto.ClientFrame{Type: liveproto.TypeSubscribe, Subscription: "unbound", Sealed: sealed})
	if err := other.WriteMessage(websocket.TextMessage, data); err != nil {
		t.Fatal(err)
	}
	_ = other.SetReadDeadline(time.Now().Add(30 * time.Second))
	_, reply, err := other.ReadMessage()
	var unknown map[string]any
	if err != nil || json.Unmarshal(reply, &unknown) != nil || unknown["type"] != "error" || unknown["code"] != "invalid_frame" || unknown["subscription"] != nil {
		t.Fatalf("unbound endpoint answered %s, %v; want the unknown-frame error", reply, err)
	}

	// Binding the app (apid re-registers the endpoint with the route) applies
	// to the open connection.
	unbound.LiveRoutes = []realtime.LiveRoute{{KID: secret.KID, Database: "db_1", URL: "ws" + strings.TrimPrefix(hubSrv.URL, "http") + "/live", SealedRole: reader}}
	if err := m.RegisterEndpoint(unbound); err != nil {
		t.Fatal(err)
	}
	data, _ = json.Marshal(liveproto.ClientFrame{Type: liveproto.TypeSubscribe, Subscription: "bound", Sealed: sealed})
	if err := other.WriteMessage(websocket.TextMessage, data); err != nil {
		t.Fatal(err)
	}
	if f := frameOn(other, "bound"); f["type"] != liveproto.TypeRows {
		t.Fatalf("after binding: %v", f)
	}
}
