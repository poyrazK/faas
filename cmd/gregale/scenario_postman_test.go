package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

const postmanTestSchema = "https://schema.getpostman.com/json/collection/v2.1.0/collection.json"

func newPostmanTestImporter() *postmanImporter {
	return &postmanImporter{defaultStatus: 200, variables: map[string]string{}, fields: map[string]string{}, usedNames: map[string]bool{}}
}

func TestPostmanImportGeneratedManifestRunsWithoutPostman(t *testing.T) {
	collection := map[string]any{
		"info":     map[string]any{"schema": postmanTestSchema},
		"variable": []any{map[string]any{"key": "baseUrl", "value": "https://production.invalid/v1"}, map[string]any{"key": "token", "value": "exported-token-never-copy"}},
		"auth":     map[string]any{"type": "bearer", "bearer": []any{map[string]any{"key": "token", "value": "{{token}}"}}},
		"event":    []any{map[string]any{"listen": "prerequest", "script": map[string]any{"exec": []string{"throw new Error('script-must-not-run');"}}}},
		"item": []any{map[string]any{"name": "Customers", "item": []any{
			map[string]any{"name": "Submit", "request": map[string]any{
				"method": "POST", "url": "{{baseUrl}}/exports",
				"header": []any{map[string]any{"key": "X-Customer", "value": "{{customerId}}"}, map[string]any{"key": "X-Disabled", "value": "must-not-copy", "disabled": true}},
				"body":   map[string]any{"mode": "raw", "raw": `{"customer":"{{customerId}}","count":{{count}},"count_text":"{{count}}","enabled":{{enabled}},"static":9007199254740993,"decimal":0.125}`},
			}, "response": []any{map[string]any{"code": 201}},
				"event": []any{map[string]any{"listen": "test", "script": map[string]any{"exec": "pm.test('created', () => pm.response.to.have.status(201));"}}}},
			map[string]any{"name": "Submit", "request": map[string]any{
				"method": "GET", "auth": map[string]any{"type": "noauth"},
				"url": map[string]any{"raw": "{{baseUrl}}/customers/:customerId?removed=must-not-copy", "variable": []any{map[string]any{"key": "customerId", "value": "exported-customer-never-copy"}},
					"query": []any{map[string]any{"key": "q", "value": "{{customerId}}"}, map[string]any{"key": "tag", "value": "a&b"}, map[string]any{"key": "tag", "value": "two words"}, map[string]any{"key": "skip", "value": "{{$randomUUID}}", "disabled": true}}},
			}},
		}}},
	}
	dir := t.TempDir()
	from, output := filepath.Join(dir, "collection.json"), filepath.Join(dir, "gregale-test.yaml")
	body, err := json.Marshal(collection)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(from, body, 0o600); err != nil {
		t.Fatal(err)
	}
	args := []string{"import", "--from", from, "--project", "export-api", "--output", output}
	if code := cmdTest(args); code == 0 {
		t.Fatal("import silently omitted Postman scripts")
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatalf("failed import left a manifest: %v", err)
	}
	oldJSON, oldOut, oldErr := jsonOutput, osStdout, osStderr
	var stdout, stderr bytes.Buffer
	jsonOutput, osStdout, osStderr = true, &stdout, &stderr
	t.Cleanup(func() { jsonOutput, osStdout, osStderr = oldJSON, oldOut, oldErr })
	args = append(args, "--requests-only")
	if code := cmdTest(args); code != 0 {
		t.Fatalf("import exit = %d (%s)", code, stderr.String())
	}
	var imported postmanImportResult
	if err := json.Unmarshal(stdout.Bytes(), &imported); err != nil || imported.Requests != 2 || imported.ScriptsOmitted != 2 || imported.StatusDefaults != 1 || len(imported.RequiredData) != 4 {
		t.Fatalf("import report = (%+v, %v)", imported, err)
	}
	for _, input := range imported.RequiredData {
		if input.Variable == "customerId" && input.Field != "customer_id" {
			t.Fatalf("variable mapping = %+v", input)
		}
	}
	manifest, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{"production.invalid", "exported-token-never-copy", "exported-customer-never-copy", "must-not-copy", "script-must-not-run", "pm.test"} {
		if bytes.Contains(manifest, []byte(private)) || bytes.Contains(stdout.Bytes(), []byte(private)) {
			t.Errorf("import exposed omitted content: %q", private)
		}
	}
	if info, err := os.Stat(output); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("manifest permissions = (%v, %v)", info, err)
	}
	fields := []string{"count", "customer_id", "enabled", "token"}
	scenarios, _, err := readTestManifest(output, fields...)
	if err != nil {
		t.Fatal(err)
	}
	steps := scenarios["api-collection"].Requests
	if len(steps) != 2 || steps[0].Name != "request-submit" || steps[1].Name != "request-submit-2" || steps[0].Path != "/v1/exports" || steps[0].Expect.Status != 201 || steps[0].Headers["Authorization"] != "Bearer ${data.token}" || steps[1].Headers["Authorization"] != "" {
		t.Fatalf("imported steps = %+v", steps)
	}
	var mu sync.Mutex
	var methods []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		methods = append(methods, request.Method)
		switch request.Method {
		case http.MethodPost:
			if request.URL.Path != "/v1/exports" || request.Header.Get("Authorization") != "Bearer runtime-token" || request.Header.Get("X-Customer") != "a/b?&" || request.Header.Get("X-Disabled") != "" {
				t.Errorf("imported submit headers or path = %s, %v", request.URL, request.Header)
			}
			decoder := json.NewDecoder(request.Body)
			decoder.UseNumber()
			var payload map[string]any
			if err := decoder.Decode(&payload); err != nil || payload["customer"] != "a/b?&" || payload["count"] != json.Number("2") || payload["count_text"] != "2" || payload["enabled"] != true || payload["static"] != json.Number("9007199254740993") || payload["decimal"] != json.Number("0.125") {
				t.Errorf("imported JSON body = (%+v, %v)", payload, err)
			}
			w.WriteHeader(http.StatusCreated)
		case http.MethodGet:
			query := request.URL.Query()
			if request.URL.EscapedPath() != "/v1/customers/a%2Fb%3F&" || request.Header.Get("Authorization") != "" || query.Get("q") != "a/b?&" || len(query["tag"]) != 2 || query["tag"][0] != "a&b" || query["tag"][1] != "two words" || query.Has("skip") || query.Has("removed") {
				t.Errorf("imported read = %s, %v", request.URL, request.Header)
			}
			w.WriteHeader(http.StatusOK)
		default:
			t.Errorf("unexpected imported method: %s", request.Method)
		}
	}))
	defer server.Close()
	data := filepath.Join(dir, "cases.json")
	if err := os.WriteFile(data, []byte(`[{"customer_id":"a/b?&","count":2,"enabled":true,"token":"runtime-token"}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	if code := cmdTest([]string{"--engine", "local", "--scenario", "api-collection", "--base-url", server.URL, "--manifest", output, "--data", data}); code != 0 {
		t.Fatalf("imported scenario run exit = %d (%s)", code, stderr.String())
	}
	var receipts []testRunReceipt
	if err := json.Unmarshal(stdout.Bytes(), &receipts); err != nil || len(receipts) != 1 || receipts[0].Status != "passed" || len(receipts[0].Requests) != 2 {
		t.Fatalf("imported run report = (%+v, %v)", receipts, err)
	}
	mu.Lock()
	if strings.Join(methods, ",") != "POST,GET" {
		t.Errorf("folder request order = %v", methods)
	}
	mu.Unlock()
	if code := cmdTest(args); code == 0 {
		t.Fatal("import overwrote an existing manifest")
	}
	if current, err := os.ReadFile(output); err != nil || !bytes.Equal(current, manifest) {
		t.Fatalf("existing manifest changed: %v", err)
	}
}

func TestPostmanImportRejectsUnsupportedSemanticsWithoutCopyingValues(t *testing.T) {
	cases := []struct {
		name string
		edit func(map[string]any, map[string]any)
	}{
		{"multiple origins", func(c, _ map[string]any) {
			c["item"] = append(c["item"].([]any), map[string]any{"request": "https://other.invalid/health"})
		}},
		{"basic auth", func(_, r map[string]any) { r["auth"] = map[string]any{"type": "basic"} }},
		{"literal bearer", func(_, r map[string]any) {
			r["auth"] = map[string]any{"type": "bearer", "bearer": []any{map[string]any{"key": "token", "value": "private-token"}}}
		}},
		{"literal authorization", func(_, r map[string]any) {
			r["header"] = []any{map[string]any{"key": "Authorization", "value": "Bearer private-token"}}
		}},
		{"mixed literal bearer", func(_, r map[string]any) {
			r["auth"] = map[string]any{"type": "bearer", "bearer": []any{map[string]any{"key": "token", "value": "private-token{{suffix}}"}}}
		}},
		{"duplicate header", func(_, r map[string]any) {
			r["header"] = []any{map[string]any{"key": "X-Test", "value": "a"}, map[string]any{"key": "x-test", "value": "b"}}
		}},
		{"URL credentials", func(_, r map[string]any) { r["url"] = "https://user:private-token@example.invalid/health" }},
		{"dynamic variable", func(_, r map[string]any) { r["url"] = "https://example.invalid/{{$randomUUID}}" }},
		{"vault variable", func(_, r map[string]any) {
			r["header"] = []any{map[string]any{"key": "X-Test", "value": "{{vault:private-token}}"}}
		}},
		{"malformed variable", func(_, r map[string]any) { r["url"] = "https://example.invalid/{{broken" }},
		{"native syntax", func(_, r map[string]any) { r["url"] = "https://example.invalid/${data.input}" }},
		{"relative URL", func(_, r map[string]any) { r["url"] = "/health" }},
		{"multipart", func(_, r map[string]any) { r["body"] = map[string]any{"mode": "formdata"} }},
		{"non JSON", func(_, r map[string]any) { r["body"] = map[string]any{"mode": "raw", "raw": "private-token"} }},
		{"null JSON", func(_, r map[string]any) { r["body"] = map[string]any{"mode": "raw", "raw": "null"} }},
		{"templated JSON key", func(_, r map[string]any) { r["body"] = map[string]any{"mode": "raw", "raw": `{"{{key}}":1}`} }},
		{"multiple JSON bodies", func(_, r map[string]any) { r["body"] = map[string]any{"mode": "raw", "raw": "{} {}"} }},
		{"proxy", func(_, r map[string]any) { r["proxy"] = map[string]any{"host": "private-token"} }},
		{"certificate", func(_, r map[string]any) { r["certificate"] = map[string]any{"src": "/private-token"} }},
		{"protocol options", func(c, _ map[string]any) { c["protocolProfileBehavior"] = map[string]any{"followRedirects": true} }},
		{"ambiguous statuses", func(c, _ map[string]any) {
			c["item"].([]any)[0].(map[string]any)["response"] = []any{map[string]any{"code": 200}, map[string]any{"code": 401}}
		}},
		{"invalid saved status", func(c, _ map[string]any) {
			c["item"].([]any)[0].(map[string]any)["response"] = []any{map[string]any{"code": 999}}
		}},
		{"case field collision", func(_, r map[string]any) { r["url"] = "https://example.invalid/{{customer-id}}/{{customer_id}}" }},
		{"GET body", func(_, r map[string]any) { r["method"] = "GET"; r["body"] = map[string]any{"mode": "raw", "raw": `{}`} }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			request := map[string]any{"method": "POST", "url": "https://example.invalid/health"}
			collection := map[string]any{"info": map[string]any{"schema": postmanTestSchema}, "item": []any{map[string]any{"request": request}}}
			tc.edit(collection, request)
			body, err := json.Marshal(collection)
			if err != nil {
				t.Fatal(err)
			}
			if err := newPostmanTestImporter().load(body); err == nil {
				t.Fatal("unsupported collection semantics were accepted")
			} else if strings.Contains(err.Error(), "private-token") {
				t.Fatalf("error disclosed a credential: %v", err)
			}
		})
	}
}

func TestPostmanImportSupportsStructuredURLsInheritanceAndStatusChoice(t *testing.T) {
	collection := map[string]any{"info": map[string]any{"schema": postmanTestSchema},
		"auth":  map[string]any{"type": "basic"}, // Overridden by the folder.
		"event": []any{map[string]any{"disabled": true, "script": map[string]any{"exec": "ignored"}}},
		"item": []any{map[string]any{"auth": map[string]any{"type": "bearer", "bearer": []any{map[string]any{"key": "token", "value": "{{folderToken}}"}}}, "item": []any{
			map[string]any{"name": "Health", "request": map[string]any{"method": "GET", "url": map[string]any{"protocol": "https", "host": []string{"example", "invalid"}, "port": "8443", "path": []string{"health"}}}, "response": []any{map[string]any{"code": 200}, map[string]any{"code": 403}}},
			map[string]any{"name": "Root", "request": "https://example.invalid:8443/"},
		}}},
	}
	body, err := json.Marshal(collection)
	if err != nil {
		t.Fatal(err)
	}
	importer := newPostmanTestImporter()
	importer.explicitStatus, importer.defaultStatus = true, 202
	if err := importer.load(body); err != nil {
		t.Fatal(err)
	}
	if len(importer.steps) != 2 || importer.steps[0].Path != "/health" || importer.steps[1].Path != "/" || importer.steps[0].Expect.Status != 202 || importer.steps[1].Expect.Status != 202 || importer.steps[0].Headers["Authorization"] != "Bearer ${data.folder_token}" || importer.statusDefaults != 2 || importer.scripts != 0 {
		t.Fatalf("structured import = %+v", importer)
	}
}

func TestPostmanImportBoundsDocumentsFoldersRequestsAndInputs(t *testing.T) {
	for _, raw := range []string{`{`, `{"item":[]}`, `{"info":{"schema":"old-version"},"item":[]}`, `{"info":{"schema":"` + postmanTestSchema + `"},"item":[]} {}`} {
		if err := newPostmanTestImporter().load([]byte(raw)); err == nil {
			t.Errorf("invalid document was accepted: %s", raw)
		}
	}
	items := make([]any, 0, 101)
	for i := 0; i < 101; i++ {
		items = append(items, map[string]any{"request": fmt.Sprintf("https://example.invalid/%d", i)})
	}
	var deep any = map[string]any{"request": "https://example.invalid/health"}
	for i := 0; i < 18; i++ {
		deep = map[string]any{"item": []any{deep}}
	}
	var manyFields strings.Builder
	manyFields.WriteString("https://example.invalid/")
	for i := 0; i < 33; i++ {
		_, _ = fmt.Fprintf(&manyFields, "{{field%d}}/", i)
	}
	for _, inputItems := range []any{items, []any{deep}, []any{map[string]any{"request": manyFields.String()}}} {
		body, err := json.Marshal(map[string]any{"info": map[string]any{"schema": postmanTestSchema}, "item": inputItems})
		if err != nil {
			t.Fatal(err)
		}
		if err := newPostmanTestImporter().load(body); err == nil {
			t.Fatal("unbounded collection was accepted")
		}
	}
	path, output := filepath.Join(t.TempDir(), "too-large.json"), filepath.Join(t.TempDir(), "output.yaml")
	if err := os.WriteFile(path, bytes.Repeat([]byte(" "), postmanDocumentLimit+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if code := cmdTestImport([]string{"--from", path, "--project", "example-api", "--output", output}); code == 0 {
		t.Fatal("oversized collection was accepted")
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatal("oversized import left an output file")
	}
	for _, raw := range []string{`{"n":1e-1000000000}`, `{"n":0.123456789012345678901234567890}`} {
		if _, err := newPostmanTestImporter().body(json.RawMessage(`{"mode":"raw","raw":` + strconvQuotePostmanTest(raw) + `}`)); err == nil {
			t.Fatal("JSON number lost precision during import")
		}
	}
}

func strconvQuotePostmanTest(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}
