package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const storedWorkflowDefinitionYAML = `name: paid-invoice
trigger:
  type: manual
steps:
  - name: record
    path: /record-payment
`

func writeStoredWorkflowFixture(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "stored_workflow.yaml")
	if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func captureStoredWorkflowStdout(t *testing.T) *bytes.Buffer {
	t.Helper()
	var output bytes.Buffer
	previous := osStdout
	osStdout = &output
	t.Cleanup(func() { osStdout = previous })
	return &output
}

func TestCmdStoredWorkflowsDispatchUsage(t *testing.T) {
	// The verb list is too long for one usage line; the error names the
	// shape and the help command that lists every verb.
	code, output := runWithStderr(t, func() int { return cmdStoredWorkflows(nil) })
	if code != 1 || !strings.Contains(output, "usage: gregale workflow stored <command>") || !strings.Contains(output, "All options: gregale workflow stored --help") {
		t.Fatalf("exit=%d stderr=%q", code, output)
	}
	code, output = runWithStderr(t, func() int { return cmdStoredWorkflows([]string{"unknown"}) })
	if code != 1 || !strings.Contains(output, "unknown stored workflows subcommand") {
		t.Fatalf("exit=%d stderr=%q", code, output)
	}
}

func writeStoredWorkflowJSONFixture(t *testing.T, name, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestCmdStoredWorkflowsSimulateUsesSampleInputAndMocks(t *testing.T) {
	resetJSONOut(t)
	f := authedFakeAPI(t, `{"definition_valid":true,"complete":true,"issues":[],"warnings":[],"step_order":["lookup"],"trace":[{"step_name":"lookup","kind":"run","state":"mocked","input":{"id":"inv-1"},"output":{"paid":true},"run":"/lookup_invoice"},{"step_name":"send","kind":"outbound","state":"would_execute","integration_id":"00000000-0000-0000-0000-000000000001","method":"GET","path":"/contacts/contact%2042","raw_query":"email=a%2Bb%40example.com"}]}`, http.StatusOK)
	output := captureStoredWorkflowStdout(t)
	definition := writeStoredWorkflowFixture(t, storedWorkflowDefinitionYAML)
	input := writeStoredWorkflowJSONFixture(t, "input.json", `{"invoice_id":"inv-1"}`)
	mocks := writeStoredWorkflowJSONFixture(t, "mocks.json", `{"lookup":{"paid":true}}`)
	itemMocks := writeStoredWorkflowJSONFixture(t, "item-mocks.json", `{"batch":[{"id":"one"},{"id":"two"}]}`)
	attemptMocks := writeStoredWorkflowJSONFixture(t, "attempt-mocks.json", `{"record":[{"outcome":"failure","http_status":503},{"outcome":"success","output":{"paid":true}}]}`)
	args := []string{"--app", "billing", "--file", definition, "--input-file", input, "--mock-outputs-file", mocks, "--mock-item-outputs-file", itemMocks, "--mock-attempts-file", attemptMocks}
	if code := cmdStoredWorkflowsSimulate(args); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if f.sawMethod != http.MethodPost || f.sawPath != "/v1/apps/billing/workflows/stored:simulate" {
		t.Fatalf("request = %s %s", f.sawMethod, f.sawPath)
	}
	var request api.SimulateStoredWorkflowRequest
	if err := json.Unmarshal(f.sawBody, &request); err != nil {
		t.Fatalf("request body = %s, err=%v", f.sawBody, err)
	}
	if string(request.Input) != `{"invoice_id":"inv-1"}` || string(request.MockOutputs["lookup"]) != `{"paid":true}` || len(request.MockItemOutputs["batch"]) != 2 || len(request.MockAttempts["record"]) != 2 || request.MockAttempts["record"][0].HTTPStatus == nil || *request.MockAttempts["record"][0].HTTPStatus != 503 {
		t.Fatalf("simulation request did not preserve samples: %+v", request)
	}
	for _, want := range []string{"mocked", `{"id":"inv-1"}`, `{"paid":true}`, "GET /contacts/contact%2042?email=a%2Bb%40example.com"} {
		if !strings.Contains(output.String(), want) {
			t.Errorf("human trace %q missing %q", output.String(), want)
		}
	}
}

func TestCmdStoredWorkflowsSimulateJSONReturnsTrace(t *testing.T) {
	resetJSONOut(t)
	jsonOutput = true
	authedFakeAPI(t, `{"definition_valid":true,"complete":false,"issues":[],"warnings":[],"step_order":["lookup"],"trace":[{"step_name":"lookup","kind":"run","state":"blocked","reason":"dependency_output_missing"}]}`, http.StatusOK)
	output := captureStoredWorkflowStdout(t)
	definition := writeStoredWorkflowFixture(t, storedWorkflowDefinitionYAML)
	if code := cmdStoredWorkflowsSimulate([]string{"--app", "billing", "--file", definition}); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	var response api.SimulateStoredWorkflowResponse
	if err := json.Unmarshal(output.Bytes(), &response); err != nil || response.Complete || len(response.Trace) != 1 || response.Trace[0].State != "blocked" {
		t.Fatalf("response = %s, err=%v", output.String(), err)
	}
}

func TestCmdStoredWorkflowsSimulateCanRequireCompleteTrace(t *testing.T) {
	resetJSONOut(t)
	authedFakeAPI(t, `{"definition_valid":true,"complete":false,"issues":[],"warnings":[],"step_order":["lookup"],"trace":[{"step_name":"lookup","kind":"run","state":"blocked","reason":"dependency_output_missing"}]}`, http.StatusOK)
	captureStoredWorkflowStdout(t)
	definition := writeStoredWorkflowFixture(t, storedWorkflowDefinitionYAML)
	if code := cmdStoredWorkflowsSimulate([]string{"--app", "billing", "--file", definition, "--require-complete"}); code != 1 {
		t.Fatalf("exit = %d, want incomplete-trace failure", code)
	}
}

func TestCmdStoredWorkflowsSimulateRejectsInvalidSampleBeforeRequest(t *testing.T) {
	resetJSONOut(t)
	f := authedFakeAPI(t, `{}`, http.StatusOK)
	definition := writeStoredWorkflowFixture(t, storedWorkflowDefinitionYAML)
	input := writeStoredWorkflowJSONFixture(t, "input.json", "{invalid")
	code, output := runWithStderr(t, func() int {
		return cmdStoredWorkflowsSimulate([]string{"--app", "billing", "--file", definition, "--input-file", input})
	})
	if code != 1 || !strings.Contains(output, "Invalid stored workflow simulation input") || f.sawMethod != "" {
		t.Fatalf("exit=%d stderr=%q request=%s %s", code, output, f.sawMethod, f.sawPath)
	}
}

const storedWorkflowResponseWithPrivateDefinition = `{"name":"paid-invoice","version":4,"source":"api","draft":{"name":"paid-invoice","steps":[{"name":"draft-step","path":"/draft","input":{"marker":"draft-private-value"}}]},"published":{"name":"paid-invoice","steps":[{"name":"published-step","path":"/published","input":{"marker":"published-private-value"}}]},"published_version":3,"enabled":true}`

func TestCmdStoredWorkflowsListReturnsSafeMetadata(t *testing.T) {
	resetJSONOut(t)
	jsonOutput = true
	f := authedFakeAPI(t, `{"app_slug":"billing","runtime_enabled":true,"max_definitions":20,"stored_workflows":[`+storedWorkflowResponseWithPrivateDefinition+`]}`, http.StatusOK)
	output := captureStoredWorkflowStdout(t)
	if code := cmdStoredWorkflowsList([]string{"--app", "billing"}); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if f.sawMethod != http.MethodGet || f.sawPath != "/v1/apps/billing/workflows/stored" {
		t.Fatalf("request = %s %s", f.sawMethod, f.sawPath)
	}
	if strings.Contains(output.String(), "private-value") {
		t.Fatalf("list echoed definition contents: %s", output.String())
	}
	var summary storedWorkflowListSummary
	if err := json.Unmarshal(output.Bytes(), &summary); err != nil || summary.AppSlug != "billing" || len(summary.StoredWorkflows) != 1 || summary.StoredWorkflows[0].Version != 4 {
		t.Fatalf("summary = %s, err=%v", output.String(), err)
	}
}

func TestCmdStoredWorkflowsGetReturnsSafeMetadata(t *testing.T) {
	resetJSONOut(t)
	jsonOutput = true
	f := authedFakeAPI(t, storedWorkflowResponseWithPrivateDefinition, http.StatusOK)
	output := captureStoredWorkflowStdout(t)
	if code := cmdStoredWorkflowsGet([]string{"--app", "billing", "--name", "paid-invoice"}); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if f.sawMethod != http.MethodGet || f.sawPath != "/v1/apps/billing/workflows/stored/paid-invoice" {
		t.Fatalf("request = %s %s", f.sawMethod, f.sawPath)
	}
	if strings.Contains(output.String(), "private-value") {
		t.Fatalf("get echoed definition contents without explicit export: %s", output.String())
	}
	var summary storedWorkflowGetSummary
	if err := json.Unmarshal(output.Bytes(), &summary); err != nil || summary.Name != "paid-invoice" || summary.Version != 4 || summary.PublishedVersion != 3 {
		t.Fatalf("summary = %s, err=%v", output.String(), err)
	}
}

func TestCmdStoredWorkflowsGetExportsDraftAsPrivateJSONFile(t *testing.T) {
	resetJSONOut(t)
	authedFakeAPI(t, storedWorkflowResponseWithPrivateDefinition, http.StatusOK)
	output := captureStoredWorkflowStdout(t)
	path := filepath.Join(t.TempDir(), "draft.json")
	if code := cmdStoredWorkflowsGet([]string{"--app", "billing", "--name", "paid-invoice", "--definition-out", path}); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Errorf("export mode = %04o, want 0600", info.Mode().Perm())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var definition struct {
		Steps []struct {
			Name string `json:"name"`
		} `json:"steps"`
	}
	if err := json.Unmarshal(data, &definition); err != nil || len(definition.Steps) != 1 || definition.Steps[0].Name != "draft-step" {
		t.Fatalf("export = %s, err=%v", data, err)
	}
	if strings.Contains(output.String(), "draft-private-value") {
		t.Fatalf("get printed exported definition: %s", output.String())
	}
}

func TestCmdStoredWorkflowsGetExportsPublishedDefinitionWhenSelected(t *testing.T) {
	resetJSONOut(t)
	authedFakeAPI(t, storedWorkflowResponseWithPrivateDefinition, http.StatusOK)
	path := filepath.Join(t.TempDir(), "published.json")
	if code := cmdStoredWorkflowsGet([]string{"--app", "billing", "--name", "paid-invoice", "--definition-out", path, "--published"}); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var definition struct {
		Steps []struct {
			Name string `json:"name"`
		} `json:"steps"`
	}
	if err := json.Unmarshal(data, &definition); err != nil || len(definition.Steps) != 1 || definition.Steps[0].Name != "published-step" {
		t.Fatalf("export = %s, err=%v", data, err)
	}
}

func TestCmdStoredWorkflowsGetRefusesToOverwriteDefinitionFile(t *testing.T) {
	resetJSONOut(t)
	authedFakeAPI(t, storedWorkflowResponseWithPrivateDefinition, http.StatusOK)
	path := filepath.Join(t.TempDir(), "keep.json")
	if err := os.WriteFile(path, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	code, output := runWithStderr(t, func() int {
		return cmdStoredWorkflowsGet([]string{"--app", "billing", "--name", "paid-invoice", "--definition-out", path})
	})
	contents, err := os.ReadFile(path)
	if err != nil || string(contents) != "keep" {
		t.Fatalf("existing file contents=%q err=%v", contents, err)
	}
	if code != 1 || !strings.Contains(output, "Could not export stored workflow definition") {
		t.Fatalf("exit=%d stderr=%q", code, output)
	}
}

func TestReadStoredWorkflowDefinitionRejectsUnknownFieldsAndExtraDocuments(t *testing.T) {
	for _, contents := range []string{
		"name: sample\nsteps: []\nunknown: true\n",
		storedWorkflowDefinitionYAML + "---\nname: second\nsteps: []\n",
	} {
		code, output := runWithStderr(t, func() int {
			_, ok := readStoredWorkflowDefinition(writeStoredWorkflowFixture(t, contents))
			if ok {
				return 0
			}
			return 1
		})
		if code != 1 || !strings.Contains(output, "Invalid stored workflow definition") {
			t.Fatalf("exit=%d stderr=%q", code, output)
		}
	}
}

func TestCmdStoredWorkflowsValidatePostsDefinition(t *testing.T) {
	resetJSONOut(t)
	f := authedFakeAPI(t, `{"valid":true,"issues":[],"step_order":["record"]}`, http.StatusOK)
	captureStoredWorkflowStdout(t)
	path := writeStoredWorkflowFixture(t, storedWorkflowDefinitionYAML)
	if code := cmdStoredWorkflowsValidate([]string{"--app", "billing", "--file", path}); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if f.sawMethod != http.MethodPost || f.sawPath != "/v1/apps/billing/workflows/stored:validate" {
		t.Fatalf("request = %s %s", f.sawMethod, f.sawPath)
	}
	var body struct {
		Definition struct {
			Name string `json:"name"`
		} `json:"definition"`
	}
	if err := json.Unmarshal(f.sawBody, &body); err != nil || body.Definition.Name != "paid-invoice" {
		t.Fatalf("request body = %s, err=%v", f.sawBody, err)
	}
}

func TestCmdStoredWorkflowsValidateReturnsFailureForInvalidDefinition(t *testing.T) {
	resetJSONOut(t)
	authedFakeAPI(t, `{"valid":false,"issues":["step path is invalid"],"step_order":[]}`, http.StatusOK)
	var output bytes.Buffer
	previous := osStdout
	osStdout = &output
	t.Cleanup(func() { osStdout = previous })
	path := writeStoredWorkflowFixture(t, storedWorkflowDefinitionYAML)
	if code := cmdStoredWorkflowsValidate([]string{"--app", "billing", "--file", path}); code != 1 {
		t.Fatalf("exit = %d, want invalid-definition exit 1", code)
	}
	if !strings.Contains(output.String(), "step path is invalid") {
		t.Fatalf("stdout = %q", output.String())
	}
}

func TestCmdStoredWorkflowsApplyUsesExpectedVersion(t *testing.T) {
	resetJSONOut(t)
	f := authedFakeAPI(t, `{"name":"paid-invoice","version":1,"source":"draft","draft":{"name":"paid-invoice","steps":[{"name":"record","path":"/record-payment"}]}}`, http.StatusOK)
	captureStoredWorkflowStdout(t)
	path := writeStoredWorkflowFixture(t, storedWorkflowDefinitionYAML)
	if code := cmdStoredWorkflowsApply([]string{"--app", "billing", "--file", path, "--expected-version", "0"}); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if f.sawMethod != http.MethodPut || f.sawPath != "/v1/apps/billing/workflows/stored/paid-invoice" {
		t.Fatalf("request = %s %s", f.sawMethod, f.sawPath)
	}
	var body struct {
		ExpectedVersion int64 `json:"expected_version"`
		Definition      struct {
			Name string `json:"name"`
		} `json:"definition"`
	}
	if err := json.Unmarshal(f.sawBody, &body); err != nil || body.ExpectedVersion != 0 || body.Definition.Name != "paid-invoice" {
		t.Fatalf("request body = %s, err=%v", f.sawBody, err)
	}
}

func TestCmdStoredWorkflowsApplyJSONOmitsDefinitionContents(t *testing.T) {
	resetJSONOut(t)
	jsonOutput = true
	authedFakeAPI(t, `{"name":"paid-invoice","version":1,"source":"draft","draft":{"name":"paid-invoice","steps":[{"name":"record","path":"/record-payment","input":{"marker":"private-definition-value"}}]}}`, http.StatusOK)
	output := captureStoredWorkflowStdout(t)
	path := writeStoredWorkflowFixture(t, storedWorkflowDefinitionYAML)
	if code := cmdStoredWorkflowsApply([]string{"--app", "billing", "--file", path, "--expected-version", "0"}); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if strings.Contains(output.String(), "private-definition-value") {
		t.Fatalf("machine output echoed the stored workflow definition: %s", output.String())
	}
	var summary storedWorkflowMutationSummary
	if err := json.Unmarshal(output.Bytes(), &summary); err != nil || summary.Name != "paid-invoice" || summary.Version != 1 {
		t.Fatalf("summary = %s, err=%v", output.String(), err)
	}
}

func TestCmdStoredWorkflowsApplyRequiresExpectedVersion(t *testing.T) {
	resetJSONOut(t)
	f := authedFakeAPI(t, `{}`, http.StatusOK)
	path := writeStoredWorkflowFixture(t, storedWorkflowDefinitionYAML)
	code, output := runWithStderr(t, func() int {
		return cmdStoredWorkflowsApply([]string{"--app", "billing", "--file", path})
	})
	if code != 1 || !strings.Contains(output, "--expected-version <n>") || f.sawMethod != "" {
		t.Fatalf("exit=%d stderr=%q request=%s %s", code, output, f.sawMethod, f.sawPath)
	}
}

func TestCmdStoredWorkflowsPublishCanExplicitlyTakeOverManifest(t *testing.T) {
	resetJSONOut(t)
	f := authedFakeAPI(t, `{"name":"paid-invoice","version":2,"source":"api","draft":{"name":"paid-invoice","steps":[]},"published_version":2,"enabled":true}`, http.StatusOK)
	captureStoredWorkflowStdout(t)
	if code := cmdStoredWorkflowsPublish([]string{"--app", "billing", "--name", "paid-invoice", "--expected-version", "1", "--take-over-manifest"}); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if f.sawMethod != http.MethodPost || f.sawPath != "/v1/apps/billing/workflows/stored/paid-invoice/publish" {
		t.Fatalf("request = %s %s", f.sawMethod, f.sawPath)
	}
	var body struct {
		ExpectedVersion  int64 `json:"expected_version"`
		TakeOverManifest bool  `json:"take_over_manifest"`
	}
	if err := json.Unmarshal(f.sawBody, &body); err != nil || body.ExpectedVersion != 1 || !body.TakeOverManifest {
		t.Fatalf("request body = %s, err=%v", f.sawBody, err)
	}
}

func TestCmdStoredWorkflowsRevisionsListReturnsSafeMetadataAndPages(t *testing.T) {
	resetJSONOut(t)
	jsonOutput = true
	f := authedFakeAPI(t, `{"total":51,"limit":25,"offset":50,"revisions":[{"version":42,"definition":{"name":"paid-invoice","steps":[{"name":"private-step","path":"/private","input":{"marker":"revision-private-value"}}]},"definition_hash":"abc123","recorded_at":"2026-10-01T12:00:00Z","legacy_snapshot":false,"published_by_account_id":"account-private-id","published_by_api_key_id":"key-private-id"}]}`, http.StatusOK)
	output := captureStoredWorkflowStdout(t)
	if code := cmdStoredWorkflowsRevisionsList([]string{"--app", "billing", "--name", "paid-invoice", "--limit", "25", "--offset", "50"}); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if f.sawMethod != http.MethodGet || f.sawPath != "/v1/apps/billing/workflows/stored/paid-invoice/revisions" || f.sawQuery != "limit=25&offset=50" {
		t.Fatalf("request = %s %s?%s", f.sawMethod, f.sawPath, f.sawQuery)
	}
	for _, secret := range []string{"revision-private-value", "account-private-id", "key-private-id"} {
		if strings.Contains(output.String(), secret) {
			t.Errorf("revision list exposed %q: %s", secret, output.String())
		}
	}
	var summary storedWorkflowRevisionListSummary
	if err := json.Unmarshal(output.Bytes(), &summary); err != nil || summary.Total != 51 || summary.Offset != 50 || len(summary.Revisions) != 1 || summary.Revisions[0].Version != 42 {
		t.Fatalf("summary = %s, err=%v", output.String(), err)
	}
}

func TestCmdStoredWorkflowsRevisionsShowExportsDefinitionPrivately(t *testing.T) {
	resetJSONOut(t)
	jsonOutput = true
	f := authedFakeAPI(t, `{"version":42,"definition":{"name":"paid-invoice","steps":[{"name":"revision-step","path":"/revision","input":{"marker":"revision-private-value"}}]},"definition_hash":"abc123","recorded_at":"2026-10-01T12:00:00Z","legacy_snapshot":true,"published_by_account_id":"account-private-id","published_by_api_key_id":"key-private-id"}`, http.StatusOK)
	output := captureStoredWorkflowStdout(t)
	path := filepath.Join(t.TempDir(), "revision.json")
	if code := cmdStoredWorkflowsRevisionsShow([]string{"--app", "billing", "--name", "paid-invoice", "--revision", "42", "--definition-out", path}); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if f.sawMethod != http.MethodGet || f.sawPath != "/v1/apps/billing/workflows/stored/paid-invoice/revisions/42" {
		t.Fatalf("request = %s %s", f.sawMethod, f.sawPath)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("export mode info=%v err=%v", info, err)
	}
	data, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(data), "revision-step") || !strings.Contains(string(data), "revision-private-value") {
		t.Fatalf("export = %s, err=%v", data, err)
	}
	for _, secret := range []string{"revision-private-value", "account-private-id", "key-private-id"} {
		if strings.Contains(output.String(), secret) {
			t.Errorf("revision show exposed %q in output: %s", secret, output.String())
		}
	}
	var summary storedWorkflowRevisionGetSummary
	if err := json.Unmarshal(output.Bytes(), &summary); err != nil || summary.Version != 42 || !summary.LegacySnapshot || summary.DefinitionExportedTo != path {
		t.Fatalf("summary = %s, err=%v", output.String(), err)
	}
}

func TestCmdStoredWorkflowsRestoreCreatesDraftWithExpectedVersion(t *testing.T) {
	resetJSONOut(t)
	jsonOutput = true
	f := authedFakeAPI(t, `{"name":"paid-invoice","version":48,"source":"api","draft":{"name":"paid-invoice","steps":[{"name":"restored-private-step","path":"/restored"}]},"published_version":47,"enabled":true}`, http.StatusOK)
	output := captureStoredWorkflowStdout(t)
	if code := cmdStoredWorkflowsRestore([]string{"--app", "billing", "--name", "paid-invoice", "--revision", "42", "--expected-version", "47"}); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if f.sawMethod != http.MethodPost || f.sawPath != "/v1/apps/billing/workflows/stored/paid-invoice/revisions/42/restore" {
		t.Fatalf("request = %s %s", f.sawMethod, f.sawPath)
	}
	var request api.RestoreStoredWorkflowRevisionRequest
	if err := json.Unmarshal(f.sawBody, &request); err != nil || request.ExpectedVersion != 47 {
		t.Fatalf("request body = %s, err=%v", f.sawBody, err)
	}
	if f.sawHeader.Get("Idempotency-Key") == "" {
		t.Fatal("restore request omitted idempotency key")
	}
	if strings.Contains(output.String(), "restored-private-step") {
		t.Fatalf("restore output exposed draft definition: %s", output.String())
	}
	var summary storedWorkflowRevisionRestoreSummary
	if err := json.Unmarshal(output.Bytes(), &summary); err != nil || summary.RestoredFromRevision != 42 || summary.Version != 48 || summary.PublishedVersion != 47 {
		t.Fatalf("summary = %s, err=%v", output.String(), err)
	}
}

func TestCmdStoredWorkflowsRestoreRequiresVersionAndRevision(t *testing.T) {
	resetJSONOut(t)
	f := authedFakeAPI(t, `{}`, http.StatusOK)
	for _, args := range [][]string{
		{"--app", "billing", "--name", "paid-invoice", "--revision", "42"},
		{"--app", "billing", "--name", "paid-invoice", "--revision", "0", "--expected-version", "1"},
		{"--app", "billing", "--name", "paid-invoice", "--revision", "42", "--expected-version", "-1"},
	} {
		code, output := runWithStderr(t, func() int { return cmdStoredWorkflowsRestore(args) })
		if code != 1 || !strings.Contains(output, "usage: gregale workflow stored restore") || f.sawMethod != "" {
			t.Fatalf("args=%v exit=%d stderr=%q request=%s %s", args, code, output, f.sawMethod, f.sawPath)
		}
	}
}

func TestCmdStoredWorkflowsDeleteRequiresConfirmationAndVersion(t *testing.T) {
	resetJSONOut(t)
	f := authedFakeAPI(t, "", http.StatusNoContent)
	for _, args := range [][]string{
		{"--app", "billing", "--name", "paid-invoice", "--expected-version", "12"},
		{"--app", "billing", "--name", "paid-invoice", "--expected-version", "0", "--yes"},
		{"--app", "billing", "--name", "paid-invoice", "--expected-version", "-1", "--yes"},
	} {
		code, output := runWithStderr(t, func() int { return cmdStoredWorkflowsDelete(args) })
		if code != 1 || f.sawMethod != "" {
			t.Fatalf("args=%v exit=%d stderr=%q request=%s %s", args, code, output, f.sawMethod, f.sawPath)
		}
	}
}

func TestCmdStoredWorkflowsDeleteSendsVersionAndManifestAcknowledgement(t *testing.T) {
	resetJSONOut(t)
	f := authedFakeAPI(t, "", http.StatusNoContent)
	output := captureStoredWorkflowStdout(t)
	args := []string{"--app", "billing", "--name", "paid-invoice", "--expected-version", "12", "--yes", "--restore-manifest"}
	if code := cmdStoredWorkflowsDelete(args); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if f.sawMethod != http.MethodDelete || f.sawPath != "/v1/apps/billing/workflows/stored/paid-invoice" {
		t.Fatalf("request = %s %s", f.sawMethod, f.sawPath)
	}
	query, err := url.ParseQuery(f.sawQuery)
	if err != nil || query.Get("expected_version") != "12" || query.Get("restore_manifest") != "true" {
		t.Fatalf("query = %q, parsed=%v err=%v", f.sawQuery, query, err)
	}
	if !strings.Contains(output.String(), `Deleted stored workflow "paid-invoice" at version 12.`) || !strings.Contains(output.String(), "YAML definition may own") {
		t.Fatalf("human output = %q", output.String())
	}
}

func TestCmdStoredWorkflowsDeleteJSONReportsRequestedManifestRestore(t *testing.T) {
	resetJSONOut(t)
	jsonOutput = true
	f := authedFakeAPI(t, "", http.StatusNoContent)
	output := captureStoredWorkflowStdout(t)
	args := []string{"--app", "billing", "--name", "paid-invoice", "--expected-version", "12", "--yes"}
	if code := cmdStoredWorkflowsDelete(args); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	query, err := url.ParseQuery(f.sawQuery)
	if err != nil || query.Get("restore_manifest") != "false" {
		t.Fatalf("query = %q, parsed=%v err=%v", f.sawQuery, query, err)
	}
	var summary storedWorkflowDeleteSummary
	if err := json.Unmarshal(output.Bytes(), &summary); err != nil || summary.Name != "paid-invoice" || summary.DeletedVersion != 12 || summary.RestoreManifestRequested {
		t.Fatalf("summary = %s, err=%v", output.String(), err)
	}
}

const storedWorkflowHealthResponseJSON = `{"app_slug":"billing","stored_workflow_name":"paid-invoice","window_start":"2026-10-01T00:00:00Z","window_end":"2026-10-05T00:00:00Z","run_count":4,"completed_run_count":3,"active_run_count":1,"queued_run_count":2,"success_rate":0.6666666667,"status_counts":{"pending":0,"running":0,"awaiting_event":0,"succeeded":2,"failed":1,"dead":1},"p50_duration_ms":18,"p95_duration_ms":92,"last_run":{"id":"run-latest","status":"running","created_at":"2026-10-05T00:00:00Z"},"last_success":{"id":"run-success","status":"succeeded","created_at":"2026-10-04T12:00:00Z","finished_at":"2026-10-04T12:00:01Z"},"last_failure":{"id":"run-failure","status":"failed","created_at":"2026-10-03T12:00:00Z"},"failed_steps":[{"step_name":"charge","failed_run_count":2,"last_failed_at":"2026-10-03T12:01:00Z"}]}`

func TestCmdStoredWorkflowsHealthSendsWindowAndSafeJSON(t *testing.T) {
	resetJSONOut(t)
	jsonOutput = true
	f := authedFakeAPI(t, storedWorkflowHealthResponseJSON, http.StatusOK)
	output := captureStoredWorkflowStdout(t)
	args := []string{"--app", "billing", "--name", "paid-invoice", "--created-after", "2026-10-01T00:00:00Z", "--created-before", "2026-10-05T00:00:00Z"}
	if code := cmdStoredWorkflowsHealth(args); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if f.sawMethod != http.MethodGet || f.sawPath != "/v1/apps/billing/workflows/stored/paid-invoice/health" {
		t.Fatalf("request = %s %s", f.sawMethod, f.sawPath)
	}
	query, err := url.ParseQuery(f.sawQuery)
	if err != nil || query.Get("created_after") != "2026-10-01T00:00:00Z" || query.Get("created_before") != "2026-10-05T00:00:00Z" {
		t.Fatalf("query = %q, parsed=%v err=%v", f.sawQuery, query, err)
	}
	for _, sensitive := range []string{"input", "output", "error", "private-payload"} {
		if strings.Contains(strings.ToLower(output.String()), sensitive) {
			t.Errorf("health output contains %q: %s", sensitive, output.String())
		}
	}
	var summary api.StoredWorkflowHealthResponse
	if err := json.Unmarshal(output.Bytes(), &summary); err != nil || summary.RunCount != 4 || summary.SuccessRate < 0.66 || len(summary.FailedSteps) != 1 || summary.LastFailure.ID != "run-failure" {
		t.Fatalf("summary = %s, err=%v", output.String(), err)
	}
}

func TestCmdStoredWorkflowsHealthRendersHumanSummaryAndDefaultWindow(t *testing.T) {
	resetJSONOut(t)
	f := authedFakeAPI(t, storedWorkflowHealthResponseJSON, http.StatusOK)
	output := captureStoredWorkflowStdout(t)
	if code := cmdStoredWorkflowsHealth([]string{"--app", "billing", "--name", "paid-invoice"}); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if f.sawPath != "/v1/apps/billing/workflows/stored/paid-invoice/health" || f.sawQuery != "" {
		t.Fatalf("request = %s?%s, want default health window", f.sawPath, f.sawQuery)
	}
	for _, want := range []string{"Stored workflow health: billing/paid-invoice", "Runs: 4 (3 completed)", "Active now: 1, queued: 2", "Queue diagnostics: unavailable", "Success rate: 66.7%", "p50 18ms, p95 92ms", "run-failure", "charge", "failed=1"} {
		if !strings.Contains(output.String(), want) {
			t.Errorf("human output missing %q: %s", want, output.String())
		}
	}
}

func TestCmdStoredWorkflowsHealthRendersQueueDiagnostics(t *testing.T) {
	for _, asJSON := range []bool{false, true} {
		t.Run(fmt.Sprintf("json=%v", asJSON), func(t *testing.T) {
			resetJSONOut(t)
			jsonOutput = asJSON
			var fixture api.StoredWorkflowHealthResponse
			if err := json.Unmarshal([]byte(storedWorkflowHealthResponseJSON), &fixture); err != nil {
				t.Fatal(err)
			}
			fixture.Queue = &api.StoredWorkflowQueueHealth{ObservedAt: time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC), WaitingRunCount: 3, DueRunCount: 2,
				StaleRunCount: 1, OldestDueAgeSeconds: 42.5, AppRunningCount: 2, AppDispatchLimit: 2, TenantDispatchLimit: 1, AppAtCapacity: true,
				ReasonCounts: map[string]int64{api.StoredWorkflowQueueReady: 0, api.StoredWorkflowQueueScheduled: 0, api.StoredWorkflowQueueRetryBackoff: 0,
					api.StoredWorkflowQueueParkedWait: 1, api.StoredWorkflowQueueAppCapacity: 2, api.StoredWorkflowQueueTenantCapacity: 0, api.StoredWorkflowQueueWorkflowCapacity: 0}}
			body, err := json.Marshal(fixture)
			if err != nil {
				t.Fatal(err)
			}
			authedFakeAPI(t, string(body), http.StatusOK)
			output := captureStoredWorkflowStdout(t)
			if code := cmdStoredWorkflowsHealth([]string{"--app", "billing", "--name", "paid-invoice"}); code != 0 {
				t.Fatalf("exit=%d", code)
			}
			if asJSON {
				var got api.StoredWorkflowHealthResponse
				if err := json.Unmarshal(output.Bytes(), &got); err != nil || got.Queue == nil || got.Queue.ReasonCounts[api.StoredWorkflowQueueAppCapacity] != 2 || got.Queue.OldestDueAgeSeconds != 42.5 {
					t.Fatalf("queue JSON=%s err=%v", output.String(), err)
				}
				return
			}
			for _, want := range []string{"Queue observed: 2026-10-07T12:00:00Z", "App dispatch capacity: 2/2 (full), tenant limit: 1", "Waiting now: 3, due: 2, stale: 1, oldest due: 42.5s", "Waiting reasons: parked wait=1, app capacity=2"} {
				if !strings.Contains(output.String(), want) {
					t.Errorf("missing %q: %s", want, output.String())
				}
			}
		})
	}
}

