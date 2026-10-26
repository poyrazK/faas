package journeys

import (
	"strings"
	"testing"
	"time"
)

// These tests cover the harness itself and need no target.

func TestRunIDEncodesStartMinute(t *testing.T) {
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

func TestRunTimeRejectsMalformedIDs(t *testing.T) {
	for _, id := range []string{"", "abc", "ABCDEF", "abcdefg", "ab_def", "hk3x9-"} {
		if _, ok := runTime(id); ok {
			t.Errorf("runTime(%q) accepted a malformed id", id)
		}
	}
}

func TestSlugRunOnlyMatchesJourneySlugs(t *testing.T) {
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

func TestStaleSlugsKeepsCurrentRunAndRecentRuns(t *testing.T) {
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

func TestSlugPanicsWhenTooLong(t *testing.T) {
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

func TestDecodeObjectsReadsIndentedAndNDJSON(t *testing.T) {
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

func TestFieldWalksMapsAndArrays(t *testing.T) {
	doc := map[string]any{"a": map[string]any{"b": []any{"x", map[string]any{"c": "y"}}}}
	tests := map[string]any{"a.b.0": "x", "a.b.1.c": "y", "a.b.2": nil, "a.z": nil, "a.b.x": nil}
	for path, want := range tests {
		if got := field(doc, path); got != want {
			t.Errorf("field(%q) = %v, want %v", path, got, want)
		}
	}
}

func TestLoadTarget(t *testing.T) {
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
	} {
		if _, err := loadTarget(env(bad)); err == nil {
			t.Errorf("loadTarget(%v) accepted a bad configuration", bad)
		}
	}
}

func TestRedactHidesToken(t *testing.T) {
	tg := &target{token: "fp_live_0123456789abcdef"}
	if got := tg.redact("Authorization: Bearer fp_live_0123456789abcdef"); strings.Contains(got, "0123456789") {
		t.Fatalf("redact leaked the token: %q", got)
	}
}

func TestStoryBlocksLaterStepsAfterFailure(t *testing.T) {
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
