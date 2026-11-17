package mcpmanagement

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/tasktoolsource"
	"github.com/onebox-faas/faas/pkg/tasktoolsource/sourcecheck"
)

func TestHostedTaskSourceIntentThroughHTTP(t *testing.T) {
	for _, protocol := range []string{api.MCPProtocolVersion, api.MCPLegacyProtocolVersion} {
		t.Run(protocol, func(t *testing.T) {
			f := newUploadedTaskFixture(t)
			const token = "fp_test_hosted_task_retention"
			f.token = token
			var mu sync.Mutex
			rows := map[string]api.HostedTaskSourceIntent{}
			writes := 0
			lose := true
			requester := uuid.NewString()
			hook := func(w http.ResponseWriter, r *http.Request, _ string) bool {
				prefix := "/v1/task-tool-source-intents/"
				if !strings.HasPrefix(r.URL.Path, prefix) {
					if strings.HasPrefix(r.URL.Path, "/v1/apps/") {
						f.serve(w, r)
						return true
					}
					return false
				}
				mu.Lock()
				defer mu.Unlock()
				id := strings.TrimPrefix(r.URL.Path, prefix)
				if r.Method == http.MethodGet {
					record, ok := rows[id]
					if !ok {
						api.WriteProblem(w, api.NewProblem(404, "not_found", "Not found", ""))
					} else {
						_ = json.NewEncoder(w).Encode(record)
					}
					return true
				}
				var in api.HostedTaskSourcePrepare
				if json.NewDecoder(r.Body).Decode(&in) != nil {
					t.Error("malformed retained source")
					w.WriteHeader(400)
					return true
				}
				review, err := sourcecheck.ValidatePrepare(f.account.ID, in)
				if err != nil {
					t.Error(err)
					w.WriteHeader(400)
					return true
				}
				record, err := tasktoolsource.NewRecord(f.account.ID, requester, id, review, time.Now())
				if err != nil {
					t.Error(err)
					w.WriteHeader(400)
					return true
				}
				rows[id] = record
				writes++
				if lose {
					api.WriteProblem(w, api.NewProblem(503, "lost_reply", "Lost committed reply", ""))
				} else {
					_ = json.NewEncoder(w).Encode(record)
				}
				return true
			}
			roots := []string{}
			makeBridge := func() *remoteFixture {
				uploadRoot := filepath.Join(t.TempDir(), "uploads")
				roots = append(roots, uploadRoot)
				remote := newRemoteFixtureWithOptions(t, true, HTTPOptions{Hosted: true, AgentKeys: true, AgentKeyToolsetSelection: true, AllowToolPublishing: true, Toolsets: []string{ToolsetBuildTools}, UploadDir: uploadRoot, CLIRunner: func(string) LocalCLI { return f.opts.LocalCLI }})
				remote.mu.Lock()
				remote.accounts[token] = f.account.ID
				remote.keyScopes = map[string][]string{token: {api.ScopeAgent, api.ScopeAppsRead, api.ScopeDeployWrite}}
				remote.apiHook = hook
				remote.mu.Unlock()
				return remote
			}
			remote := makeBridge()
			cs, ctx := connectAgentCatalog(t, remote, token, ToolsetBuildTools, protocol)
			args := uploadedTaskArgs(t)
			planned := publicationCall(t, ctx, cs, "gregale_plan_uploaded_task_tool_deploy", args, false)
			raw, _ := json.Marshal(planned)
			var review uploadedTaskReview
			if json.Unmarshal(raw, &review) != nil {
				t.Fatal("invalid review")
			}
			prepare := prepareUploadedTaskArgs{planUploadedTaskArgs: args, IntentID: strings.Repeat("e", 64), ExpectedReviewSHA256: review.Fingerprint}
			raw, _ = json.Marshal(prepare)
			var forbidden map[string]any
			_ = json.Unmarshal(raw, &forbidden)
			forbidden["credential"] = "PRIVATE_TASK_SENTINEL"
			denied, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "gregale_prepare_uploaded_task_tool_deploy", Arguments: forbidden})
			if err != nil || !denied.IsError {
				t.Fatalf("undeclared credential accepted: %v", err)
			}
			deniedJSON, _ := json.Marshal(denied)
			if strings.Contains(string(deniedJSON), "PRIVATE_TASK_SENTINEL") {
				t.Fatal("validation echoed a credential value")
			}

			publicationCall(t, ctx, cs, "gregale_prepare_uploaded_task_tool_deploy", prepare, true)
			_ = cs.Close()
			remote = makeBridge()
			cs, ctx = connectAgentCatalog(t, remote, token, ToolsetBuildTools, protocol)
			lose = false
			got := publicationCall(t, ctx, cs, "gregale_get_uploaded_task_tool_deploy", getUploadedTaskIntentArgs{prepare.IntentID}, false)
			publicationCall(t, ctx, cs, "gregale_prepare_uploaded_task_tool_deploy", prepare, false)
			mu.Lock()
			count := writes
			mu.Unlock()
			if count != 1 || f.cli.Load() != 0 || f.writes.Load() != 0 {
				t.Fatalf("writes=%d CLI=%d other writes=%d", count, f.cli.Load(), f.writes.Load())
			}
			raw, _ = json.Marshal(got)
			if strings.Contains(string(raw), "PRIVATE_TASK_SENTINEL") || strings.Contains(string(raw), "customer source must not execute") {
				t.Fatal("source or credential leaked")
			}
			// All staged source directories are released even after the lost response.
			for _, root := range roots {
				dirs, err := os.ReadDir(filepath.Join(root, "plans"))
				if err != nil && !os.IsNotExist(err) || len(dirs) > 0 {
					t.Fatal("staged source survived uncertain outcome", err)
				}
			}
		})
	}
}
