package journeys

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestLogExplanationSnapshot(t *testing.T) {
	configured := needs(t, tierSmoke, false)
	const deployment = "7971fae0-6fd8-4425-b65e-e0c231ad1155"
	secret := "sk_live_" + strings.Repeat("a", 32)
	var partial atomic.Bool
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method != http.MethodGet {
			t.Errorf("explanation attempted %s", r.Method)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		switch r.URL.Path {
		case "/v1/deployments/" + deployment:
			if err := json.NewEncoder(w).Encode(map[string]string{"id": deployment, "app_id": "app-demo", "status": "failed", "error": "build failed with " + secret}); err != nil {
				t.Error(err)
			}
		case "/v1/apps/demo":
			if err := json.NewEncoder(w).Encode(map[string]string{"id": "app-demo", "slug": "demo"}); err != nil {
				t.Error(err)
			}
		case "/v1/deployments/" + deployment + "/logs":
			if r.URL.Query().Get("follow") != "0" || r.URL.Query().Get("limit") != "500" {
				t.Error("snapshot is not bounded")
			}
			w.Header().Set("Content-Type", "text/event-stream")
			row, err := json.Marshal(map[string]string{"line": "startup error " + secret, "level": "error"})
			if err != nil {
				t.Error(err)
				return
			}
			_, _ = fmt.Fprintf(w, "event: log\ndata: %s\n\n", row)
			if !partial.Load() {
				_, _ = fmt.Fprint(w, "event: end\ndata: {}\n\n")
			}
		default:
			t.Errorf("unexpected API read: %s", r.URL)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	tg := &target{api: server.URL, cli: configured.cli, home: t.TempDir(), appsDomain: configured.appsDomain}
	for _, mode := range []string{"complete", "partial_eof"} {
		t.Run(mode, func(t *testing.T) {
			partial.Store(mode == "partial_eof")
			res := tg.exec(t, "fp_live_fixture", t.TempDir(), time.Minute, "logs", "demo", "--deployment", deployment, "--explain", "--json")
			var report struct {
				Type         string   `json:"type"`
				Deployment   string   `json:"deployment_id"`
				Source       string   `json:"source"`
				ErrorCount   int      `json:"error_count"`
				Observed     int      `json:"observed_lines"`
				Complete     bool     `json:"complete"`
				EndReason    string   `json:"end_reason"`
				NextCommands []string `json:"next_commands"`
			}
			decoder := json.NewDecoder(strings.NewReader(string(res.stdout)))
			if err := decoder.Decode(&report); err != nil {
				t.Fatalf("explanation is not JSON: %v: %s", err, res.stderr)
			}
			if err := decoder.Decode(new(any)); err != io.EOF {
				t.Fatal("stdout includes extra objects or raw log records")
			}
			wantCode, wantReason := 0, "end"
			if partial.Load() {
				wantCode, wantReason = 3, "eof"
			}
			if res.exit != wantCode || report.Complete == partial.Load() || report.EndReason != wantReason || report.Type != "explanation" || report.Source != "build/deploy" || report.Deployment != deployment || report.ErrorCount != 1 || report.Observed != 1 || len(report.NextCommands) == 0 {
				t.Fatalf("unexpected snapshot result: exit=%d, %+v", res.exit, report)
			}
			if strings.Contains(string(res.stdout)+string(res.stderr), secret) {
				t.Fatal("explanation disclosed a credential")
			}
		})
	}
	if requests.Load() != 6 {
		t.Fatalf("unexpected read count: %d", requests.Load())
	}
}
