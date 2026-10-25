// adr: 521
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/operations"
)

const developerDefinitionID = "11111111-1111-1111-1111-111111111111"
const developerOperationID = "22222222-2222-2222-2222-222222222222"

type customerOperationSubmissionTestClient struct {
	identity api.OperationTenantIdentity
	origin   string
	submit   func(api.OperationStartRequest, string) (api.OperationAcceptedResponse, error)
}

func (c customerOperationSubmissionTestClient) BaseURL() string { return c.origin }
func (c customerOperationSubmissionTestClient) GetPlatformTenantSelfOperationIdentity(context.Context) (api.OperationTenantIdentity, error) {
	return c.identity, nil
}
func (c customerOperationSubmissionTestClient) StartPlatformTenantSelfOperation(_ context.Context, r api.OperationStartRequest, k string) (api.OperationAcceptedResponse, error) {
	return c.submit(r, k)
}

func developerAccepted() api.OperationAcceptedResponse {
	path := "/v1/platform-tenant-self/customer-operations/" + developerOperationID
	return api.OperationAcceptedResponse{ID: developerOperationID, StatusURL: path, EventsURL: path + "/events"}
}

func developerSubmissionFixture(t *testing.T) (customerOperationDeveloperCommand, customerOperationSubmissionTestClient, time.Time) {
	t.Helper()
	dir := t.TempDir()
	input := filepath.Join(dir, "input.json")
	if err := os.WriteFile(input, []byte(`{"count":1}`), 0600); err != nil {
		t.Fatal(err)
	}
	c := customerOperationDeveloperCommand{verb: "start", self: true, definition: developerDefinitionID, input: input, key: "stable-export", receipt: filepath.Join(dir, "request.json")}
	client := customerOperationSubmissionTestClient{origin: "https://api.example.test", identity: api.OperationTenantIdentity{AccountID: "33333333-3333-3333-3333-333333333333", PlatformTenantID: "44444444-4444-4444-4444-444444444444"}}
	return c, client, time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
}

func TestCustomerOperationSubmissionReceiptSurvivesLostResponse(t *testing.T) {
	c, client, now := developerSubmissionFixture(t)
	calls := 0
	client.submit = func(req api.OperationStartRequest, key string) (api.OperationAcceptedResponse, error) {
		calls++
		body, err := readCustomerOperationPrivateReceipt(c.receipt)
		if err != nil {
			t.Fatal("receipt was not durable before POST", err)
		}
		var r customerOperationSubmissionReceipt
		if err := decodeCustomerOperationReceipt(body, &r); err != nil {
			t.Fatal(err)
		}
		if key != r.IdempotencyKey || req.DefinitionID != r.DefinitionID || !bytes.Equal(req.Input, r.Input) {
			t.Fatal("request changed from saved receipt")
		}
		if calls == 1 {
			return api.OperationAcceptedResponse{}, io.ErrUnexpectedEOF
		}
		return developerAccepted(), nil
	}
	if _, err := startCustomerOperation(t.Context(), client, c, now); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("lost response: %v", err)
	}
	original, err := os.ReadFile(c.receipt)
	if err != nil {
		t.Fatal(err)
	}
	resume := customerOperationDeveloperCommand{verb: "start", self: true, receipt: c.receipt}
	accepted, err := startCustomerOperation(t.Context(), client, resume, now.Add(time.Minute))
	if err != nil || accepted != developerAccepted() {
		t.Fatalf("resume %+v %v", accepted, err)
	}
	again, err := startCustomerOperation(t.Context(), client, resume, now.Add(time.Hour))
	if err != nil || again != accepted || calls != 2 {
		t.Fatalf("confirmed receipt re-posted: calls=%d, err=%v", calls, err)
	}
	after, err := os.ReadFile(c.receipt)
	if err != nil || !bytes.Equal(original, after) {
		t.Fatal("original request receipt mutated")
	}
	for _, path := range []string{c.receipt, c.receipt + ".accepted.json"} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatal("receipt is not private", err)
		}
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(body, []byte("Bearer")) {
			t.Fatal("credential leaked into receipt")
		}
	}
	partials, err := filepath.Glob(filepath.Join(filepath.Dir(c.receipt), ".gregale-submission-*"))
	if err != nil || len(partials) != 0 {
		t.Fatal("temporary receipt files leaked", partials, err)
	}
}

