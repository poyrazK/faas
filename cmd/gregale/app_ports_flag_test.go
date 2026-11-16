package main_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestParseAppPortsFlag(t *testing.T) {
	got, err := main.ParseAppPortsFlag("tcp:7000, udp:5353, admin=tcp:9000, metrics=TCP:9100:internal")
	if err != nil {
		t.Fatal(err)
	}
	want := []api.WorkloadPort{
		{Name: "tcp-7000", Port: 7000, Protocol: api.WorkloadPortTCP},
		{Name: "udp-5353", Port: 5353, Protocol: api.WorkloadPortUDP},
		{Name: "admin", Port: 9000, Protocol: api.WorkloadPortTCP},
		{Name: "metrics", Port: 9100, Protocol: api.WorkloadPortTCP, Internal: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ports = %+v\nwant %+v", got, want)
	}
	if cleared, err := main.ParseAppPortsFlag("none"); err != nil || cleared == nil || len(cleared) != 0 {
		t.Fatalf("none = %+v, %v; want an empty, non-nil list", cleared, err)
	}
	for _, bad := range []string{"", "sctp:9", "tcp", "tcp:x", "tcp:7000:public", "tcp:0"} {
		if _, err := main.ParseAppPortsFlag(bad); err == nil {
			t.Errorf("--ports %q accepted", bad)
		}
	}
}

// Hunt 34 H34-20: a non-Compose app could declare the listener that
// `app listener create` and `app udp add` require only with a raw API call.
func TestAppPortsFlagSendsTheListenerDeclaration(t *testing.T) {
	var body map[string]json.RawMessage
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPatch && r.URL.Path == "/v1/apps/demo" {
			raw, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(raw, &body)
		}
		if strings.HasPrefix(r.URL.Path, "/v1/apps/demo") {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(api.AppResponse{Slug: "demo", Status: "active", RAMMB: 512})
			return
		}
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not found", ""))
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	_, readStderr, restore := main.SwapIO(t)
	code := main.Run([]string{"app", "demo", "--ports", "tcp:7000,udp:5353"})
	restore()
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, readStderr())
	}
	var ports []api.WorkloadPort
	if err := json.Unmarshal(body["ports"], &ports); err != nil {
		t.Fatalf("PATCH body ports %s: %v", body["ports"], err)
	}
	if len(ports) != 2 || ports[0].Name != "tcp-7000" || ports[1].Protocol != api.WorkloadPortUDP {
		t.Fatalf("PATCH ports = %+v", ports)
	}
}
