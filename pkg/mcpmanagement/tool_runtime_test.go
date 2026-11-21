package mcpmanagement

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale/templates"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/mcphosting"
)

func TestHostedToolTemplatesUseNativeStarters(t *testing.T) {
	for _, protocol := range []string{"2026-07-28", "2025-11-25"} {
		for _, runtime := range []string{"node", "go", "python"} {
			t.Run(protocol+"/"+runtime, func(t *testing.T) {
				dir := t.TempDir()
				if err := templates.Materialize("mcp-"+runtime, dir); err != nil {
					t.Fatal(err)
				}
				if err := validateHostedToolProject(dir, runtime); err != nil {
					t.Fatalf("native starter rejected: %v", err)
				}
				if config, err := mcphosting.Load(dir); err != nil || config.Auth.Mode != "open" {
					t.Fatalf("native starter hosting contract rejected: %v", err)
				}
				f, backend := newPublicationFixture(t)
				opts := hostedPublicationOptions(t, f, backend)
				opts.LocalCLI = func(_ context.Context, args []string, _ []byte) (LocalCLIResult, error) {
					if len(args) != 6 || !slices.Equal(args[:3], []string{"mcp", "init", "--path"}) || args[4] != "--runtime" || args[5] != runtime {
						return LocalCLIResult{}, errors.New("unexpected starter command")
					}
					return LocalCLIResult{}, templates.Materialize("mcp-"+runtime, args[3])
				}
				cs, ctx := connectSelectedTestServer(t, backend, opts, protocol)
				manifest := publicationCall(t, ctx, cs, "gregale_get_tool_template", toolTemplateArgs{Runtime: runtime}, false)
				digest, err := toolSourceDigest(dir)
				if err != nil || string(manifest["template_sha256"]) != `"`+digest+`"` {
					t.Fatalf("starter digest: %v, %v", manifest, err)
				}
				marker := map[string]string{"node": "package.json", "go": "go.mod", "python": "requirements.txt"}[runtime]
				var files []string
				if err := json.Unmarshal(manifest["files"], &files); err != nil || !slices.Contains(files, marker) {
					t.Fatalf("missing native dependency file %s: %v", marker, manifest)
				}
				chunk := publicationCall(t, ctx, cs, "gregale_get_tool_template", toolTemplateArgs{Runtime: runtime, File: marker}, false)
				var encoded string
				_ = json.Unmarshal(chunk["content"], &encoded)
				got, decodeErr := base64.StdEncoding.DecodeString(encoded)
				want, readErr := os.ReadFile(filepath.Join(dir, marker))
				if decodeErr != nil || readErr != nil || !bytes.Equal(got, want) || string(chunk["next_offset"]) != "-1" {
					t.Fatalf("starter dependency bytes: decode=%v read=%v", decodeErr, readErr)
				}
			})
		}
	}
}