func TestCustomerOperationSubmissionConflictsAndReplayWindow(t *testing.T) {
	for _, kind := range []string{"input", "key", "definition", "tenant", "account", "api", "expired", "future", "corrupt", "public", "symlink", "ack-conflict"} {
		t.Run(kind, func(t *testing.T) {
			c, client, now := developerSubmissionFixture(t)
			calls := 0
			client.submit = func(api.OperationStartRequest, string) (api.OperationAcceptedResponse, error) {
				calls++
				return api.OperationAcceptedResponse{}, io.ErrUnexpectedEOF
			}
			_, _ = startCustomerOperation(t.Context(), client, c, now)
			switch kind {
			case "input":
				if err := os.WriteFile(c.input, []byte(`{"count":2}`), 0600); err != nil {
					t.Fatal(err)
				}
			case "key":
				c.key = "new-key"
			case "definition":
				c.definition = "55555555-5555-5555-5555-555555555555"
			case "tenant":
				client.identity.PlatformTenantID = "55555555-5555-5555-5555-555555555555"
			case "account":
				client.identity.AccountID = "55555555-5555-5555-5555-555555555555"
			case "api":
				client.origin = "https://other.example.test"
			case "expired":
				now = now.Add(customerOperationSafeReplayWindow())
			case "future":
				now = now.Add(-time.Second)
			case "corrupt":
				if err := os.WriteFile(c.receipt, []byte(`{"version":1,"version":1}`), 0600); err != nil {
					t.Fatal(err)
				}
			case "public":
				if err := os.Chmod(c.receipt, 0644); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				original := c.receipt
				c.receipt += "-link"
				if err := os.Symlink(original, c.receipt); err != nil {
					t.Fatal(err)
				}
			case "ack-conflict":
				body, _ := encodeCustomerOperationReceipt(customerOperationSubmissionAcknowledgement{Version: 1, RequestSHA256: "wrong", Accepted: developerAccepted()})
				if err := os.WriteFile(c.receipt+".accepted.json", body, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := startCustomerOperation(t.Context(), client, c, now); err == nil {
				t.Fatal("conflicting or unsafe replay accepted")
			}
			if calls != 1 {
				t.Fatalf("unsafe receipt made another POST: %d", calls)
			}
		})
	}
}

func TestCustomerOperationSubmissionConcurrentAndEquivalentInput(t *testing.T) {
	c, client, now := developerSubmissionFixture(t)
	var calls atomic.Int32
	client.submit = func(req api.OperationStartRequest, key string) (api.OperationAcceptedResponse, error) {
		calls.Add(1)
		if req.DefinitionID != developerDefinitionID || key != "stable-export" || string(req.Input) != `{"count":1}` {
			t.Error("concurrent request changed")
		}
		return developerAccepted(), nil
	}
	var group sync.WaitGroup
	for i := 0; i < 6; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			a, err := startCustomerOperation(t.Context(), client, c, now)
			if err != nil || a != developerAccepted() {
				t.Errorf("concurrent start %+v %v", a, err)
			}
		}()
	}
	group.Wait()
	if calls.Load() < 1 {
		t.Fatal("no submission")
	}
	if err := os.WriteFile(c.input, []byte(`{ "count":1.0 }`), 0600); err != nil {
		t.Fatal(err)
	}
	previous := calls.Load()
	if _, err := startCustomerOperation(t.Context(), client, c, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != previous {
		t.Fatal("equivalent input reposted a confirmed receipt")
	}
}

func TestCustomerOperationSubmissionRetainsMaximumDepthInput(t *testing.T) {
	c, client, now := developerSubmissionFixture(t)
	input := strings.Repeat("[", api.OperationJSONMaxDepth) + "0" + strings.Repeat("]", api.OperationJSONMaxDepth)
	if _, err := operations.CanonicalJSON([]byte(input)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(c.input, []byte(input), 0600); err != nil {
		t.Fatal(err)
	}
	client.submit = func(api.OperationStartRequest, string) (api.OperationAcceptedResponse, error) {
		return developerAccepted(), nil
	}
	if _, err := startCustomerOperation(t.Context(), client, c, now); err != nil {
		t.Fatal(err)
	}
}

func TestCustomerOperationSubmissionNoPOSTWithoutPrivateReceipt(t *testing.T) {
	for _, kind := range []string{"missing-parent", "duplicate-input", "invalid-definition", "invalid-key", "bad-ack", "orphan-ack"} {
		t.Run(kind, func(t *testing.T) {
			c, client, now := developerSubmissionFixture(t)
			calls := 0
			client.submit = func(api.OperationStartRequest, string) (api.OperationAcceptedResponse, error) {
				calls++
				a := developerAccepted()
				a.ID = "bad-id"
				return a, nil
			}
			switch kind {
			case "missing-parent":
				c.receipt = filepath.Join(t.TempDir(), "absent", "request.json")
			case "duplicate-input":
				if err := os.WriteFile(c.input, []byte(`{"count":1,"count":2}`), 0600); err != nil {
					t.Fatal(err)
				}
			case "invalid-definition":
				c.definition = "invalid"
			case "invalid-key":
				c.key = "key\r\nInjected: value"
			case "orphan-ack":
				if err := os.WriteFile(c.receipt+".accepted.json", []byte(`{}`), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := startCustomerOperation(t.Context(), client, c, now); err == nil {
				t.Fatal("invalid submission accepted")
			}
			want := 0
			if kind == "bad-ack" {
				want = 1
			}
			if calls != want {
				t.Fatalf("POST count %d want %d", calls, want)
			}
		})
	}
}

func TestCustomerOperationLocalValidationUsesManifestCompiler(t *testing.T) {
	for _, format := range []string{"yaml", "toml"} {
		t.Run(format, func(t *testing.T) {
			dir := t.TempDir()
			schema := `{"type":"object","required":["count"],"properties":{"count":{"type":"integer","minimum":1}},"additionalProperties":false}`
			for name, body := range map[string]string{"input.json": schema, "output.json": `{"type":"object"}`, "sample.json": `{"count":2}`} {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0600); err != nil {
					t.Fatal(err)
				}
			}
			name := "gregale.yaml"
			body := "operations:\n  - name: export\n    method: POST\n    path: /exports\n    owner: platform_tenant\n    input_schema: input.json\n    output_schema: output.json\n    progress_stages: [generating]\n"
			if format == "toml" {
				name = "gregale.toml"
				body = "[[operations]]\nname='export'\nmethod='POST'\npath='/exports'\nowner='platform_tenant'\ninput_schema='input.json'\noutput_schema='output.json'\nprogress_stages=['generating']\n"
			}
			if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
			c := customerOperationDeveloperCommand{verb: "validate", app: "exports", dir: dir, plan: "pro", name: "export", input: filepath.Join(dir, "sample.json")}
			r, err := validateCustomerOperationSource(c)
			if err != nil || len(r.Definitions) != 1 || !r.Definitions[0].InputValidated || r.Definitions[0].Revision == "" {
				t.Fatalf("validation %+v %v", r, err)
			}
			contract, err := operations.Compile(r.Definitions[0].Spec, api.MustLimitsFor(api.PlanPro).Operations)
			if err != nil || contract.Revision != r.Definitions[0].Revision {
				t.Fatal("validator revision differs from server")
			}
			if err := os.WriteFile(c.input, []byte(`{"count":0}`), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := validateCustomerOperationSource(c); err == nil {
				t.Fatal("bad sample accepted")
			}
			c.input = ""
			c.plan = "free"
			if _, err := validateCustomerOperationSource(c); err == nil {
				t.Fatal("free plan accepted")
			}
			c.plan = "invalid"
			if _, err := validateCustomerOperationSource(c); err == nil {
				t.Fatal("unknown plan accepted")
			}
			c.plan = "pro"
			c.name = "missing"
			if _, err := validateCustomerOperationSource(c); err == nil {
				t.Fatal("unknown definition accepted")
			}
			c.name = "export"
			if err := os.Remove(filepath.Join(dir, "input.json")); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(filepath.Join(dir, "output.json"), filepath.Join(dir, "input.json")); err != nil {
				t.Fatal(err)
			}
			if _, err := validateCustomerOperationSource(c); err == nil {
				t.Fatal("linked schema accepted")
			}
		})
	}
}

func TestCustomerOperationSourceReaderRejectsParentSymlinksAndEscapes(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.json"), []byte(`true`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "schemas")); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	for _, path := range []string{"schemas/secret.json", "../secret.json", filepath.Join(outside, "secret.json")} {
		if _, err := readCustomerOperationSourceFile(root, path, 1024); err == nil {
			t.Fatal("unsafe source read", path)
		}
	}
}

func TestCustomerOperationDeveloperParserAndTenantRoutes(t *testing.T) {
	for _, args := range [][]string{{"start"}, {"start", "--self", "--receipt-file", "r", "--definition", developerDefinitionID}, {"validate", "--app", "exports"}, {"validate", "--app", "exports", "--plan", "pro", "--input-file", "input"}, {"definitions", "get", "export", "--app", "exports"}, {"definitions", "wrong"}} {
		if _, err := parseCustomerOperationDeveloper(args); err == nil {
			t.Fatal("ambiguous developer command", args)
		}
	}
	for _, args := range [][]string{{"get", "op", "--self", "--app", "exports"}, {"recover", "op", "--self"}, {"executions", "op", "--self"}} {
		if _, err := parseCustomerOperationCommand(args); err == nil {
			t.Fatal("self mode gained account authority", args)
		}
	}
	for _, args := range [][]string{{"start", "--self", "--receipt-file", "r"}, {"definitions", "list", "--app", "exports", "--deployment", developerDefinitionID}, {"validate", "--app", "exports", "--plan", "pro"}} {
		if _, err := parseCustomerOperationDeveloper(args); err != nil {
			t.Fatal(args, err)
		}
	}
	data := []byte("export bytes")
	digest := fmt.Sprintf("sha256:%x", sha256.Sum256(data))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer rotated-tenant-token" || !strings.HasPrefix(r.URL.Path, "/v1/platform-tenant-self/customer-operations/") {
			t.Errorf("wrong authority: %s", r.URL.Path)
		}
		if strings.HasSuffix(r.URL.Path, "/artifacts/artifact") {
			w.Header().Set("X-Gregale-Artifact-SHA256", digest)
			w.Header().Set("Content-Length", fmt.Sprint(len(data)))
			_, _ = w.Write(data)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/events") {
			_ = json.NewEncoder(w).Encode(api.OperationEventsResponse{Events: []api.OperationEvent{}, LatestSequence: 1})
			return
		}
		_ = json.NewEncoder(w).Encode(api.OperationResponse{ID: developerOperationID, State: api.OperationSucceeded, CompletionDelivery: api.OperationDeliveryResponse{State: "dead"}, Artifacts: []api.OperationResultArtifact{{ID: "artifact", SizeBytes: int64(len(data)), SHA256: digest}}})
	}))
	defer server.Close()
	client := tenantCustomerOperationsClient{client: api.NewClient(server.URL, "rotated-tenant-token").SetCompletionCache(nil)}
	var c customerOperationCommand
	var out bytes.Buffer
	for _, args := range [][]string{
		{"watch", "--self", developerOperationID},
		{"watch", developerOperationID, "--self"},
		{"watch", "--self=true", developerOperationID},
		{"watch", "--timeout", "1s", "--self", developerOperationID},
	} {
		var err error
		c, err = parseCustomerOperationCommand(args)
		if err != nil || !c.self || c.id != developerOperationID || c.app != "" {
			t.Fatalf("tenant selector consumed or changed the operation identity: %v %+v %v", args, c, err)
		}
		if code, err := runCustomerOperationCommand(t.Context(), client, c, &out, true); err != nil || code != 0 {
			t.Fatal("tenant watch", code, err)
		}
	}
	c.verb = "events"
	if _, err := runCustomerOperationCommand(t.Context(), client, c, &out, true); err != nil {
		t.Fatal(err)
	}
	c.verb = "download"
	c.output = filepath.Join(t.TempDir(), "export.csv")
	if _, err := runCustomerOperationCommand(t.Context(), client, c, &out, true); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(c.output)
	if err != nil || !bytes.Equal(got, data) {
		t.Fatal("tenant download mismatch", err)
	}
}

