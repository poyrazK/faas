package main_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// `--app shop --since 7d` parsed as app="--since": the normalizer moved the
// slug behind the flags without knowing --app takes a value.
func TestNormalizeAnalyticsArgsKeepsAppWithItsValue(t *testing.T) {
	for _, tc := range []struct{ in, want []string }{
		{[]string{"--app", "shop", "--since", "7d"}, []string{"--app", "shop", "--since", "7d"}},
		{[]string{"-app", "shop", "-by", "status"}, []string{"-app", "shop", "-by", "status"}},
		{[]string{"--app=shop", "--since", "7d"}, []string{"--app=shop", "--since", "7d"}},
		{[]string{"shop", "--json", "--by", "status"}, []string{"--json", "--by", "status", "shop"}},
	} {
		if got := main.NormalizeAnalyticsArgs(tc.in); !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("normalizeAnalyticsArgs(%v) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

// The API groups by consumer_id; the CLI refused it. An empty window that
// was clamped to retention also lost its clamp note.
func TestAnalyticsAppFlagConsumerGroupingAndEmptyClampNote(t *testing.T) {
	main.ResetJSONOut(t)
	var query string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/apps/shop/analytics" {
			http.NotFound(w, r)
			return
		}
		query = r.URL.RawQuery
		_ = json.NewEncoder(w).Encode(api.RequestAnalyticsResponse{Slug: "shop", Since: "3d", GroupBy: "consumer_id", WindowClamped: true})
	}))
	t.Cleanup(srv.Close)
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_test_x")
	out, restore := main.CaptureStdout(t)
	code := main.Run([]string{"analytics", "--app", "shop", "--since", "7d", "--by", "consumer_id"})
	restore()
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out.String())
	}
	if !strings.Contains(query, "group_by=consumer_id") || !strings.Contains(query, "since=7d") {
		t.Fatalf("query = %q, want group_by=consumer_id and since=7d", query)
	}
	if !strings.Contains(out.String(), "clamped to plan telemetry retention") {
		t.Fatalf("empty clamped window lost its note:\n%s", out.String())
	}
}
