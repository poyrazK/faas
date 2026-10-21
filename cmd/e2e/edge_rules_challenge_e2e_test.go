//go:build !no_pg

// edge_rules_challenge_e2e_test.go — per-kind e2e for `kind=challenge`
// (ADR-831 amendment 7): a script is refused, a browser gets the page,
// solving it the way the page's script does earns a pass, the pass gets the
// browser through, and the outcomes reach edge_rule_events as security
// events. Bitmask: APID | Gatewayd; see edge_rules_common_test.go for the
// kind=route-substitute precondition. The harness gateway runs with an
// ephemeral session key, which arms the challenge.

package e2e_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"html"
	"io"
	"math/bits"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/state"
)

var (
	challengeFormAttrs = regexp.MustCompile(`<form id="gregale-challenge" method="post" action="([^"]+)" data-token="([^"]+)" data-difficulty="(\d+)">`)
	challengeReturn    = regexp.MustCompile(`name="return" value="([^"]*)"`)
)

func TestEdgeRulesChallenge_E2E(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	ctx := context.Background()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	h := e2etest.StartWithEnv(t, pool, e2etest.APID|e2etest.Gatewayd, nil)
	key := h.SeedAccount(ctx, api.PlanHobby)
	accountID := accountIDFromKey(t, ctx, pool, key)

	slug := "challenge-test-app"
	createRec := doReqBytes(t, h, key, http.MethodPost, "/v1/apps",
		api.CreateAppRequest{Slug: slug, RequireAuthn: boolPtr(false)})
	var app api.AppResponse
	if err := json.Unmarshal(createRec, &app); err != nil {
		t.Fatalf("decode app: %v body=%s", err, createRec)
	}
	host := "edgectl-challenge.apps.test.example"
	seedRouteSubstitute(t, ctx, pool, accountID, app.ID, host, slug)
	ruleID := seedEdgeRuleDirect(t, ctx, pool, accountID, app.ID, host, state.EdgeRuleKindChallenge,
		map[string]any{"kind": "challenge", "challenge": map[string]any{"difficulty": 10, "pass_ttl_seconds": 300}})
	resetEdgeRuleCache(t, h)

	browser := map[string]string{
		"User-Agent":     "Mozilla/5.0 (X11; Linux x86_64; rv:131.0) Gecko/20100101 Firefox/131.0",
		"Accept":         "text/html,application/xhtml+xml,*/*;q=0.8",
		"Sec-Fetch-Mode": "navigate",
	}

	// A script never sees the page: 403 challenge_required.
	_, body, status := doReqHeaders(t, h, host, http.MethodGet, "/account", nil,
		map[string]string{"User-Agent": "python-requests/2.32", "Accept": "application/json"})
	if status != http.StatusForbidden || problemCode(body) != api.CodeChallengeRequired {
		t.Fatalf("script: status=%d code=%q, want 403 %s", status, problemCode(body), api.CodeChallengeRequired)
	}

	// A browser navigation gets the challenge page.
	hdr, page, status := doReqHeaders(t, h, host, http.MethodGet, "/account?tab=orders", nil, browser)
	if status != http.StatusForbidden || !strings.HasPrefix(hdr.Get("Content-Type"), "text/html") {
		t.Fatalf("page: status=%d content-type=%q, want 403 text/html", status, hdr.Get("Content-Type"))
	}
	m := challengeFormAttrs.FindSubmatch(page)
	ret := challengeReturn.FindSubmatch(page)
	if m == nil || ret == nil {
		t.Fatalf("no challenge form in page: %.400s", page)
	}
	action, token := html.UnescapeString(string(m[1])), html.UnescapeString(string(m[2]))
	difficulty, _ := strconv.Atoi(string(m[3]))
	back := html.UnescapeString(string(ret[1]))
	if difficulty != 10 || back != "/account?tab=orders" {
		t.Fatalf("form: difficulty=%d return=%q, want 10 and the original path", difficulty, back)
	}

	// Solve it as the page's script does and post the answer.
	form := url.Values{"token": {token}, "counter": {solveChallenge(t, token, difficulty)}, "return": {back}}
	hdr, body, status = postChallengeForm(t, h, host, action, form, browser)
	if status != http.StatusSeeOther || hdr.Get("Location") != back {
		t.Fatalf("solve: status=%d location=%q body=%s, want 303 to %s", status, hdr.Get("Location"), body, back)
	}
	var pass *http.Cookie
	for _, c := range (&http.Response{Header: hdr}).Cookies() {
		if c.Name == "gregale_challenge" {
			pass = c
		}
	}
	if pass == nil || !pass.HttpOnly || !pass.Secure {
		t.Fatalf("pass cookie = %+v in %v", pass, hdr.Values("Set-Cookie"))
	}

	// The pass gets the browser, and its page's own requests, past the gate.
	withPass := map[string]string{"Cookie": pass.Name + "=" + pass.Value}
	for k, v := range browser {
		withPass[k] = v
	}
	_, body, status = doReqHeaders(t, h, host, http.MethodGet, "/account?tab=orders", nil, withPass)
	assertBackendFallthrough(t, status, body)
	withPass["Sec-Fetch-Mode"], withPass["Accept"] = "cors", "application/json"
	_, body, status = doReqHeaders(t, h, host, http.MethodGet, "/api/cart", nil, withPass)
	assertBackendFallthrough(t, status, body)

	// A forged pass is challenged again.
	withPass["Cookie"] = pass.Name + "=" + pass.Value + "x"
	if _, _, status = doReqHeaders(t, h, host, http.MethodGet, "/api/cart", nil, withPass); status != http.StatusForbidden {
		t.Fatalf("forged pass: status=%d, want 403", status)
	}

	// The outcomes reach edge_rule_events at the gateway's next flush (one
	// minute), as ADR-964 security events.
	want := []string{state.EdgeRuleEventChallenged, state.EdgeRuleEventRejected, state.EdgeRuleEventSolved}
	var got []string
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		got = challengeEventOutcomes(t, ctx, pool, ruleID)
		if !slices.ContainsFunc(want, func(o string) bool { return !slices.Contains(got, o) }) {
			break
		}
		time.Sleep(2 * time.Second)
	}
	for _, o := range want {
		if !slices.Contains(got, o) {
			t.Fatalf("edge_rule_events outcomes for the challenge rule = %v, want %v", got, want)
		}
	}
	if slices.Contains(got, state.EdgeRuleHitMatched) {
		t.Errorf("enforced challenge rule recorded plain matched events: %v", got)
	}
}

