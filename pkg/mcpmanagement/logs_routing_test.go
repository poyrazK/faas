package mcpmanagement

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// App logs are served by the compute gateway behind the public edge, not by
// apid. The hosted bridge talks to apid's loopback listener, so get_logs
// answered "no such API route" for every hosted connection; it now streams
// logs from LogsAPIBase with the connection's credential.
func TestGetLogsUsesLogsAPIBase(t *testing.T) {
	apid := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not found", "no such API route"))
	}))
	defer apid.Close()
	var auth string
	edge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		if r.URL.Path != "/v1/apps/alpha/logs" {
			http.NotFound(w, r)
			return
		}
		data, _ := json.Marshal(api.LogEvent{Seq: 1, Line: "served by the edge"})
		_, _ = w.Write([]byte("event: log\ndata: " + string(data) + "\n\n"))
	}))
	defer edge.Close()
	_, m, err := newManagementServer(api.NewClient(apid.URL, testToken), Options{APIBase: apid.URL, LogsAPIBase: edge.URL, Token: testToken})
	if err != nil {
		t.Fatal(err)
	}
	out, err := m.getLogs(t.Context(), logsArgs{App: "alpha"})
	if err != nil {
		t.Fatal(err)
	}
	snap := out.(logSnapshot)
	if len(snap.Items) != 1 || snap.Items[0].Line != "served by the edge" {
		t.Fatalf("snapshot = %+v", snap)
	}
	if auth != "Bearer "+testToken {
		t.Fatalf("log request carried %q, want the connection's credential", auth)
	}
}

func TestHostedBridgeDefaultsLogsToItsOrigin(t *testing.T) {
	svc, err := NewHTTPService(HTTPOptions{APIBase: "http://127.0.0.1:8081", ResourceURL: "https://api.example.test/mcp", RedirectURIs: []string{"http://127.0.0.1/callback"}, Hosted: true})
	if err != nil {
		t.Fatal(err)
	}
	if got := svc.remote.opts.LogsAPIBase; got != "https://api.example.test" {
		t.Fatalf("LogsAPIBase = %q", got)
	}
	g := &oauthGrant{client: api.NewClient("http://127.0.0.1:8081", testToken)}
	if got := svc.remote.grantOptions(g, nil, nil).LogsAPIBase; got != "https://api.example.test" {
		t.Fatalf("grant LogsAPIBase = %q", got)
	}
}
