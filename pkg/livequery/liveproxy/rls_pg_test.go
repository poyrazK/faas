package liveproxy

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/onebox-faas/faas/pkg/livequery"
	"github.com/onebox-faas/faas/pkg/livequery/liveproto"
	"github.com/onebox-faas/faas/pkg/livequery/liveserver"
	"github.com/onebox-faas/faas/pkg/realtime"
)

// readerRole creates a login-less role that may read tables, as an app's
// binding login may, and drops it when the test ends.
func readerRole(t *testing.T, pool interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}, tables ...string) string {
	t.Helper()
	role := fmt.Sprintf("lq_reader_%d", rand.Uint32())
	if _, err := pool.Exec(context.Background(), fmt.Sprintf(`CREATE ROLE %s NOLOGIN; GRANT SELECT ON %s TO %s`, role, strings.Join(tables, ", "), role)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), fmt.Sprintf(`DROP OWNED BY %[1]s; DROP ROLE %[1]s`, role))
	})
	return role
}

// userJWT verifies tokens "user:<sub>" as role.
type userJWT struct{ role string }

func (userJWT) Authorize(_ context.Context, token string, _ realtime.AuthPolicy) (string, error) {
	return strings.TrimPrefix(token, "user:"), nil
}

func (u userJWT) AuthorizeClaims(_ context.Context, token string, _ realtime.AuthPolicy) (json.RawMessage, time.Time, error) {
	sub, ok := strings.CutPrefix(token, "user:")
	if !ok {
		return nil, time.Time{}, fmt.Errorf("bad token")
	}
	exp := time.Now().Add(time.Hour)
	// iat differs on every token, as a refresh's does.
	return json.RawMessage(fmt.Sprintf(`{"sub":%q,"role":%q,"exp":%d,"iat":%d}`, sub, u.role, exp.Unix(), time.Now().UnixNano())), exp, nil
}

// wsClient reads one realtime connection's live frames per subscription.
type wsClient struct {
	t       *testing.T
	ws      *websocket.Conn
	results map[string]*liveproto.Result
	frames  map[string][]map[string]any
}

func (c *wsClient) send(f liveproto.ClientFrame) {
	c.t.Helper()
	data, _ := json.Marshal(f)
	if err := c.ws.WriteMessage(websocket.TextMessage, data); err != nil {
		c.t.Fatal(err)
	}
}

func (c *wsClient) until(what string, cond func() bool) {
	c.t.Helper()
	_ = c.ws.SetReadDeadline(time.Now().Add(30 * time.Second))
	for !cond() {
		_, data, err := c.ws.ReadMessage()
		if err != nil {
			c.t.Fatalf("waiting for %s: %v", what, err)
		}
		var raw map[string]any
		if json.Unmarshal(data, &raw) != nil {
			continue
		}
		sub, _ := raw["subscription"].(string)
		c.frames[sub] = append(c.frames[sub], raw)
		frame, err := liveproto.ParseServerFrame(data)
		if err != nil {
			continue
		}
		if p, ok := frame.(*liveproto.Progress); ok {
			for _, r := range c.results {
				_ = r.Apply(p)
			}
			continue
		}
		if r := c.results[sub]; r != nil {
			if err := r.Apply(frame); err != nil {
				c.t.Fatalf("%s: %v", sub, err)
			}
		}
	}
}

func (c *wsClient) last(sub string) map[string]any {
	if fs := c.frames[sub]; len(fs) > 0 {
		return fs[len(fs)-1]
	}
	return nil
}

func ids(r *liveproto.Result) string {
	var out []string
	for _, row := range r.Rows() {
		out = append(out, *row.Values[0])
	}
	sort.Strings(out)
	return strings.Join(out, ",")
}