// solveChallenge finds a counter whose SHA-256 over "token:counter" starts
// with difficulty zero bits — the work the page's script does.
func solveChallenge(t *testing.T, token string, difficulty int) string {
	t.Helper()
	for n := 0; n < 1<<24; n++ {
		c := strconv.Itoa(n)
		sum := sha256.Sum256([]byte(token + ":" + c))
		if bits.LeadingZeros32(binary.BigEndian.Uint32(sum[:4])) >= difficulty {
			return c
		}
	}
	t.Fatal("no counter meets the difficulty")
	return ""
}

// postChallengeForm posts the solve form the way the browser does; the
// harness client does not follow the 303.
func postChallengeForm(t *testing.T, h *e2etest.Harness, host, path string, form url.Values, headers map[string]string) (http.Header, []byte, int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.EdgeURL()+path, bytes.NewReader([]byte(form.Encode())))
	if err != nil {
		t.Fatalf("new solve request: %v", err)
	}
	req.Host = host
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	client := *h.HTTPClient()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST %s (Host=%s): %v", path, host, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.Header, b, resp.StatusCode
}

// challengeEventOutcomes lists the distinct event outcomes stored for a rule.
func challengeEventOutcomes(t *testing.T, ctx context.Context, pool *pgxpool.Pool, ruleID string) []string {
	t.Helper()
	rows, err := pool.Query(ctx, `select distinct outcome from edge_rule_events where rule_id = $1`, ruleID)
	if err != nil {
		t.Fatalf("query edge_rule_events: %v", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var o string
		if err := rows.Scan(&o); err != nil {
			t.Fatalf("scan outcome: %v", err)
		}
		out = append(out, o)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read edge_rule_events: %v", err)
	}
	return out
}
