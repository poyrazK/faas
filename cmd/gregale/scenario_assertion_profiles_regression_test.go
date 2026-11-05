package main_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
)

func TestScenarioAssertionProfilesPreserveExactJSON(t *testing.T) {
	for _, tc := range []struct {
		name, expected, matching, mismatch string
	}{
		{"integer", "9007199254740993", "9007199254740993", "9007199254740992"},
		{"decimal", "9007199254740993.0", "9007199254740993", "9007199254740992"},
		{"fraction", "0.10000000000000001", "0.10000000000000001", "0.1"},
		{"large exponent", "1e400", "1e400", "1e399"},
		{"small exponent", "1e-400", "1e-400", "0"},
		{"nested", "{items: [9007199254740993.0, true, null, text]}", `{"items":[9007199254740993,true,null,"text"]}`, `{"items":[9007199254740992,true,null,"text"]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manifestPath := filepath.Join(t.TempDir(), "gregale-test.yaml")
			main.WriteSuiteTestFile(t, manifestPath, fmt.Sprintf(`version: 1
assertion_profiles:
  precise:
    request_deadlines:
      query:
        within: 300ms
        min_samples: 1
        expect: {status: 200, json: {'/value': %s}}
scenarios:
  exact-profile:
    project: exact-profile
    services:
      cache: {source: .}
    steps:
      - name: baseline
        requests: [{name: initial, method: GET, path: /health, expect: {status: 200}}]
      - name: exercise
        requests: [{name: query, method: GET, path: /query, expect: {status: 200}}]
        load:
          vus: 1
          duration: 3s
          chaos_schedule:
            - {name: baseline, duration: 1s, assertions: [precise]}
            - name: fault
              duration: 1s
              assertions: [precise]
              rules: [{to: cache, kind: latency, latency: 100ms, percent: 100}]
            - {name: recovery, duration: 1s}
      - name: restored
        requests: [{name: final, method: GET, path: /health, expect: {status: 200}}]
`, tc.expected))
			manifest, _, err := main.ReadTestManifestDocument(manifestPath, func(string, main.TestScenario) []string { return nil })
			if err != nil {
				t.Fatal(err)
			}
			original := manifest.AssertionProfiles["precise"].RequestDeadlines["query"].Expect
			phases := manifest.Scenarios["exact-profile"].Steps[1].Load.ChaosSchedule
			compiled := phases[0].RequestDeadlines["query"].Expect
			originalJSON, err := json.Marshal(original.JSON)
			if err != nil {
				t.Fatal(err)
			}
			compiledJSON, err := json.Marshal(compiled.JSON)
			if err != nil || !bytes.Equal(originalJSON, compiledJSON) {
				t.Fatalf("profile JSON changed from %s to %s: %v", originalJSON, compiledJSON, err)
			}
			for _, response := range []struct {
				name, value string
				pass        bool
			}{{"matching", tc.matching, true}, {"mismatch", tc.mismatch, false}} {
				t.Run(response.name, func(t *testing.T) {
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						_, _ = fmt.Fprintf(w, `{"value":%s}`, response.value)
					}))
					defer server.Close()
					requests := []main.TestHTTPRequest{{Name: "query", Method: "GET", Path: "/query", Expect: *compiled}}
					evidence, err := main.RunTestHTTPRequests(t.Context(), server.URL, "exact-profile", nil, requests, nil)
					if len(evidence) != 1 || evidence[0].Passed != response.pass || (err == nil) != response.pass {
						t.Fatalf("response %s: %+v, %v", response.value, evidence, err)
					}
				})
			}
			// Expanding the same profile into another phase must still isolate
			// mutable expectations from both the source and other expansions.
			compiled.JSON["/value"] = "changed"
			for _, unchanged := range []main.TestHTTPExpect{*original, *phases[1].RequestDeadlines["query"].Expect} {
				encoded, err := json.Marshal(unchanged.JSON)
				if err != nil || !bytes.Equal(originalJSON, encoded) {
					t.Fatalf("profile expectations share mutable state: %s (%v)", encoded, err)
				}
			}
		})
	}
}