// adr: 20261016151947 — RLS mode end to end: a browser subscribes with a Data API read
// and its user's token; realtimed verifies the token and routes by the
// database bound to the endpoint's app; the hub compiles the read and
// maintains it as the token's role, so row-level security decides each
// user's rows, including after a membership change; a renewal with another
// user's token is refused; a hub session that is not realtimed may not
// present claims.
func TestRealtimeCarriesRLSQueries(t *testing.T) {
	dsn, pool := openDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// role stands for the app's Data API login (ADR-650): no memberships,
	// grants only in the api schema.
	role := fmt.Sprintf("lq_rt_%d", rand.Uint32())
	for _, sql := range []string{
		`CREATE SCHEMA api`,
		`CREATE TABLE api.members (channel_id bigint, user_id text, PRIMARY KEY (channel_id, user_id))`,
		`CREATE TABLE api.messages (id bigint PRIMARY KEY, channel_id bigint NOT NULL, body text)`,
		fmt.Sprintf(`CREATE ROLE %s NOLOGIN NOINHERIT`, role),
		fmt.Sprintf(`GRANT USAGE ON SCHEMA api TO %[1]s; GRANT SELECT ON api.messages, api.members TO %[1]s`, role),
		`ALTER TABLE api.messages ENABLE ROW LEVEL SECURITY`,
		fmt.Sprintf(`CREATE POLICY readable ON api.messages FOR SELECT TO %s USING (channel_id IN (SELECT channel_id FROM api.members WHERE user_id = current_setting('request.jwt.claims', true)::json->>'sub'))`, role),
		`INSERT INTO api.messages VALUES (1, 1, 'one'), (2, 2, 'two'); INSERT INTO api.members VALUES (1, 'alice')`,
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
	capture, err := livequery.OpenCapture(ctx, dsn, fmt.Sprintf("lqrls_%d", time.Now().UnixNano()), "gregale_live")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = capture.Close() }()
	engine := livequery.NewEngine(pool, capture)
	engine.VerifyEachRound = true
	hub := liveserver.New(liveserver.Config{Database: "db_1", Engine: engine, Idle: 5 * time.Millisecond, MaxPerConnection: 1000, Pool: pool})
	stopped := make(chan error, 1)
	go func() { stopped <- hub.Run(ctx) }()
	defer func() { cancel(); <-stopped }()
	hubMux := http.NewServeMux()
	anyOrigin := func(*http.Request) bool { return true }
	hubMux.Handle("/live", liveserver.WebSocketTrusting(hub, anyOrigin, func(*http.Request) bool { return true }))
	hubMux.Handle("/untrusted", liveserver.WebSocket(hub, anyOrigin))
	hubSrv := httptest.NewServer(hubMux)
	defer hubSrv.Close()
	hubURL := "ws" + strings.TrimPrefix(hubSrv.URL, "http")

	proxy := New(nil, nil)
	defer proxy.Close()
	m := realtime.NewManager(realtime.Config{JWTAuthorizer: userJWT{role: role}, ResumePreview: true, HistoryReader: noHistory{}, Live: ForRealtime(proxy)}, hooks{})
	defer func() { _ = m.Close() }()
	if err := m.RegisterEndpoint(realtime.Endpoint{
		ID: "ep", AppID: "app-1", AccountID: "acct-1", CallbackURL: "https://app.example",
		AllowedOrigins: []string{"https://app.example"},
		ClientAuth: realtime.AuthPolicy{Mode: realtime.AuthModeOIDCJWT, Issuer: "https://issuer.example",
			JWKSURL: "https://issuer.example/.well-known/jwks.json", Algorithms: []string{"RS256"}},
		LiveRoutes: []realtime.LiveRoute{{KID: "0123456789abcdef", Database: "db_1", URL: hubURL + "/live", DataAPIRole: role}},
	}); err != nil {
		t.Fatal(err)
	}
	rtSrv := httptest.NewServer(m.Handler())
	defer rtSrv.Close()
	ws, resp, err := (&websocket.Dialer{Subprotocols: []string{realtime.ResumeSubprotocol}}).Dial(
		"ws"+strings.TrimPrefix(rtSrv.URL, "http")+realtime.ManagedPathPrefix+"ep", http.Header{"Authorization": {"Bearer user:alice"}})
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	defer ws.Close()
	c := &wsClient{t: t, ws: ws, results: map[string]*liveproto.Result{"alice": {}, "bob": {}}, frames: map[string][]map[string]any{}}

	read := `messages?select=id,body&order=id`
	c.send(liveproto.ClientFrame{Type: liveproto.TypeSubscribe, Subscription: "alice", Database: "db_1", Rest: read, Token: "user:alice"})
	c.send(liveproto.ClientFrame{Type: liveproto.TypeSubscribe, Subscription: "bob", Database: "db_1", Rest: read, Token: "user:bob"})
	c.until("both results", func() bool { return c.results["alice"].Ready() && c.results["bob"].Ready() })
	if got := ids(c.results["alice"]) + "|" + ids(c.results["bob"]); got != "1|" {
		t.Fatalf("initial alice|bob = %s", got)
	}

	// Membership decides: alice joins channel 2 and sees its message.
	if _, err := pool.Exec(ctx, `INSERT INTO api.members VALUES (2, 'alice')`); err != nil {
		t.Fatal(err)
	}
	c.until("alice's new channel", func() bool { return ids(c.results["alice"]) == "1,2" })
	if got := ids(c.results["bob"]); got != "" {
		t.Fatalf("bob sees %s", got)
	}

	// A refreshed token keeps the subscription; another user's does not.
	c.send(liveproto.ClientFrame{Type: liveproto.TypeRenew, Subscription: "alice", Token: "user:alice"})
	c.until("the renewal", func() bool { f := c.last("alice"); return f != nil && f["type"] == liveproto.TypeRenewed })
	c.send(liveproto.ClientFrame{Type: liveproto.TypeRenew, Subscription: "alice", Token: "user:bob"})
	c.until("the mismatch", func() bool { f := c.last("alice"); return f != nil && f["code"] == liveproto.CodeRenewMismatch })

	// Only realtimed may present claims to the hub.
	direct, resp, err := websocket.DefaultDialer.Dial(hubURL+"/untrusted", nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	defer direct.Close()
	d := &wsClient{t: t, ws: direct, results: map[string]*liveproto.Result{}, frames: map[string][]map[string]any{}}
	d.send(liveproto.ClientFrame{Type: liveproto.TypeSubscribe, Subscription: "forged", Database: "db_1", Rest: read,
		Claims: json.RawMessage(`{"sub":"alice","role":"` + role + `"}`), ClaimsExpires: time.Now().Add(time.Hour).Unix()})
	d.until("the refusal", func() bool { return d.last("forged") != nil })
	if f := d.last("forged"); f["code"] != liveproto.CodeForbidden || f["closed"] != true {
		t.Fatalf("untrusted claims: %v", f)
	}
}