func TestCmdStoredWorkflowsHealthRejectsInvalidWindowsBeforeRequest(t *testing.T) {
	resetJSONOut(t)
	f := authedFakeAPI(t, storedWorkflowHealthResponseJSON, http.StatusOK)
	now := time.Now().UTC()
	cases := []struct {
		name string
		args []string
	}{
		{"invalid timestamp", []string{"--created-after", "yesterday"}},
		{"reversed timestamps", []string{"--created-after", now.Add(-time.Hour).Format(time.RFC3339), "--created-before", now.Add(-2 * time.Hour).Format(time.RFC3339)}},
		{"window too long", []string{"--created-after", now.Add(-31 * 24 * time.Hour).Format(time.RFC3339)}},
		{"future end", []string{"--created-before", now.Add(time.Hour).Format(time.RFC3339)}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			args := append([]string{"--app", "billing", "--name", "paid-invoice"}, test.args...)
			code, output := runWithStderr(t, func() int { return cmdStoredWorkflowsHealth(args) })
			if code != 1 || !strings.Contains(output, "Invalid stored workflow health time range") || f.sawMethod != "" {
				t.Fatalf("exit=%d stderr=%q request=%s %s", code, output, f.sawMethod, f.sawPath)
			}
		})
	}
}

func TestCmdStoredWorkflowsPauseAndResumeUseExpectedVersion(t *testing.T) {
	for _, test := range []struct {
		name    string
		command func([]string) int
		enabled bool
	}{
		{"pause", cmdStoredWorkflowsPause, false},
		{"resume", cmdStoredWorkflowsResume, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			resetJSONOut(t)
			jsonOutput = true
			f := authedFakeAPI(t, `{"name":"paid-invoice","version":9,"source":"api","draft":{"name":"paid-invoice","steps":[{"name":"private-step","path":"/private"}]},"published":{"name":"paid-invoice","steps":[{"name":"private-published-step","path":"/published"}]},"published_version":7,"enabled":`+map[bool]string{true: "true", false: "false"}[test.enabled]+`}`, http.StatusOK)
			output := captureStoredWorkflowStdout(t)
			args := []string{"--app", "billing", "--name", "paid-invoice", "--expected-version", "8"}
			if code := test.command(args); code != 0 {
				t.Fatalf("exit = %d", code)
			}
			if f.sawMethod != http.MethodPut || f.sawPath != "/v1/apps/billing/workflows/stored/paid-invoice/enabled" {
				t.Fatalf("request = %s %s", f.sawMethod, f.sawPath)
			}
			var request api.SetStoredWorkflowEnabledRequest
			if err := json.Unmarshal(f.sawBody, &request); err != nil || request.ExpectedVersion != 8 || request.Enabled == nil || *request.Enabled != test.enabled {
				t.Fatalf("request body = %s, err=%v", f.sawBody, err)
			}
			if f.sawHeader.Get("Idempotency-Key") == "" {
				t.Fatal("enabled-state mutation omitted idempotency key")
			}
			if strings.Contains(output.String(), "private-step") || strings.Contains(output.String(), "private-published-step") {
				t.Fatalf("enabled-state output exposed definitions: %s", output.String())
			}
			var summary storedWorkflowMutationSummary
			if err := json.Unmarshal(output.Bytes(), &summary); err != nil || summary.Version != 9 || summary.Enabled != test.enabled {
				t.Fatalf("summary = %s, err=%v", output.String(), err)
			}
		})
	}
}

func TestCmdStoredWorkflowsPauseAndResumeRequirePositiveVersion(t *testing.T) {
	resetJSONOut(t)
	f := authedFakeAPI(t, `{}`, http.StatusOK)
	for _, test := range []struct {
		command string
		args    []string
	}{
		{"pause", []string{"--app", "billing", "--name", "paid-invoice"}},
		{"resume", []string{"--app", "billing", "--name", "paid-invoice", "--expected-version", "0"}},
	} {
		code, output := runWithStderr(t, func() int { return cmdStoredWorkflows(append([]string{test.command}, test.args...)) })
		if code != 1 || !strings.Contains(output, "usage: gregale workflow stored "+test.command) || f.sawMethod != "" {
			t.Fatalf("command=%s exit=%d stderr=%q request=%s %s", test.command, code, output, f.sawMethod, f.sawPath)
		}
	}
}
