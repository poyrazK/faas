package mcpmanagement

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale/templates"
	"github.com/onebox-faas/faas/pkg/mcpopenapi"
)

const openAPIToolDocument = `{"openapi":"3.1.0","paths":{"/sum":{"get":{"operationId":"add","responses":{"200":{"content":{"application/json":{"schema":{"type":"object","properties":{"sum":{"type":"number"}},"required":["sum"],"additionalProperties":false}}}}}}}}}`

func openAPIGeneratorCLI(t *testing.T, publisher LocalCLI, calls *atomic.Int64) LocalCLI {
	t.Helper()
	return func(ctx context.Context, args []string, stdin []byte) (LocalCLIResult, error) {
		if !slices.Contains(args, "import") {
			return publisher(ctx, args, stdin)
		}
		calls.Add(1)
		value := func(flag string) string {
			i := slices.Index(args, flag)
			if i < 0 || i+1 >= len(args) {
				t.Fatalf("missing generator flag %s", flag)
			}
			return args[i+1]
		}
		if value("--spec") != "-" || string(stdin) != openAPIToolDocument {
			t.Fatal("generation accepted a host path or changed specification bytes")
		}
		project, err := mcpopenapi.Generate(stdin, mcpopenapi.Options{ServerURL: value("--server-url"), OperationIDs: strings.Split(value("--operations"), ",")})
		if err != nil {
			return LocalCLIResult{}, err
		}
		dir := value("--path")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return LocalCLIResult{}, err
		}
		if err := templates.Materialize("mcp-node", dir); err != nil {
			return LocalCLIResult{}, err
		}
		if err := os.RemoveAll(filepath.Join(dir, "test")); err != nil {
			return LocalCLIResult{}, err
		}
		for name, content := range project.Files {
			target := filepath.Join(dir, filepath.FromSlash(name))
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return LocalCLIResult{}, err
			}
			if err := os.WriteFile(target, content, 0o644); err != nil {
				return LocalCLIResult{}, err
			}
		}
		return LocalCLIResult{}, nil
	}
}

func TestOpenAPIToolsHostedGenerationAndReviewedPublication(t *testing.T) {
	for _, protocol := range []string{"2026-07-28", "2025-11-25"} {
		t.Run(protocol, func(t *testing.T) {
			f, backend := newPublicationFixture(t)
			opts := hostedPublicationOptions(t, f, backend)
			var generated atomic.Int64
			opts.LocalCLI = openAPIGeneratorCLI(t, f.cli, &generated)
			cs, ctx := connectSelectedTestServer(t, backend, opts, protocol)
			args := generateOpenAPIToolsArgs{Document: openAPIToolDocument, ServerURL: "https://api.example.com", OperationIDs: []string{"add"}}
			out := publicationCall(t, ctx, cs, "gregale_generate_tools_from_openapi", args, false)
			if string(out["fixtures_require_review"]) != "true" || string(out["operation_ids"]) != `["add"]` || !strings.Contains(string(out["files"]), "openapi-tools.json") {
				t.Fatal("generation omitted review requirements or source manifest")
			}
			// Reconstruct only the files the existing publisher needs. The
			// production CLI test separately checks the complete Node scaffold.
			files := []inlineFile{{Path: "package.json", Content: `{"scripts":{"start":"node server.js"}}`}}
			for _, name := range []string{"server.js", "gregale-mcp.json", "openapi-tools.json", "gregale-mcp.tests.example.json"} {
				args.File = name
				chunk := publicationCall(t, ctx, cs, "gregale_generate_tools_from_openapi", args, false)
				if string(chunk["project_sha256"]) != string(out["project_sha256"]) {
					t.Fatal("file transfer changed the deterministic source identity")
				}
				var encoded string
				if err := json.Unmarshal(chunk["content"], &encoded); err != nil {
					t.Fatal(err)
				}
				data, err := base64.StdEncoding.DecodeString(encoded)
				if err != nil || string(chunk["next_offset"]) != "-1" {
					t.Fatal("small generated file was not transferred exactly", err)
				}
				files = append(files, inlineFile{Path: name, Content: string(data)})
			}
			planArgs := planUploadedToolArgs{App: "alpha", Reason: "publish reviewed API adapter", Files: files, AccessMode: "open", AllowToolExecution: true}
			publicationCall(t, ctx, cs, "gregale_plan_uploaded_tool_deploy", planArgs, true)
			f.mu.Lock()
			if f.deploys != 0 || f.tests != 0 {
				t.Fatal("generation or incomplete examples triggered execution")
			}
			f.mu.Unlock()
			planArgs.Files = append(planArgs.Files, inlineFile{Path: "gregale-mcp.tests.json", Content: `{"version":1,"cases":[{"name":"adds","kind":"tool","target":"add","arguments":{},"expected_result":{"content":[{"type":"text","text":"API operation completed."}],"structuredContent":{"result":{"sum":3}}}}]}`})
			plan := publicationCall(t, ctx, cs, "gregale_plan_uploaded_tool_deploy", planArgs, false)
			var id string
			if err := json.Unmarshal(plan["plan_id"], &id); err != nil {
				t.Fatal(err)
			}
			publicationCall(t, ctx, cs, "gregale_apply_tool_deploy", applyArgs{PlanID: id}, false)
			done := waitPublication(t, ctx, cs, id)
			if string(done["status"]) != `"completed"` {
				t.Fatalf("publication did not complete: %v", done)
			}
			publicationCall(t, ctx, cs, "gregale_apply_tool_deploy", applyArgs{PlanID: id}, false)
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.deploys != 1 || generated.Load() != 5 {
				t.Fatal("repeat apply replayed publication or source generation unexpectedly")
			}
		})
	}
}

func TestOpenAPIToolsReadOnlyGenerationAndValidation(t *testing.T) {
	f, backend := newPublicationFixture(t)
	var generated atomic.Int64
	opts := Options{APIBase: backend.URL, Token: testToken, LocalCLI: openAPIGeneratorCLI(t, f.cli, &generated), Toolsets: []string{ToolsetBuildTools}}
	cs, ctx := connectSelectedTestServer(t, backend, opts, "2026-07-28")
	args := generateOpenAPIToolsArgs{Document: openAPIToolDocument, ServerURL: "https://api.example.com", OperationIDs: []string{"add"}}
	publicationCall(t, ctx, cs, "gregale_generate_tools_from_openapi", args, false)
	for _, change := range []func(*generateOpenAPIToolsArgs){
		func(a *generateOpenAPIToolsArgs) { a.Document = "{}" },
		func(a *generateOpenAPIToolsArgs) { a.OperationIDs = []string{"missing"} },
		func(a *generateOpenAPIToolsArgs) { a.ServerURL = "https://127.0.0.1" },
		func(a *generateOpenAPIToolsArgs) { a.Offset = -1 },
	} {
		invalid := args
		change(&invalid)
		publicationCall(t, ctx, cs, "gregale_generate_tools_from_openapi", invalid, true)
	}
	if generated.Load() != 1 {
		t.Fatal("invalid generation arguments reached the CLI")
	}
	args.File = "../outside"
	publicationCall(t, ctx, cs, "gregale_generate_tools_from_openapi", args, true)
}