func TestCustomerOperationLocalValidationUsesManifestCompilerIntegratedBusinessWorkflows(t *testing.T) {
	for _, format := range []string{"yaml", "toml"} {
		t.Run(format, func(t *testing.T) {
			dir := t.TempDir()
			schema := `{"type":"object","required":["count"],"properties":{"count":{"type":"integer","minimum":1}},"additionalProperties":false}`
			for name, body := range map[string]string{"input.json": schema, "output.json": `{"type":"object"}`, "sample.json": `{"count":2}`} {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0600); err != nil {
					t.Fatal(err)
				}
			}
			name := "gregale.yaml"
			body := "operations:\n  - name: export\n    method: POST\n    path: /exports\n    owner: platform_tenant\n    input_schema: input.json\n    output_schema: output.json\n    progress_stages: [generating]\n    http_transaction_version: 1\n"
			if format == "toml" {
				name = "gregale.toml"
				body = "[[operations]]\nname='export'\nmethod='POST'\npath='/exports'\nowner='platform_tenant'\ninput_schema='input.json'\noutput_schema='output.json'\nprogress_stages=['generating']\nhttp_transaction_version=1\n"
			}
			if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
			c := customerOperationDeveloperCommand{verb: "validate", app: "exports", dir: dir, plan: "pro", name: "export", input: filepath.Join(dir, "sample.json")}
			r, err := validateCustomerOperationSource(c)
			if err != nil || len(r.Definitions) != 1 || !r.Definitions[0].InputValidated || r.Definitions[0].Revision == "" || r.Definitions[0].Spec.HTTPTransactionVersion != 1 {
				t.Fatalf("validation %+v %v", r, err)
			}
			for _, asJSON := range []bool{false, true} {
				var output bytes.Buffer
				if err := renderCustomerOperationValidation(&output, r, asJSON); err != nil {
					t.Fatal(err)
				}
				want := "http_transaction_version=1"
				if asJSON {
					want = `"http_transaction_version":1`
				}
				if !strings.Contains(output.String(), want) {
					t.Fatalf("validation hides negotiation: %s", output.String())
				}
			}
			contract, err := operations.Compile(r.Definitions[0].Spec, api.MustLimitsFor(api.PlanPro).Operations)
			if err != nil || contract.Revision != r.Definitions[0].Revision {
				t.Fatal("validator revision differs from server")
			}
			if err := os.WriteFile(c.input, []byte(`{"count":0}`), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := validateCustomerOperationSource(c); err == nil {
				t.Fatal("bad sample accepted")
			}
			c.input = ""
			c.plan = "free"
			if _, err := validateCustomerOperationSource(c); err == nil {
				t.Fatal("free plan accepted")
			}
			c.plan = "invalid"
			if _, err := validateCustomerOperationSource(c); err == nil {
				t.Fatal("unknown plan accepted")
			}
			c.plan = "pro"
			c.name = "missing"
			if _, err := validateCustomerOperationSource(c); err == nil {
				t.Fatal("unknown definition accepted")
			}
			c.name = "export"
			if err := os.Remove(filepath.Join(dir, "input.json")); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(filepath.Join(dir, "output.json"), filepath.Join(dir, "input.json")); err != nil {
				t.Fatal(err)
			}
			if _, err := validateCustomerOperationSource(c); err == nil {
				t.Fatal("linked schema accepted")
			}
		})
	}
}
