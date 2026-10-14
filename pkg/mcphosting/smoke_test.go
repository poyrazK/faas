package mcphosting

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestSmokeFixtures(t *testing.T) {
	suite, err := LoadSmokeSuite(strings.NewReader(`{"version":1,"cases":[
 {"name":"addition","kind":"tool","target":"add","arguments":{"a":1},"expected_result":{"content":[{"type":"text","text":"private-result"}]}},
 {"name":"resource","kind":"resource","target":"test://greeting","mime_type":"text/plain"},
 {"name":"prompt","kind":"prompt","target":"greeting","message_roles":["user"]},
 {"name":"missing","kind":"auth","credential":"missing","expected_status":401},
 {"name":"malformed","kind":"auth","credential":"malformed","expected_status":401}
 ]}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, broken := range []bool{false, true} {
		t.Run(fmt.Sprint(broken), func(t *testing.T) {
			calls := 0
			requests := 0
			client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				requests++
				if r.Header.Get("Authorization") != "Bearer private-token" {
					w.WriteHeader(401)
					return
				}
				var req struct {
					ID     int    `json:"id"`
					Method string `json:"method"`
				}
				_ = json.NewDecoder(r.Body).Decode(&req)
				w.Header().Set("Content-Type", "application/json")
				result := ""
				switch req.Method {
				case "initialize":
					result = fmt.Sprintf(`{"protocolVersion":%q,"capabilities":{"tools":{}}}`, ProtocolVersion)
				case "notifications/initialized":
					w.WriteHeader(202)
					return
				case "server/discover":
					result = fmt.Sprintf(`{"supportedVersions":[%q],"capabilities":{"tools":{}}}`, ProtocolVersion)
				case "tools/list":
					result = `{"tools":[{"name":"add","inputSchema":{"type":"object"}}]}`
				case "tools/call":
					calls++
					result = `{"content":[{"type":"text","text":"private-result"}]}`
					if broken {
						result = `{"isError":true,"content":[{"type":"text","text":"private-error"}]}`
					}
				case "resources/read":
					result = `{"contents":[{"uri":"test://greeting","mimeType":"text/plain","text":"private-resource"}]}`
					if broken {
						result = `{"contents":[]}`
					}
				case "prompts/get":
					result = `{"messages":[{"role":"user","content":{"type":"text","text":"private-prompt"}}]}`
					if broken {
						result = `{"messages":[{"role":"system","content":{"type":"text","text":"private-prompt"}}]}`
					}
				default:
					t.Errorf("unexpected method %s", req.Method)
					w.WriteHeader(500)
					return
				}
				_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":%s}`, req.ID, result)
			})
			client.Token = "private-token"
			rejected := RunSmoke(context.Background(), client, suite, false)
			if rejected.OK || requests != 0 {
				t.Fatal("unapproved suite reached server")
			}
			report := RunSmoke(context.Background(), client, suite, true)
			if report.OK == broken || calls != 1 || len(report.Checks) != 5 {
				t.Fatalf("calls=%d report=%+v", calls, report)
			}
			raw, _ := json.Marshal(report)
			if strings.Contains(string(raw), "private-") {
				t.Fatalf("private data leaked: %s", raw)
			}
		})
	}
}

func TestSmokeFixtureValidation(t *testing.T) {
	for _, body := range []string{
		`{"version":1,"cases":[]}`,
		`{"version":1,"cases":[{"name":"a","kind":"tool","target":"x"}]}`,
		`{"version":1,"cases":[{"name":"a","kind":"auth","credential":"missing","expected_status":403}]}`,
		`{"version":1,"cases":[{"name":"a","kind":"resource","target":"x","mime_type":"text/plain","unknown":true}]}`,
		`{"version":1,"cases":[{"name":"a","kind":"prompt","target":"x","message_roles":["system"]}]}`,
		`{"version":1,"cases":[{"name":"a","kind":"auth","credential":"missing","expected_status":401},{"name":"a","kind":"auth","credential":"missing","expected_status":401}]}`,
		strings.Repeat(" ", smokeMaxBytes+1),
	} {
		if _, err := LoadSmokeSuite(strings.NewReader(body)); err == nil {
			t.Fatal("accepted invalid fixture")
		}
	}
}

func TestSmokeDeadlineDoesNotExecute(t *testing.T) {
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) { t.Error("expired suite reached server") })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	report := RunSmoke(ctx, client, SmokeSuite{Version: 1, Cases: []SmokeCase{{Name: "resource", Kind: "resource", Target: "test://x", MIMEType: "text/plain"}}}, false)
	if report.OK {
		t.Fatal("expired suite passed")
	}
}
