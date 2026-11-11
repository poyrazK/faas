package main_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"gopkg.in/yaml.v3"
)

func TestScenarioManifestJSONNumbersKeepTheirExactValues(t *testing.T) {
	for _, tc := range []struct {
		name, literal, response string
		pass                    bool
	}{
		{"large decimal matches", "9007199254740993.0", "9007199254740993", true},
		{"rounded integer differs", "9007199254740993.0", "9007199254740992", false},
		{"decimal precision", "0.10000000000000001", "0.1", false},
		{"large exponent", "9.007199254740993e15", "9007199254740993", true},
		{"YAML decimal spelling", "+000.125_0", "0.125", true},
		{"integer above uint64", "18446744073709551617", "18446744073709551617", true},
		{"exponent outside float64", "1e400", "1e400", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, roundTrip := range []bool{false, true} {
				t.Run(fmt.Sprintf("round-trip=%t", roundTrip), func(t *testing.T) {
					stdout, _ := main.CaptureSuiteTestOutput(t)
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						body, err := io.ReadAll(r.Body)
						var payload map[string]any
						decoder := json.NewDecoder(strings.NewReader(string(body)))
						decoder.UseNumber()
						decodeErr := decoder.Decode(&payload)
						want := tc.literal
						if want == "+000.125_0" {
							want = "0.1250"
						}
						value, numeric := payload["value"].(json.Number)
						nested, array := payload["nested"].([]any)
						if err != nil || decodeErr != nil || !numeric || string(value) != want || !array || len(nested) != 1 || nested[0] != json.Number(want) {
							t.Errorf("request number changed: %s (%v)", body, err)
						}
						_, _ = fmt.Fprintf(w, `{"value":%s}`, tc.response)
					}))
					defer server.Close()
					manifest := filepath.Join(t.TempDir(), "gregale-test.yaml")
					main.WriteSuiteTestFile(t, manifest, `version: 1
scenarios:
  audit:
    project: audit-app
    requests:
      - name: exact
        method: POST
        path: /numbers
        json: {value: `+tc.literal+`, nested: [`+tc.literal+`]}
        expect: {status: 200, json: {'/value': `+tc.literal+`}}
`)
					if roundTrip {
						scenarios, _, err := main.ReadTestManifest(manifest)
						if err != nil {
							t.Fatal(err)
						}
						body, err := yaml.Marshal(main.TestManifest{Version: 1, Scenarios: scenarios})
						if err != nil {
							t.Fatal(err)
						}
						main.WriteSuiteTestFile(t, manifest, string(body))
					}
					code, problem := main.RunWithStderr(t, func() int {
						return main.CmdTest([]string{"--manifest", manifest, "--scenario", "audit", "--engine", "local", "--base-url", server.URL})
					})
					var receipts []main.TestRunReceipt
					if err := json.Unmarshal(stdout.Bytes(), &receipts); err != nil || len(receipts) != 1 || (receipts[0].Status == "passed") != tc.pass || (code == 0) != tc.pass {
						t.Fatalf("exact comparison exit %d: %s; %s (%v)", code, problem, stdout, err)
					}
				})
			}
		})
	}
}

func TestScenarioManifestJSONAliasesAndStrictSchema(t *testing.T) {
	manifest := filepath.Join(t.TempDir(), "gregale-test.yaml")
	base := `version: 1
scenarios:
  audit:
    project: audit-app
    requests:
      - &base
        name: first
        method: POST
        path: /payload
        json: &payload {value: 9007199254740993.0}
        expect: {status: 200, json: {'/value': 9007199254740993.0}}
      - <<: *base
        name: second
        json: {<<: *payload, other: [*payload]}
`
	main.WriteSuiteTestFile(t, manifest, base)
	scenarios, _, err := main.ReadTestManifest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(scenarios["audit"].Requests[1].JSON)
	if err != nil || string(encoded) != `{"other":[{"value":9007199254740993.0}],"value":9007199254740993.0}` {
		t.Fatalf("merged JSON changed: %s (%v)", encoded, err)
	}
	for _, invalid := range []string{
		strings.Replace(base, "        method: POST", "        method: POST\n        unsupported: true", 1),
		strings.Replace(base, "expect: {status: 200, json:", "expect: {unsupported: true, status: 200, json:", 1),
		strings.Replace(base, "value: 9007199254740993.0", "value: .nan", 1),
	} {
		main.WriteSuiteTestFile(t, manifest, invalid)
		if _, _, err := main.ReadTestManifest(manifest); err == nil {
			t.Fatal("custom HTTP JSON decoder accepted an unknown field or non-finite number")
		}
	}
}