func TestHostedToolPublicationRuntimeReview(t *testing.T) {
	for _, tc := range []struct {
		name, runtime, marker, content, wantCode string
	}{
		{name: "legacy Node defaults", marker: "package.json", content: `{"scripts":{"start":"node server.js"}}`},
		{name: "Go requires selection", marker: "go.mod", content: "module tool\n", wantCode: "unsupported_project"},
		{name: "Python requires selection", marker: "requirements.txt", wantCode: "unsupported_project"},
		{name: "unknown runtime", runtime: "ruby", marker: "Gemfile", wantCode: "invalid_argument"},
		{name: "Go mismatch", runtime: "go", marker: "package.json", content: `{"scripts":{"start":"node server.js"}}`, wantCode: "unsupported_project"},
		{name: "Python mismatch", runtime: "python", marker: "go.mod", content: "module tool\n", wantCode: "unsupported_project"},
		{name: "Go missing marker", runtime: "go", marker: "main.go", wantCode: "unsupported_project"},
		{name: "Python missing marker", runtime: "python", marker: "server.py", wantCode: "unsupported_project"},
		{name: "Node missing start", runtime: "node", marker: "package.json", content: `{}`, wantCode: "unsupported_project"},
		{name: "Node malformed package", runtime: "node", marker: "package.json", content: `{`, wantCode: "unsupported_project"},
		{name: "Python requirements", runtime: "python", marker: "requirements.txt"},
		{name: "Python pyproject", runtime: "python", marker: "pyproject.toml", content: "[project]\nname = 'tool'\n"},
		{name: "Python Pipfile", runtime: "python", marker: "Pipfile"},
		{name: "Python setup", runtime: "python", marker: "setup.py"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, backend := newPublicationFixture(t)
			opts := hostedPublicationOptions(t, f, backend)
			cs, ctx := connectSelectedTestServer(t, backend, opts, "2026-07-28")
			args := uploadedToolArgs(t)
			args.Runtime, args.Files[0] = tc.runtime, inlineFile{Path: tc.marker, Content: tc.content}
			out := publicationCall(t, ctx, cs, "gregale_plan_uploaded_tool_deploy", args, tc.wantCode != "")
			if tc.wantCode != "" {
				if string(out["code"]) != `"`+tc.wantCode+`"` {
					t.Fatalf("error code: %v", out)
				}
				opts.uploads.mu.Lock()
				defer opts.uploads.mu.Unlock()
				if len(opts.uploads.dirs) != 0 {
					t.Fatal("invalid runtime retained source")
				}
			} else {
				want := tc.runtime
				if want == "" {
					want = "node"
				}
				if string(out["runtime"]) != `"`+want+`"` {
					t.Fatalf("normalized review: %v", out)
				}
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.deploys != 0 || f.tests != 0 || len(f.journal) != 0 {
				t.Fatal("runtime review dispatched publication")
			}
		})
	}
}

func TestHostedToolPublicationRejectsMixedRuntimeMarkers(t *testing.T) {
	for _, runtime := range []string{"node", "go", "python"} {
		for _, marker := range []string{"Dockerfile", "dOcKeRfIlE", "package.json", "requirements.txt", "go.mod"} {
			if marker == map[string]string{"node": "package.json", "go": "go.mod", "python": "requirements.txt"}[runtime] {
				continue
			}
			t.Run(runtime+"/"+marker, func(t *testing.T) {
				args := uploadedRuntimeToolArgs(t, runtime)
				args.Files = append(args.Files, inlineFile{Path: marker, Content: "build override"})
				f, backend := newPublicationFixture(t)
				opts := hostedPublicationOptions(t, f, backend)
				cs, ctx := connectSelectedTestServer(t, backend, opts, "2026-07-28")
				publicationCall(t, ctx, cs, "gregale_plan_uploaded_tool_deploy", args, true)
			})
		}
	}
}

func TestHostedToolPublicationRuntimeMarkersStaySourceBound(t *testing.T) {
	for _, runtime := range []string{"node", "go", "python"} {
		t.Run(runtime, func(t *testing.T) {
			f, backend := newPublicationFixture(t)
			opts := hostedPublicationOptions(t, f, backend)
			_, m, err := newManagementServer(api.NewClient(backend.URL, testToken), opts)
			if err != nil {
				t.Fatal(err)
			}
			out, err := m.planUploadedToolDeploy(t.Context(), uploadedRuntimeToolArgs(t, runtime))
			if err != nil {
				t.Fatal(err)
			}
			id := out.(map[string]any)["plan_id"].(string)
			m.mu.Lock()
			source := m.toolPublications[id].Path
			m.mu.Unlock()
			if err := os.WriteFile(filepath.Join(source, "Dockerfile"), []byte("FROM scratch\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err = m.applyToolDeploy(t.Context(), applyArgs{PlanID: id})
			var problem *inputError
			if !errors.As(err, &problem) || problem.code != "source_changed" {
				t.Fatalf("changed runtime accepted: %v", err)
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.deploys != 0 || len(f.journal) != 0 {
				t.Fatal("changed runtime reached native publication")
			}
		})
	}
}
