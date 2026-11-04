package main_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
)

// `billing costs` listed h11-py six times: one row per deployment under the
// same application name (Hunt 12 H12-43).
func TestBillingCostsOneRowPerApp(t *testing.T) {
	main.ResetJSONOut(t)
	body := `{"period_start":"2026-10-01T00:00:00Z","period_end":"2026-11-01T00:00:00Z","as_of":"2026-10-22T15:41:00Z","meters":[{"meter":"compute","coverage":{"complete":true},"accrued":{"meter":"compute","contracts":[{"price":{"unit":"mb_seconds"},"allocations":[
		{"attribution":{"app_id":"a-1","deployment_id":"d-1","name":"web"},"quantity":100,"net_millicents":10},
		{"attribution":{"job_id":"j-1","name":"nightly"},"quantity":50,"net_millicents":0},
		{"attribution":{"app_id":"a-1","deployment_id":"d-2","name":"web"},"quantity":300,"net_millicents":30},
		{"attribution":{"app_id":"a-2","deployment_id":"d-3","name":"api"},"quantity":1000,"net_millicents":5}]}]}}]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/billing/costs" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_test_x")
	out, restore := main.CaptureStdout(t)
	code := main.Run([]string{"billing", "costs"})
	restore()
	got := out.String()
	if code != 0 || strings.Count(got, " web ") != 1 {
		t.Fatalf("exit %d, want one web row:\n%s", code, got)
	}
	web, api, job := strings.Index(got, "web"), strings.Index(got, " api "), strings.Index(got, "nightly")
	if !strings.Contains(got, "400 mb_seconds") || web < 0 || api < 0 || job < 0 || !(web < api && api < job) {
		t.Fatalf("want web (400, highest cost), api, nightly in that order:\n%s", got)
	}
}
