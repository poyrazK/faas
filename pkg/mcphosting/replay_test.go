package mcphosting

// adr: 20261122194324 — replay compares a candidate with the serving
// deployment without capturing traffic.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

type replayServer struct {
	tools, resources string
	resourceText     string
	toolFails        bool
	calls            int
}

func (s *replayServer) handle(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     int    `json:"id"`
			Method string `json:"method"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		w.Header().Set("Content-Type", "application/json")
		result := `{}`
		switch req.Method {
		case "initialize":
			result = fmt.Sprintf(`{"protocolVersion":%q,"capabilities":{"tools":{},"resources":{},"prompts":{}}}`, ProtocolVersion)
		case "notifications/initialized":
			w.WriteHeader(202)
			return
		case "server/discover":
			result = fmt.Sprintf(`{"supportedVersions":[%q],"capabilities":{"tools":{},"resources":{},"prompts":{}}}`, ProtocolVersion)
		case "tools/list":
			result = `{"tools":` + s.tools + `}`
		case "resources/list":
			result = `{"resources":` + s.resources + `}`
		case "resources/templates/list":
			result = `{"resourceTemplates":[]}`
		case "prompts/list":
			result = `{"prompts":[]}`
		case "resources/read":
			result = `{"contents":[{"uri":"test://status","mimeType":"text/plain","text":"` + s.resourceText + `"}]}`
		case "tools/call":
			s.calls++
			if s.toolFails {
				_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"error":{"code":-32603,"message":"boom"}}`, req.ID)
				return
			}
			result = `{"content":[{"type":"text","text":"ok"}]}`
		default:
			t.Errorf("unexpected method %s", req.Method)
		}
		_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":%s}`, req.ID, result)
	}
}

const replaySearch = `{"name":"search","inputSchema":{"type":"object","properties":{"q":{"type":"string"}}}}`

func TestReplayComparesCandidateWithServing(t *testing.T) {
	fixtures, err := LoadSmokeSuite(strings.NewReader(`{"version":1,"cases":[{"name":"find","kind":"tool","target":"search","arguments":{"q":"x"},"expected_result":{"content":[{"type":"text","text":"ok"}]}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	resources := `[{"uri":"test://status","name":"status"}]`
	for _, tc := range []struct {
		name      string
		candidate replayServer
		allow     []string
		ok        bool
		outcomes  []string
		calls     int
	}{
		{"identical", replayServer{tools: "[" + replaySearch + "]", resources: resources, resourceText: "up"}, []string{"search"}, true, []string{"same", "same"}, 2},
		{"resource changed", replayServer{tools: "[" + replaySearch + "]", resources: resources, resourceText: "down"}, nil, true, []string{"changed"}, 0},
		{"tool newly fails", replayServer{tools: "[" + replaySearch + "]", resources: resources, resourceText: "up", toolFails: true}, []string{"search"}, false, []string{"same", "new_error"}, 2},
		{"tool removed", replayServer{tools: "[]", resources: resources, resourceText: "up"}, nil, false, []string{"same"}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			serving := &replayServer{tools: "[" + replaySearch + "]", resources: resources, resourceText: "up"}
			candidate := tc.candidate
			report := Replay(context.Background(), testClient(t, serving.handle(t)), testClient(t, candidate.handle(t)), ReplayOptions{Runs: 2, Fixtures: &fixtures, AllowTools: tc.allow})
			if len(report.Problems) > 0 {
				t.Fatalf("problems: %v", report.Problems)
			}
			if report.OK != tc.ok {
				t.Fatalf("OK=%v catalog=%+v probes=%+v", report.OK, report.Catalog, report.Probes)
			}
			var outcomes []string
			for _, p := range report.Probes {
				outcomes = append(outcomes, p.Outcome)
			}
			if strings.Join(outcomes, ",") != strings.Join(tc.outcomes, ",") {
				t.Fatalf("outcomes = %v", outcomes)
			}
			if candidate.calls != tc.calls || serving.calls != tc.calls {
				t.Fatalf("tool calls serving=%d candidate=%d want %d each", serving.calls, candidate.calls, tc.calls)
			}
			if tc.allow == nil && len(report.Skipped) != 1 {
				t.Fatalf("a tool fixture without --allow-tool must be skipped, not run: %v", report.Skipped)
			}
		})
	}
}
