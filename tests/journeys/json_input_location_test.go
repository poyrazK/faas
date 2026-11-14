package journeys

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestJSONInputLocationsIsolatedAPI(t *testing.T) {
	configured := needs(t, tierSmoke, false)
	t.Parallel()
	const body = "{\r\n  \"private\": \"payload-private-value\",\r\n  \"n\":\r\n}"
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	for _, command := range []string{"invoke", "send"} {
		for _, kind := range []string{"inline", "file", "stdin"} {
			for _, jsonMode := range []bool{false, true} {
				name := command + "/" + kind + "/text"
				if jsonMode {
					name = command + "/" + kind + "/json"
				}
				t.Run(name, func(t *testing.T) {
					tg := &target{api: server.URL, cli: configured.cli, home: t.TempDir(), appsDomain: configured.appsDomain}
					value, source, stdin := body, "inline", ""
					switch kind {
					case "file":
						source = filepath.Join(tg.home, "request.json")
						if err := os.WriteFile(source, []byte(body), 0o600); err != nil {
							t.Fatal(err)
						}
						value = "@" + source
					case "stdin":
						value, source, stdin = "-", "stdin", body
					}
					option := "--payload"
					args := []string{command, "api"}
					if command == "send" {
						option = "--data"
						args = append(args, "--type", "test")
					}
					args = append(args, option, value)
					mode := "--json=false"
					if jsonMode {
						mode = "--json"
					}
					args = append([]string{mode}, args...)
					res := tg.execIn(t, "fp_json-input-private", tg.home, stdin, time.Minute, args...)
					if res.exit != 1 || len(res.stdout) != 0 || calls.Load() != 0 {
						t.Fatalf("invalid input changed exit/output or reached the API: exit=%d calls=%d stderr=%s", res.exit, calls.Load(), res.stderr)
					}
					detail := string(res.stderr)
					if jsonMode {
						var problem struct {
							Code   string `json:"code"`
							Detail string `json:"detail"`
						}
						if err := json.Unmarshal(res.stderr, &problem); err != nil || problem.Code == "" {
							t.Fatal("invalid JSON error envelope")
						}
						detail = problem.Detail
					}
					if !strings.Contains(detail, source+":4:1: "+option+" must be valid JSON") || strings.Contains(detail, "payload-private-value") || strings.Contains(detail, "fp_json-input-private") {
						t.Fatalf("missing location or leaked value: %s", detail)
					}
				})
			}
		}
	}
}

func TestJSONInputValidPayloadIsolatedAPI(t *testing.T) {
	configured := needs(t, tierSmoke, false)
	t.Parallel()
	const input = " \n{\"n\":9007199254740993,\"text\":\"雪🙂\",\"huge\":1e10000}\n"
	var calls atomic.Int32
	received := make(chan json.RawMessage, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodPost || r.URL.Path != "/v1/apps/api/invoke" {
			t.Error("valid invocation changed its API target")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var request struct{ Payload json.RawMessage }
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error("valid payload was not delivered as JSON")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		received <- request.Payload
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"inv-json-input","status":"completed","result":{"ok":true}}`)
	}))
	defer server.Close()
	tg := &target{api: server.URL, cli: configured.cli, home: t.TempDir(), appsDomain: configured.appsDomain}
	res := tg.execIn(t, "fp_json-input-private", tg.home, input, time.Minute, "--json", "invoke", "api", "--payload", "-")
	if res.exit != 0 || calls.Load() != 1 {
		t.Fatalf("valid input failed: exit=%d calls=%d stderr=%s", res.exit, calls.Load(), res.stderr)
	}
	var expected bytes.Buffer
	if err := json.Compact(&expected, []byte(input)); err != nil {
		t.Fatal(err)
	}
	select {
	case payload := <-received:
		if !bytes.Equal(payload, expected.Bytes()) {
			t.Fatal("payload numbers or text changed in transit")
		}
	default:
		t.Fatal("valid input was not delivered")
	}
}
