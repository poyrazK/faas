package mcpmanagement

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestHostedUploadedTaskReviewThroughHTTP(t *testing.T) {
	for _, protocol := range []string{api.MCPProtocolVersion, api.MCPLegacyProtocolVersion} {
		t.Run(protocol, func(t *testing.T) {
			f := newUploadedTaskFixture(t)
			const token = "fp_test_task_upload_publisher"
			f.token = token
			uploadRoot := filepath.Join(t.TempDir(), "hosted-uploads")
			remote := newRemoteFixtureWithOptions(t, true, HTTPOptions{Hosted: true, AgentKeys: true, AgentKeyToolsetSelection: true, AllowToolPublishing: true, Toolsets: []string{ToolsetBuildTools}, UploadDir: uploadRoot, CLIRunner: func(string) LocalCLI { return f.opts.LocalCLI }})
			remote.mu.Lock()
			remote.accounts[token] = f.account.ID
			remote.keyScopes = map[string][]string{token: {api.ScopeAgent, api.ScopeAppsRead, api.ScopeDeployWrite}}
			remote.apiHook = func(w http.ResponseWriter, r *http.Request, account string) bool {
				if strings.HasPrefix(r.URL.Path, "/v1/apps/") {
					f.serve(w, r)
					return true
				}
				return false
			}
			remote.mu.Unlock()
			cs, ctx := connectAgentCatalog(t, remote, token, ToolsetBuildTools, protocol)
			args := uploadedTaskArgs(t)
			for _, source := range []*uploadedTaskSource{&args.Web, &args.Worker} {
				created := callTool(t, ctx, cs, "gregale_create_source_upload", createUploadArgs{App: source.App}, false)
				var id, uploadURL string
				if json.Unmarshal(created["upload_id"], &id) != nil || json.Unmarshal(created["upload_url"], &uploadURL) != nil {
					t.Fatal("invalid upload receipt")
				}
				parsed, err := url.Parse(uploadURL)
				if err != nil {
					t.Fatal(err)
				}
				entries := []tarEntry{}
				for _, file := range source.Files {
					entries = append(entries, tarEntry{name: file.Path, typeflag: tar.TypeReg, body: file.Content})
				}
				response := remote.request(http.MethodPut, parsed.RequestURI(), bytes.NewReader(tarball(t, entries...)), "application/gzip", "")
				_ = response.Body.Close()
				if response.StatusCode != http.StatusCreated {
					t.Fatal("hosted archive upload failed", response.StatusCode)
				}
				source.UploadID, source.Files = id, nil
			}
			body := publicationCall(t, ctx, cs, "gregale_plan_uploaded_task_tool_deploy", args, false)
			raw, _ := json.Marshal(body)
			var first uploadedTaskReview
			if json.Unmarshal(raw, &first) != nil || first.Kind != "hosted_task_source_review" || first.AccountID != f.account.ID || len(first.Apps) != 4 {
				t.Fatalf("hosted review lost identity: %s", raw)
			}
			publicationCall(t, ctx, cs, "gregale_plan_uploaded_task_tool_deploy", args, true)
			inline := uploadedTaskArgs(t)
			inline.Web.ExpectedSHA256, inline.Worker.ExpectedSHA256 = first.Sources["web"], first.Sources["worker"]
			again := publicationCall(t, ctx, cs, "gregale_plan_uploaded_task_tool_deploy", inline, false)
			if string(again["release_fingerprint"]) != string(body["release_fingerprint"]) {
				t.Fatal("equivalent inline and archive source produced different reviews")
			}
			dirs, err := os.ReadDir(filepath.Join(uploadRoot, "plans"))
			if err != nil || len(dirs) != 0 {
				t.Fatal("hosted review retained staged source", err)
			}
			if f.writes.Load() != 0 || f.cli.Load() != 0 {
				t.Fatal("hosted review invoked native mutations or CLI")
			}
		})
	}
}

func TestHostedUploadedTaskReviewIgnoresAmbientEnvironment(t *testing.T) {
	f := newUploadedTaskFixture(t)
	_, m, err := newManagementServer(api.NewClient(f.backend.URL, testToken), f.opts)
	if err != nil {
		t.Fatal(err)
	}
	args := uploadedTaskArgs(t)
	args.PreviousWorkerApps = nil
	first, err := m.planUploadedTaskToolDeploy(t.Context(), args)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("MCP_TASK_NAMESPACE", "TASK_PRIVATE_NAMESPACE_SENTINEL")
	t.Setenv("MCP_TASK_MIGRATION_DATABASE_URL", "postgres://owner:TASK_SECRET_SENTINEL@invalid/db")
	t.Setenv("FAAS_API", "https://ambient.invalid")
	args.PreviousWorkerApps = []string{}
	second, err := m.planUploadedTaskToolDeploy(t.Context(), args)
	if err != nil {
		t.Fatal(err)
	}
	if first.(uploadedTaskReview).Fingerprint != second.(uploadedTaskReview).Fingerprint {
		t.Fatal("ambient values or empty-list representation changed hosted fingerprint")
	}
	raw, _ := json.Marshal(second)
	if strings.Contains(string(raw), "SENTINEL") || f.cli.Load() != 0 || f.writes.Load() != 0 {
		t.Fatal("source review read private runtime setup or executed work")
	}
}
