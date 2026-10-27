package journeys

import (
	"errors"
	"net"
	"net/url"
	"strings"
	"testing"
	"time"
)

// These tests cover the harness itself and need no target. The runner
// skips them (-skip ^TestHarness) so reports list journeys only.

func TestHarnessRunIDEncodesStartMinute(t *testing.T) {
	now := time.Date(2026, 10, 26, 17, 30, 45, 0, time.UTC)
	id := newRunID(now)
	got, ok := runTime(id)
	if !ok {
		t.Fatalf("runTime(%q) rejected a generated id", id)
	}
	if want := now.Truncate(time.Minute); !got.Equal(want) {
		t.Fatalf("runTime(%q) = %s, want %s", id, got, want)
	}
}

func TestHarnessRunTimeRejectsMalformedIDs(t *testing.T) {
	for _, id := range []string{"", "abc", "ABCDEF", "abcdefg", "ab_def", "hk3x9-"} {
		if _, ok := runTime(id); ok {
			t.Errorf("runTime(%q) accepted a malformed id", id)
		}
	}
}

func TestHarnessSlugRunOnlyMatchesJourneySlugs(t *testing.T) {
	tests := []struct {
		slug string
		run  string
		ok   bool
	}{
		{"e2ej-hk3x9a-probe", "hk3x9a", true},
		{"e2ej-hk3x9a-tpl-hello-node", "hk3x9a", true},
		{"e2e-fn-node", "", false},        // hand-made app on a shared account
		{"e2ej-hk3x9-probe", "", false},   // short run id
		{"e2ej-hk3x9a-", "", false},       // no name
		{"xe2ej-hk3x9a-probe", "", false}, // prefix must anchor
		{"e2ej-HK3X9A-probe", "", false},
	}
	for _, tc := range tests {
		run, ok := slugRun(tc.slug)
		if ok != tc.ok || run != tc.run {
			t.Errorf("slugRun(%q) = %q, %v; want %q, %v", tc.slug, run, ok, tc.run, tc.ok)
		}
	}
}

func TestHarnessStaleSlugsKeepsCurrentRunAndRecentRuns(t *testing.T) {
	now := time.Date(2026, 10, 26, 18, 0, 0, 0, time.UTC)
	old := newRunID(now.Add(-5 * time.Hour))
	recent := newRunID(now.Add(-10 * time.Minute))
	current := newRunID(now)
	apps := []map[string]any{
		{"slug": "e2ej-" + old + "-probe"},
		{"slug": "e2ej-" + recent + "-probe"},
		{"slug": "e2ej-" + current + "-probe"},
		{"slug": "e2e-fn-node"},
		{"slug": "customer-api"},
	}
	got := staleSlugs(apps, now.Add(-3*time.Hour), current)
	if len(got) != 1 || got[0] != "e2ej-"+old+"-probe" {
		t.Fatalf("staleSlugs = %v, want only the 5h-old journey app", got)
	}
}

func TestHarnessSlugPanicsWhenTooLong(t *testing.T) {
	tg := &target{runID: "hk3x9a"}
	if got := tg.slug("probe"); got != "e2ej-hk3x9a-probe" {
		t.Fatalf("slug = %q", got)
	}
	defer func() {
		if recover() == nil {
			t.Fatal("slug accepted a name longer than the slug limit")
		}
	}()
	tg.slug(strings.Repeat("x", maxSlugLen))
}

func TestHarnessDecodeObjectsReadsIndentedAndNDJSON(t *testing.T) {
	indented := []byte("{\n  \"items\": [\n    {\"id\": \"a\"}\n  ]\n}\n")
	docs, err := decodeObjects(indented)
	if err != nil || len(docs) != 1 || str(docs[0], "items.0.id") != "a" {
		t.Fatalf("indented: docs=%v err=%v", docs, err)
	}
	ndjson := []byte("{\"slug\":\"a\",\"n\":1}\n{\"slug\":\"b\",\"n\":2.5}\n")
	docs, err = decodeObjects(ndjson)
	if err != nil || len(docs) != 2 || str(docs[1], "slug") != "b" || num(docs[1], "n") != 2.5 {
		t.Fatalf("ndjson: docs=%v err=%v", docs, err)
	}
	if docs, err := decodeObjects(nil); err != nil || len(docs) != 0 {
		t.Fatalf("empty output: docs=%v err=%v", docs, err)
	}
	if _, err := decodeObjects([]byte("not json")); err == nil {
		t.Fatal("decodeObjects accepted non-JSON output")
	}
}

func TestHarnessFieldWalksMapsAndArrays(t *testing.T) {
	doc := map[string]any{"a": map[string]any{"b": []any{"x", map[string]any{"c": "y"}}}}
	tests := map[string]any{"a.b.0": "x", "a.b.1.c": "y", "a.b.2": nil, "a.z": nil, "a.b.x": nil}
	for path, want := range tests {
		if got := field(doc, path); got != want {
			t.Errorf("field(%q) = %v, want %v", path, got, want)
		}
	}
}