func TestScenarioManifestNullJSONIsPartOfReplayIdentity(t *testing.T) {
	manifest := filepath.Join(t.TempDir(), "gregale-test.yaml")
	base := "version: 1\nscenarios:\n  audit:\n    project: audit-app\n    requests:\n      - name: payload\n        method: POST\n        path: /payload\n        expect: {status: 200}\n"
	main.WriteSuiteTestFile(t, manifest, base)
	without, _, err := main.ReadTestManifest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	main.WriteSuiteTestFile(t, manifest, base+"        json: null\n")
	with, _, err := main.ReadTestManifest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	old := main.NewTestReplayManifest("audit", "warm", "", without["audit"])
	current := main.NewTestReplayManifest("audit", "warm", "", with["audit"])
	if old == nil || current == nil || old.ConfigurationSHA256 == current.ConfigurationSHA256 {
		t.Fatal("explicit JSON null did not change the replay configuration identity")
	}
	for _, method := range []string{"GET", "HEAD"} {
		main.WriteSuiteTestFile(t, manifest, strings.Replace(base+"        json: null\n", "method: POST", "method: "+method, 1))
		if _, _, err := main.ReadTestManifest(manifest); err == nil {
			t.Errorf("explicit JSON null was accepted for %s", method)
		}
	}
}

func TestScenarioManifestDistinguishesNullJSONFromNoBody(t *testing.T) {
	for _, body := range []string{"", "null", "false", "'null'"} {
		for _, load := range []bool{false, true} {
			t.Run(fmt.Sprintf("body=%s/load=%t", body, load), func(t *testing.T) {
				main.CaptureSuiteTestOutput(t)
				want := body
				if body == "'null'" {
					want = `"null"`
				}
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					got, err := io.ReadAll(r.Body)
					if err != nil || string(got) != want || (r.Header.Get("Content-Type") == "application/json") != (body != "") {
						t.Errorf("body=%q, Content-Type=%q, want %q (%v)", got, r.Header.Get("Content-Type"), want, err)
					}
					w.WriteHeader(http.StatusOK)
				}))
				defer server.Close()
				manifest := filepath.Join(t.TempDir(), "gregale-test.yaml")
				jsonLine := ""
				if body != "" {
					jsonLine = "        json: " + body + "\n"
				}
				main.WriteSuiteTestFile(t, manifest, "version: 1\nscenarios:\n  audit:\n    project: audit-app\n    requests:\n      - name: payload\n        method: POST\n        path: /payload\n"+jsonLine+"        expect: {status: 200}\n")
				scenarios, _, err := main.ReadTestManifest(manifest)
				if err != nil {
					t.Fatal(err)
				}
				manifest = main.WriteSuiteTestManifest(t, filepath.Dir(manifest), main.TestManifest{Version: 1, Scenarios: scenarios})
				args := []string{"--manifest", manifest, "--scenario", "audit", "--engine", "local", "--base-url", server.URL}
				if load {
					args = append(args, "--load", "--iterations", "1")
				}
				if code, problem := main.RunWithStderr(t, func() int { return main.CmdTest(args) }); code != 0 {
					t.Fatalf("body execution exit %d: %s", code, problem)
				}
			})
		}
	}
}
