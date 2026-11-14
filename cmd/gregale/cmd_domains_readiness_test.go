package main_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestDomainVerifyRoutingFailurePreservesDiagnostics(t *testing.T) {
	main.ResetJSONOut(t)
	old := *main.OsStdout
	var out bytes.Buffer
	*main.OsStdout = &out
	t.Cleanup(func() { *main.OsStdout = old })
	main.AuthedFakeAPI(t, `{"domain":"app.example.com","app_id":"app-1","verified":true,"cert_status":"issued","cert_not_after":"2099-01-01T00:00:00Z","readiness":{"status":"fix_routing","ownership":"verified","routing":"fail","tls":"issued"},"checks":[{"name":"points_to_gregale","status":"fail","detail":"CNAME is wrong","observed":"wrong.example.net","remediation":"Set CNAME to edge.gregale.dev"}],"dns_records":[{"type":"CNAME","name":"app.example.com","value":"edge.gregale.dev","purpose":"routing"}]}`, http.StatusOK)
	if code := main.Run([]string{"domain", "verify", "app.example.com"}); code != 1 {
		t.Fatalf("wrong routing exit=%d", code)
	}
	for _, want := range []string{"fix_routing", "wrong.example.net", "Set CNAME", "edge.gregale.dev"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("missing %s in %s", want, out.String())
		}
	}
}

func TestDomainVerifyWaitEmitsOneFinalJSONResult(t *testing.T) {
	main.ResetJSONOut(t)
	t.Setenv("FAAS_TOKEN", "test-token")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodPost || r.URL.Path != "/v1/domains/app.example.com/verify" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if calls.Add(1) == 1 {
			_, _ = fmt.Fprint(w, `{"domain":"app.example.com","app_id":"app-1","verified":true,"cert_status":"pending","readiness":{"status":"preparing_https","ownership":"verified","routing":"ok","tls":"pending"}}`)
			return
		}
		_, _ = fmt.Fprint(w, `{"domain":"app.example.com","app_id":"app-1","verified":true,"cert_status":"issued","cert_not_after":"2099-01-01T00:00:00Z","readiness":{"status":"ready","ownership":"verified","routing":"ok","tls":"issued"}}`)
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	old := *main.OsStdout
	var out bytes.Buffer
	*main.OsStdout = &out
	t.Cleanup(func() { *main.OsStdout = old })
	if code := main.Run([]string{"--json", "domain", "verify", "app.example.com", "--wait", "--timeout", "8s"}); code != 0 {
		t.Fatalf("wait exit=%d, out=%s", code, out.String())
	}
	var got api.CustomDomainResponse
	if err := json.Unmarshal(out.Bytes(), &got); err != nil || got.Readiness == nil || got.Readiness.Status != "ready" || calls.Load() != 2 {
		t.Fatalf("final JSON: %s, err=%v, calls=%d", out.String(), err, calls.Load())
	}
}

func TestDomainVerifyWaitRejectsInvalidTiming(t *testing.T) {
	main.ResetJSONOut(t)
	for _, args := range [][]string{{"--timeout", "0s"}, {"--poll-interval", "1ms"}, {"--timeout", "2h"}} {
		if code := main.Run(append([]string{"domain", "verify", "app.example.com", "--wait"}, args...)); code == 0 {
			t.Fatalf("accepted %v", args)
		}
	}
}
