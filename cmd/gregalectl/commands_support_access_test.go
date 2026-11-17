package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// support-access app commands call the customer route from the operator
// session and name the grant on every request (ADR-20261116163508).
func TestSupportAccessCommandsSendGrantHeader(t *testing.T) {
	type seen struct {
		method, path, grant string
		body                map[string]any
	}
	var got []seen
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		s := seen{method: r.Method, path: r.URL.RequestURI(), grant: r.Header.Get(api.SupportGrantHeader)}
		_ = json.Unmarshal(raw, &s.body)
		got = append(got, s)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"dep-1","status":"queued"}`))
	}))
	defer srv.Close()
	t.Setenv("FAAS_APID_URL", srv.URL)
	t.Setenv(operatorSessionEnv, "operator-cookie")

	var stdout bytes.Buffer
	restoreOut, restoreJSON := osStdout, jsonOutput
	osStdout, jsonOutput = &stdout, true
	defer func() { osStdout, jsonOutput = restoreOut, restoreJSON }()

	if code := cmdSupportAccessDispatch([]string{"deploy", "--grant", "g-1", "--app", "shop", "--repo", "acme/shop", "--ref", "main"}); code != 0 {
		t.Fatalf("deploy exit = %d", code)
	}
	if code := cmdSupportAccessDispatch([]string{"restart", "--grant", "g-1", "--app", "shop", "--fresh"}); code != 0 {
		t.Fatalf("restart exit = %d", code)
	}
	if code := cmdSupportAccessDispatch([]string{"rollback", "--grant", "g-1", "--app", "shop"}); code != 0 {
		t.Fatalf("rollback exit = %d", code)
	}
	if code := cmdSupportAccessDispatch([]string{"restart", "--app", "shop"}); code != 2 {
		t.Fatalf("restart without --grant exit = %d, want 2", code)
	}
	want := []struct{ method, path string }{
		{http.MethodPost, "/v1/apps/shop/deployments/source-ref"},
		{http.MethodPost, "/v1/apps/shop/restart?fresh=true"},
		{http.MethodPost, "/v1/apps/shop/rollback"},
	}
	if len(got) != len(want) {
		t.Fatalf("requests = %+v", got)
	}
	for i, w := range want {
		if got[i].method != w.method || got[i].path != w.path || got[i].grant != "g-1" {
			t.Errorf("request %d = %+v, want %s %s with the grant header", i, got[i], w.method, w.path)
		}
	}
	if got[0].body["repo"] != "acme/shop" || got[0].body["ref"] != "main" || len(got[0].body) != 2 {
		t.Errorf("deploy body = %v, want only repo and ref", got[0].body)
	}
	if len(got[2].body) != 0 {
		t.Errorf("rollback body = %v, want empty", got[2].body)
	}
	var out map[string]any
	dec := json.NewDecoder(&stdout)
	if err := dec.Decode(&out); err != nil || out["id"] != "dep-1" {
		t.Fatalf("JSON output = %v, %v", out, err)
	}
}
