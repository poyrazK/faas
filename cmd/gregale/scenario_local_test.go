package main

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestLocalScenarioRunsTypedCasesWithFreshCapturesAndReports(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FAAS_TOKEN", "account-secret")
	t.Setenv("GREGALE_TEST_URL", "https://production.example")
	t.Setenv("GREGALE_TEST_CONSUMER_CUSTOMER_A_KEY", "local-consumer-secret")
	var mu sync.Mutex
	submitted := map[string]map[string]any{}
	seenRuns := map[string]bool{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch request.Method {
		case http.MethodPost:
			if request.URL.Path != "/exports" || request.Header.Get("Authorization") != "Bearer local-consumer-secret" {
				t.Errorf("unexpected submission: %s %s", request.Method, request.URL)
				w.WriteHeader(http.StatusForbidden)
				return
			}
			decoder := json.NewDecoder(request.Body)
			decoder.UseNumber()
			var payload map[string]any
			if err := decoder.Decode(&payload); err != nil {
				t.Errorf("decode submitted JSON: %v", err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			if _, ok := payload["count"].(json.Number); !ok {
				t.Errorf("count lost its JSON number type: %T", payload["count"])
			}
			if _, ok := payload["enabled"].(bool); !ok {
				t.Errorf("enabled lost its JSON boolean type: %T", payload["enabled"])
			}
			run, _ := payload["run"].(string)
			if run == "" || seenRuns[run] {
				t.Errorf("run identity was missing or reused: %q", run)
			}
			seenRuns[run] = true
			id := fmt.Sprintf("private/id-%d", len(submitted)+1)
			submitted[id] = payload
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]string{"id": id})
		case http.MethodGet:
			id := strings.TrimPrefix(request.URL.Path, "/exports/")
			payload, exists := submitted[id]
			if !exists || request.URL.Query().Get("customer") != payload["customer"] || request.Header.Get("X-Customer") != payload["customer"] {
				t.Errorf("capture or case data mismatch for read: %s", request.URL)
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_ = json.NewEncoder(w).Encode(payload)
		default:
			t.Errorf("unexpected request method: %s", request.Method)
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	defer server.Close()
	manifest := filepath.Join(dir, "gregale-test.yaml")
	contents := `version: 1
scenarios:
  customer-export:
    project: export-api
    consumers: [{name: customer-a}]
    setup:
      - [sh, -c, 'test -z "$FAAS_TOKEN" && test "$GREGALE_TEST_ENGINE" = local && test "$GREGALE_TEST_PROFILE" = local && test "$GREGALE_TEST_CONSUMER_CUSTOMER_A_KEY" = local-consumer-secret && test -n "$GREGALE_TEST_DATA_JSON" && printf "setup:%s\n" "$GREGALE_TEST_CASE" >> order.log']
    trigger: [sh, -c, 'printf "trigger:%s\n" "$GREGALE_TEST_CASE" >> order.log']
    requests:
      - name: submit
        as: customer-a
        method: POST
        path: /exports
        json: {customer: '${data.customer}', count: '${data.count}', enabled: '${data.enabled}', label: 'count-${data.count}', run: '${run.id}'}
        expect: {status: 201}
        capture: {export_id: '/id'}
    checks:
      - name: read
        method: GET
        path: /exports/${steps.submit.export_id}?customer=${data.customer}
        headers: {X-Customer: '${data.customer}'}
        expect:
          status: 200
          content_type: application/json
          json: {'/customer': '${data.customer}', '/count': '${data.count}', '/enabled': '${data.enabled}', '/label': 'count-${data.count}', '/run': '${run.id}'}
    command: [sh, -c, 'printf "assert:%s\n" "$GREGALE_TEST_CASE" >> order.log; echo assertion-output']
    cleanup:
      - [sh, -c, 'printf "cleanup:%s\n" "$GREGALE_TEST_CASE" >> order.log']
`
	if err := os.WriteFile(manifest, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	data := filepath.Join(dir, "cases.json")
	if err := os.WriteFile(data, []byte(`[{"customer":"private/customer?&${literal}","count":9007199254740993,"enabled":true},{"customer":"private/second","count":2,"enabled":false}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readTestManifest(manifest); err == nil {
		t.Fatal("manifest with data references was accepted without a data file")
	}
	if code := cmdTest([]string{"--validate", "--engine", "local", "--manifest", manifest, "--data", data}); code != 0 {
		t.Fatalf("local validation required deployable source: exit %d", code)
	}
	oldJSON, oldOut, oldErr := jsonOutput, osStdout, osStderr
	var stdout, stderr bytes.Buffer
	jsonOutput, osStdout, osStderr = true, &stdout, &stderr
	t.Cleanup(func() { jsonOutput, osStdout, osStderr = oldJSON, oldOut, oldErr })
	report, junit := filepath.Join(dir, "results.json"), filepath.Join(dir, "results.xml")
	if code := cmdTest([]string{"--scenario", "customer-export", "--engine", "local", "--base-url", server.URL, "--manifest", manifest, "--data", data, "--repeat", "2", "--report", report, "--junit", junit}); code != 0 {
		t.Fatalf("local run exit %d: %s", code, stderr.String())
	}
	var receipts []testRunReceipt
	if err := json.Unmarshal(stdout.Bytes(), &receipts); err != nil || len(receipts) != 4 {
		t.Fatalf("JSON output = %s (%v)", stdout.String(), err)
	}
	for i, receipt := range receipts {
		if receipt.Engine != "local" || receipt.Profile != "local" || receipt.Case != fmt.Sprintf("row-%d", i%2+1) || receipt.Attempt != i/2+1 || receipt.Status != "passed" || receipt.Evidence != nil || receipt.AppSlug != "" || len(receipt.Requests) != 2 || len(receipt.Phases) != 4 {
			t.Fatalf("local receipt %d = %+v", i, receipt)
		}
	}
	mu.Lock()
	if len(submitted) != 4 || len(seenRuns) != 4 {
		t.Errorf("runs = %d, submissions = %d", len(seenRuns), len(submitted))
	}
	mu.Unlock()
	for _, path := range []string{report, junit} {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, private := range []string{"local-consumer-secret", "account-secret", "private/customer", "private/second", "private/id-", "9007199254740993"} {
			if bytes.Contains(body, []byte(private)) {
				t.Errorf("%s exposed a credential, data value, or capture", path)
			}
		}
		if path == junit {
			var suite testJUnitSuite
			if err := xml.Unmarshal(body, &suite); err != nil || suite.Tests != 4 || suite.Failures != 0 {
				t.Fatalf("JUnit suite = (%+v, %v)", suite, err)
			}
			for i, result := range suite.Cases {
				if result.Name != fmt.Sprintf("customer-export/local/row-%d#%d", i%2+1, i/2+1) || result.ClassName != "gregale.scenario.local" {
					t.Errorf("JUnit case = %+v", result)
				}
			}
		}
	}
	order, err := os.ReadFile(filepath.Join(dir, "order.log"))
	if err != nil {
		t.Fatal(err)
	}
	var expected strings.Builder
	for i := 0; i < 4; i++ {
		for _, phase := range []string{"setup", "trigger", "assert", "cleanup"} {
			_, _ = fmt.Fprintf(&expected, "%s:row-%d\n", phase, i%2+1)
		}
	}
	if string(order) != expected.String() || strings.Count(stderr.String(), "assertion-output") != 4 {
		t.Fatalf("command order = %q, command output = %q", order, stderr.String())
	}
}

func TestLocalScenarioCleansUpFailuresAndCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusForbidden) }))
	defer server.Close()
	for _, failure := range []string{"setup", "request", "command", "cleanup", "canceled"} {
		t.Run(failure, func(t *testing.T) {
			dir := t.TempDir()
			scenario := testScenario{Command: []string{"sh", "-c", "exit 0"}, Cleanup: [][]string{{"sh", "-c", "printf cleaned > cleanup.txt"}}}
			ctx := context.Background()
			switch failure {
			case "setup":
				scenario.Setup = [][]string{{"sh", "-c", "exit 1"}}
			case "request":
				scenario.Requests = []testHTTPRequest{{Name: "denied", Method: "GET", Path: "/", Expect: testHTTPExpect{Status: 200}}}
			case "command":
				scenario.Command = []string{"sh", "-c", "exit 1"}
			case "cleanup":
				scenario.Cleanup = append(scenario.Cleanup, []string{"sh", "-c", "exit 1"}, []string{"sh", "-c", "printf continued > after.txt"})
			case "canceled":
				canceled, cancel := context.WithCancel(ctx)
				cancel()
				ctx = canceled
			}
			receipt := runLocalTest(ctx, "failure", scenario, dir, server.URL, testDataCase{})
			if receipt.Status != "failed" || (receipt.Error == "" && receipt.CleanupError == "") {
				t.Fatalf("failed receipt = %+v", receipt)
			}
			body, err := os.ReadFile(filepath.Join(dir, "cleanup.txt"))
			if err != nil || string(body) != "cleaned" {
				t.Fatalf("cleanup = (%q, %v)", body, err)
			}
			if failure == "cleanup" {
				if receipt.CleanupError == "" {
					t.Fatal("cleanup failure was missing from receipt")
				}
				if _, err := os.Stat(filepath.Join(dir, "after.txt")); err != nil {
					t.Fatal("cleanup stopped after first failure")
				}
			}
		})
	}
}

func TestLocalScenarioCSVContinuesAfterFailedRowAndReturnsFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/denied" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"enabled":"true","count":"2"}`))
	}))
	defer server.Close()
	dir := t.TempDir()
	manifest, data, report, junit := filepath.Join(dir, "gregale-test.yaml"), filepath.Join(dir, "cases.csv"), filepath.Join(dir, "results.json"), filepath.Join(dir, "results.xml")
	contents := `version: 1
scenarios:
  csv-test:
    project: csv-api
    requests:
      - name: read
        method: GET
        path: /${data.path}
        expect:
          status: 200
          json: {'/enabled': '${data.enabled}', '/count': '${data.count}'}
    cleanup:
      - [sh, -c, 'printf "%s\n" "$GREGALE_TEST_CASE" >> cleanup.log']
`
	if err := os.WriteFile(manifest, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(data, []byte("path,enabled,count\ndenied,true,2\nallowed,true,2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code := cmdTest([]string{"--scenario", "csv-test", "--engine", "local", "--base-url", server.URL, "--manifest", manifest, "--data", data, "--report", report, "--junit", junit}); code != 1 {
		t.Fatalf("partial failure exit = %d, want 1", code)
	}
	body, err := os.ReadFile(report)
	if err != nil {
		t.Fatal(err)
	}
	var receipts []testRunReceipt
	if err := json.Unmarshal(body, &receipts); err != nil || len(receipts) != 2 || receipts[0].Status != "failed" || receipts[1].Status != "passed" {
		t.Fatalf("CSV receipts = (%+v, %v)", receipts, err)
	}
	body, err = os.ReadFile(junit)
	if err != nil {
		t.Fatal(err)
	}
	var suite testJUnitSuite
	if err := xml.Unmarshal(body, &suite); err != nil || suite.Tests != 2 || suite.Failures != 1 {
		t.Fatalf("partial failure JUnit = (%+v, %v)", suite, err)
	}
	body, err = os.ReadFile(filepath.Join(dir, "cleanup.log"))
	if err != nil || string(body) != "row-1\nrow-2\n" {
		t.Fatalf("CSV row cleanup = (%q, %v)", body, err)
	}
}

func TestLocalScenarioSelectionDoesNotBindOtherScenariosData(t *testing.T) {
	dir := t.TempDir()
	manifest := filepath.Join(dir, "gregale-test.yaml")
	contents := `version: 1
scenarios:
  smoke-test:
    project: smoke-api
    command: [sh, -c, 'test "$GREGALE_TEST_DATA_JSON" = "{}"']
  data-test:
    project: data-api
    requests:
      - name: read
        method: GET
        path: /${data.customer}
        headers: {X-Label: '${data.label}'}
        expect: {status: 200, json: {'/active': '${data.active}'}}
`
	if err := os.WriteFile(manifest, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	if code := cmdTest([]string{"--scenario", "smoke-test", "--engine", "local", "--manifest", manifest, "--base-url", "http://localhost:3000"}); code != 0 {
		t.Fatalf("unselected data scenario blocked smoke run: exit %d", code)
	}
	if code := cmdTest([]string{"--scenario", "data-test", "--engine", "local", "--validate", "--manifest", manifest}); code == 0 {
		t.Fatal("selected data scenario accepted unresolved data references")
	}
	// A deployable source lets real-VM validation select the ordinary scenario
	// from a mixed manifest without a data file or platform login.
	if err := os.WriteFile(filepath.Join(dir, "handler.js"), []byte("exports.handler = async () => ({statusCode: 200});\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code := cmdTest([]string{"--scenario", "smoke-test", "--validate", "--manifest", manifest}); code != 0 {
		t.Fatalf("unselected data scenario blocked real-VM validation: exit %d", code)
	}
}

func TestLocalScenarioRejectsPlatformWaitsMissingKeysAndInvalidURLs(t *testing.T) {
	for _, raw := range []string{"", "https://localhost:3000", "http://example.com", "http://192.168.1.3", "http://user:secret@localhost", "http://localhost/path", "http://localhost?key=value", "http://localhost?", "http://localhost#fragment", "http://localhost#", "http://localhost:0", "http://localhost:65536"} {
		if _, err := validateLocalTestURL(raw); err == nil {
			t.Errorf("invalid local origin was accepted: %q", raw)
		}
	}
	for _, raw := range []string{"http://localhost:3000/", "http://127.0.0.1:3000", "http://[::1]:3000"} {
		if got, err := validateLocalTestURL(raw); err != nil || got != strings.TrimSuffix(raw, "/") {
			t.Errorf("local origin = (%q, %v)", got, err)
		}
	}
	for _, condition := range []testWaitFor{{QueueIdle: true}, {Objects: []testObjectOutput{{Bucket: "exports"}}}, {Invocations: []testInvocationOutput{{Service: "worker"}}}, {Deliveries: []testDeliveryOutput{{Service: "sink"}}}} {
		if err := validateLocalTestScenario(testScenario{WaitFor: condition}); err == nil {
			t.Errorf("local run accepted platform wait: %+v", condition)
		}
	}
	t.Setenv("GREGALE_TEST_CONSUMER_BUYER_KEY", "")
	scenario := testScenario{Consumers: []testConsumer{{Name: "buyer"}}, Checks: []testHTTPRequest{{As: "buyer"}}}
	if _, err := localTestConsumerEnv(scenario); err == nil || !strings.Contains(err.Error(), "GREGALE_TEST_CONSUMER_BUYER_KEY") {
		t.Fatalf("missing local consumer key = %v", err)
	}
	t.Setenv("GREGALE_TEST_CONSUMER_BUYER_KEY", "test-key")
	if env, err := localTestConsumerEnv(scenario); err != nil || len(env) != 1 {
		t.Fatalf("local consumer env = (%v, %v)", env, err)
	}
	for _, args := range [][]string{
		{"--scenario", "local-test", "--data", "cases.json"},
		{"--scenario", "local-test", "--engine", "simulated", "--base-url", "http://localhost:3000"},
		{"--scenario", "local-test", "--engine", "local", "--profile", "warm"},
		{"--scenario", "local-test", "--engine", "local", "--max-workload-minutes", "1"},
		{"--scenario", "local-test", "--engine", "local"},
	} {
		if code := cmdTest(args); code == 0 {
			t.Errorf("invalid flags were accepted: %v", args)
		}
	}
}