func TestHarnessLoadTarget(t *testing.T) {
	env := func(kv map[string]string) func(string) string {
		return func(k string) string { return kv[k] }
	}
	if tg, err := loadTarget(env(nil)); tg != nil || err != nil {
		t.Fatalf("no API configured: got %v, %v; want nil, nil", tg, err)
	}
	tg, err := loadTarget(env(map[string]string{"GREGALE_E2E_API": "https://api.gregale.dev/"}))
	if err != nil {
		t.Fatal(err)
	}
	if tg.api != "https://api.gregale.dev" || tg.appsDomain != "gregale.dev" || tg.tier != tierSmoke || tg.cli != "gregale" {
		t.Fatalf("defaults: %+v", tg)
	}
	for _, bad := range []map[string]string{
		{"GREGALE_E2E_API": "api.gregale.dev"},
		{"GREGALE_E2E_API": "https://api.gregale.dev", "GREGALE_E2E_TIER": "nightly"},
		{"GREGALE_E2E_API": "https://api.gregale.dev", "GREGALE_E2E_RUN_ID": "run-1"},
		{"GREGALE_E2E_API": "https://api.gregale.dev", "GREGALE_E2E_AUTH": "cli-login", "GREGALE_E2E_TOKEN": "fp_live_x"},
		{"GREGALE_E2E_API": "https://api.gregale.dev", "GREGALE_E2E_MAX_DEPLOYS": "0"},
	} {
		if _, err := loadTarget(env(bad)); err == nil {
			t.Errorf("loadTarget(%v) accepted a bad configuration", bad)
		}
	}
}

func TestHarnessRetryableAbortOnlyForCapacity(t *testing.T) {
	tests := []struct {
		name  string
		res   cliResult
		retry bool
	}{
		{"success", cliResult{exit: 0, stdout: []byte(`{"failure_class":"infra"}`)}, false},
		{"infra class", cliResult{exit: 1, stdout: []byte(`{"failure_class":"infra"}`)}, true},
		{"ram ceiling", cliResult{exit: 1, stdout: []byte(`{"rollout_aborted_reason":"snapshot prime failed: capacity_unavailable: RAM headroom"}`)}, true},
		{"builder disk", cliResult{exit: 1, stdout: []byte(`{"rollout_aborted_reason":"builderd: insufficient host free space for build drive"}`)}, true},
		{"app bug", cliResult{exit: 1, stdout: []byte(`{"rollout_aborted_reason":"readiness probe failed: /healthz 500"}`)}, false},
		{"no receipt", cliResult{exit: 1}, false},
	}
	for _, tc := range tests {
		if got := retryableAbort(tc.res) != ""; got != tc.retry {
			t.Errorf("%s: retryableAbort = %v, want %v", tc.name, got, tc.retry)
		}
	}
}

func TestHarnessAppSlotCount(t *testing.T) {
	tests := []struct{ limit, used, maxApps, want int }{
		{100, 87, 0, 13},  // free slots
		{100, 87, 5, 5},   // capped
		{100, 87, 50, 13}, // cap above free
		{100, 100, 0, 0},  // full account
		{100, 120, 0, 0},  // over the limit (grandfathered)
		{0, 40, 0, 8},     // unknown limit
	}
	for _, tc := range tests {
		if got := appSlotCount(tc.limit, tc.used, tc.maxApps); got != tc.want {
			t.Errorf("appSlotCount(%d, %d, %d) = %d, want %d", tc.limit, tc.used, tc.maxApps, got, tc.want)
		}
	}
}

func TestHarnessPositiveEnv(t *testing.T) {
	env := map[string]string{"A": "3", "B": "0", "C": "x"}
	get := func(k string) string { return env[k] }
	if n, err := positiveEnv(get, "A", 4); n != 3 || err != nil {
		t.Errorf("A: %d, %v", n, err)
	}
	if n, err := positiveEnv(get, "MISSING", 4); n != 4 || err != nil {
		t.Errorf("default: %d, %v", n, err)
	}
	for _, k := range []string{"B", "C"} {
		if _, err := positiveEnv(get, k, 4); err == nil {
			t.Errorf("%s: accepted %q", k, env[k])
		}
	}
}

func TestHarnessRedactHidesToken(t *testing.T) {
	tg := &target{token: "fp_live_0123456789abcdef"}
	if got := tg.redact("Authorization: Bearer fp_live_0123456789abcdef"); strings.Contains(got, "0123456789") {
		t.Fatalf("redact leaked the token: %q", got)
	}
}

