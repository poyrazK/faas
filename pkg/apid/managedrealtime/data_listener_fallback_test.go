package managedrealtime

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// apid reaches the realtimed control proxy on a node's private control
// listener, and repeats a request on the data listener only while the node
// runs a gatewayd-internal that does not route it there: a refused
// connection or a mux 404. realtimed's own JSON 404 is an answer.
func TestRealtimeControlPrefersThePrivateListener(t *testing.T) {
	base, dataHost, err := realtimeGatewayBaseURL("tcp://10.0.0.7:8080")
	if err != nil || base != "http://10.0.0.7:"+api.GatewaydPrivateControlPort+"/v1/internal/realtime" || dataHost != "10.0.0.7:8080" {
		t.Fatalf("base = %q data = %q (%v)", base, dataHost, err)
	}

	var dataBodies []string
	data := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		dataBodies = append(dataBodies, string(body))
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"from":"data"}`)
	}))
	defer data.Close()
	dataURL, _ := url.Parse(data.URL)
	send := func(target string) string {
		t.Helper()
		client := &http.Client{Transport: dataListenerFallback{base: http.DefaultTransport, dataHost: dataURL.Host}}
		resp, err := client.Post(target+"/v1/internal/realtime/internal/publish", "application/json", strings.NewReader(`{"channel":"c"}`))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return string(body)
	}

	routed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"error":"no such connection"}`)
	}))
	defer routed.Close()
	if got := send(routed.URL); got != `{"error":"no such connection"}` {
		t.Fatalf("realtimed's own 404 = %q, want it answered by the private listener", got)
	}
	older := httptest.NewServer(http.NewServeMux()) // routes nothing: a mux 404
	defer older.Close()
	if got := send(older.URL); got != `{"from":"data"}` {
		t.Fatalf("an older node = %q, want the data listener's answer", got)
	}
	closed, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	refused := "http://" + closed.Addr().String()
	_ = closed.Close()
	if got := send(refused); got != `{"from":"data"}` {
		t.Fatalf("a node without the private listener = %q, want the data listener's answer", got)
	}
	for _, body := range dataBodies {
		if body != `{"channel":"c"}` {
			t.Fatalf("data listener got body %q, want the request repeated whole", body)
		}
	}
}
