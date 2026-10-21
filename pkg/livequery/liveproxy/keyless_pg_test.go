package liveproxy

import (
	"context"
	"fmt"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/livequery"
	"github.com/onebox-faas/faas/pkg/livequery/liveproto"
	"github.com/onebox-faas/faas/pkg/livequery/liveserver"
	"github.com/onebox-faas/faas/pkg/realtime"
)

// adr: 20261016151947 — reads without a user token end to end: a visitor with
// no session connects to an endpoint without client auth and subscribes to a
// public relation; a backend subscribes with a server key. realtimed admits
// both against the Data API's reported settings and the hub reads with the
// gateway's claims, so row-level security decides the rows: visitors see
// published products, follow new ones, and cannot read orders; the server
// key reads orders. The proxy test shows the key never reaches the hub.
func TestRealtimeCarriesAnonymousAndServerKeyReads(t *testing.T) {
	dsn, pool := openDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	role := fmt.Sprintf("lq_kl_%d", rand.Uint32())
	claim := `current_setting('request.jwt.claims', true)::json->>'gregale_access'`
	for _, sql := range []string{
		`CREATE SCHEMA api`,
		`CREATE TABLE api.products (id bigint PRIMARY KEY, published boolean NOT NULL, name text)`,
		`CREATE TABLE api.orders (id bigint PRIMARY KEY, total int)`,
		fmt.Sprintf(`CREATE ROLE %s NOLOGIN NOINHERIT`, role),
		fmt.Sprintf(`GRANT USAGE ON SCHEMA api TO %[1]s; GRANT SELECT ON api.products, api.orders TO %[1]s`, role),
		`ALTER TABLE api.products ENABLE ROW LEVEL SECURITY; ALTER TABLE api.orders ENABLE ROW LEVEL SECURITY`,
		fmt.Sprintf(`CREATE POLICY catalog ON api.products FOR SELECT TO %s USING (published AND %s IN ('anonymous', 'server'))`, role, claim),
		fmt.Sprintf(`CREATE POLICY backend ON api.orders FOR SELECT TO %s USING (%s = 'server')`, role, claim),
		`INSERT INTO api.products VALUES (1, true, 'lamp'), (2, false, 'draft'); INSERT INTO api.orders VALUES (10, 5)`,
	} {
		if _, err := pool.Exec(ctx, sql); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), fmt.Sprintf(`DROP OWNED BY %[1]s; DROP ROLE %[1]s`, role))
	})
	if err := livequery.Install(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `CREATE PUBLICATION gregale_live FOR ALL TABLES`); err != nil {
		t.Fatal(err)
	}
	capture, err := livequery.OpenCapture(ctx, dsn, fmt.Sprintf("lqkl_%d", time.Now().UnixNano()), "gregale_live")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = capture.Close() }()
	hub := liveserver.New(liveserver.Config{Database: "db_1", Engine: livequery.NewEngine(pool, capture), Idle: 5 * time.Millisecond, MaxPerConnection: 1000, Pool: pool})
	stopped := make(chan error, 1)
	go func() { stopped <- hub.Run(ctx) }()
	defer func() { cancel(); <-stopped }()
	hubMux := http.NewServeMux()
	anyOrigin := func(*http.Request) bool { return true }
	hubMux.Handle("/live", liveserver.WebSocketTrusting(hub, anyOrigin, func(*http.Request) bool { return true }))
	hubSrv := httptest.NewServer(hubMux)
	defer hubSrv.Close()

	key := "gdsk_" + strings.Repeat("K", 43)
	route := realtime.LiveRoute{KID: "0123456789abcdef", Database: "db_1", URL: "ws" + strings.TrimPrefix(hubSrv.URL, "http") + "/live", DataAPIRole: role,
		DataAPIAccess: &api.DataAPIAccessConfig{Version: 1, Schemas: []string{"api"}, PublicRead: []string{"api.products"},
			ServerKeys: []api.DataAPIServerKey{{Slot: "a", SHA256: keyHash(key)}}}}
	proxy := New(nil, nil)
	defer proxy.Close()
	m := realtime.NewManager(realtime.Config{ResumePreview: true, HistoryReader: noHistory{}, Live: ForRealtime(proxy)}, hooks{})
	defer func() { _ = m.Close() }()
	if err := m.RegisterEndpoint(realtime.Endpoint{
		ID: "ep", AppID: "app-1", AccountID: "acct-1", CallbackURL: "https://app.example",
		ClientAuth: realtime.AuthPolicy{Mode: realtime.AuthModeNone}, LiveRoutes: []realtime.LiveRoute{route},
	}); err != nil {
		t.Fatal(err)
	}
	rtSrv := httptest.NewServer(m.Handler())
	defer rtSrv.Close()
	ws, resp, err := (&websocket.Dialer{Subprotocols: []string{realtime.ResumeSubprotocol}}).Dial("ws"+strings.TrimPrefix(rtSrv.URL, "http")+realtime.ManagedPathPrefix+"ep", nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	defer ws.Close()
	c := &wsClient{t: t, ws: ws, results: map[string]*liveproto.Result{"visitor": {}, "backend": {}}, frames: map[string][]map[string]any{}}

	c.send(liveproto.ClientFrame{Type: liveproto.TypeSubscribe, Subscription: "visitor", Database: "db_1", Rest: "products?select=id,name&order=id", Anonymous: true})
	c.send(liveproto.ClientFrame{Type: liveproto.TypeSubscribe, Subscription: "backend", Database: "db_1", Rest: "orders?select=id&order=id", ServerKey: key})
	c.until("both results", func() bool { return c.results["visitor"].Ready() && c.results["backend"].Ready() })
	if got := ids(c.results["visitor"]) + "|" + ids(c.results["backend"]); got != "1|10" {
		t.Fatalf("initial visitor|backend = %s", got)
	}
	c.send(liveproto.ClientFrame{Type: liveproto.TypeSubscribe, Subscription: "private", Database: "db_1", Rest: "orders?select=id", Anonymous: true})
	c.until("the refusal", func() bool { return c.last("private") != nil })
	if f := c.last("private"); f["code"] != liveproto.CodeForbidden || f["closed"] != true {
		t.Fatalf("anonymous read of orders: %v", f)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO api.products VALUES (3, true, 'desk'), (4, false, 'secret')`); err != nil {
		t.Fatal(err)
	}
	c.until("the new product", func() bool { return ids(c.results["visitor"]) == "1,3" })
	c.send(liveproto.ClientFrame{Type: liveproto.TypeRenew, Subscription: "visitor", Anonymous: true})
	c.until("the renewal", func() bool { f := c.last("visitor"); return f != nil && f["type"] == liveproto.TypeRenewed })
}