func TestHarnessStoryBlocksLaterStepsAfterFailure(t *testing.T) {
	// A failing subtest would fail this test, so drive the bookkeeping only.
	s := &story{t: t, blocked: "deploy"}
	ran := false
	if s.step("serve", func(*testing.T) { ran = true }) {
		t.Fatal("step reported success while blocked")
	}
	s.check("metrics", func(*testing.T) { ran = true })
	if ran {
		t.Fatal("a blocked story ran a later step")
	}
}

func TestHarnessFailureCodeNamesProblemOrStatus(t *testing.T) {
	tests := []struct {
		r    httpResult
		want string
	}{
		{httpResult{status: 503, body: []byte(`{"code":"traffic_policy_unavailable"}`)}, "traffic_policy_unavailable"},
		{httpResult{status: 502, body: []byte(`<html>bad gateway</html>`)}, "http_502"},
		{httpResult{err: errors.New("timeout")}, "transport_error"},
	}
	for _, tc := range tests {
		if got := failureCode(tc.r); got != tc.want {
			t.Errorf("failureCode(%+v) = %q, want %q", tc.r, got, tc.want)
		}
	}
	var none *continuity
	if failed, total, _, _ := none.result(); failed != 0 || total != 0 {
		t.Fatalf("nil continuity reported %d/%d", failed, total)
	}
}

func TestHarnessIdempotentCommandRetriesOnlySafeCommands(t *testing.T) {
	retry := [][]string{
		{"app", "x", "--maintenance"}, {"park", "x"}, {"apps", "rm", "x", "--yes"},
		{"secrets", "list", "--app", "x"}, {"crons", "rm", "id"}, {"keys", "rm", "id"},
	}
	once := [][]string{
		{"deploy", "--name", "x"}, {"rollback", "x"}, {"keys", "add", "l"}, {"crons", "add"},
		{"crons", "run", "id"}, {"secrets", "set", "K=V"}, {"apps", "restore", "x"}, {},
	}
	for _, args := range retry {
		if !idempotentCommand(args) {
			t.Errorf("idempotentCommand(%q) = false, want true", args)
		}
	}
	for _, args := range once {
		if idempotentCommand(args) {
			t.Errorf("idempotentCommand(%q) = true, want false", args)
		}
	}
}

func TestHarnessLocalNetworkErrorIsDNSOnly(t *testing.T) {
	dns := httpResult{err: &url.Error{Op: "Get", URL: "https://x", Err: &net.OpError{Op: "dial", Err: &net.DNSError{Err: "i/o timeout", Name: "x"}}}}
	if !localNetworkError(dns) {
		t.Error("a DNS failure is not classed as local")
	}
	if localNetworkError(httpResult{err: errors.New("net/http: TLS handshake timeout")}) {
		t.Error("a TLS timeout (could be the edge) is classed as local")
	}
	if localNetworkError(httpResult{status: 503}) {
		t.Error("an HTTP 503 is classed as local")
	}
}

func TestHarnessPercentileNearestRank(t *testing.T) {
	ms := func(vs ...int) []time.Duration {
		out := make([]time.Duration, len(vs))
		for i, v := range vs {
			out[i] = time.Duration(v) * time.Millisecond
		}
		return out
	}
	d := ms(900, 100, 300, 200, 500, 400, 700, 600, 800, 1000)
	tests := []struct {
		p    int
		want time.Duration
	}{{50, 500 * time.Millisecond}, {95, time.Second}, {0, 100 * time.Millisecond}, {100, time.Second}}
	for _, tc := range tests {
		if got := percentile(d, tc.p); got != tc.want {
			t.Errorf("p%d = %s, want %s", tc.p, got, tc.want)
		}
	}
	if percentile(nil, 50) != 0 {
		t.Error("percentile of no samples is not zero")
	}
	if d[0] != 900*time.Millisecond {
		t.Error("percentile reordered its input")
	}
}

func TestHarnessRedactHidesDeployTokens(t *testing.T) {
	tg := &target{}
	got := tg.redact(`{"plaintext":"fp_deploy_eceb8ed1ed1ab0ac8a537f1d8dd8feae"}`)
	if strings.Contains(got, "eceb8ed1") {
		t.Fatalf("redact leaked a deploy token: %s", got)
	}
}

func TestHarnessStalePreviewsOnlyJourneyPreviews(t *testing.T) {
	now := time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC)
	old := newRunID(now.Add(-5 * time.Hour))
	apps := []map[string]any{
		{"slug": "pr-3-e2ej-" + old + "-github"},
		{"slug": "pr-4-e2ej-" + newRunID(now) + "-github"},
		{"slug": "pr-7-customer-api"},
		{"slug": "e2ej-" + old + "-github"},
	}
	got := stalePreviews(apps, now.Add(-3*time.Hour), newRunID(now))
	if len(got) != 1 || got[0] != "pr-3-e2ej-"+old+"-github" {
		t.Fatalf("stalePreviews = %v", got)
	}
}
