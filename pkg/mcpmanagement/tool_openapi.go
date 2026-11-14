package mcpmanagement

import (
	"bytes"
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/mcpopenapi"
)

type generateOpenAPIToolsArgs struct {
	Document     string   `json:"document" jsonschema:"OpenAPI 3.0 or 3.1 JSON/YAML document text, at most 256 KiB; never a URL or host path"`
	ServerURL    string   `json:"server_url" jsonschema:"Explicit public HTTPS API base URL; overrides document servers; no credentials, query or fragment"`
	OperationIDs []string `json:"operation_ids" jsonschema:"Select 1 to 16 exact public operation IDs; no automatic endpoint selection"`
	File         string   `json:"file,omitempty" jsonschema:"Exact file from the generated manifest; omit to list files"`
	Offset       int      `json:"offset,omitempty" jsonschema:"Byte offset from next_offset for a base64 file chunk"`
}

func (m *management) generateOpenAPITools(ctx context.Context, a generateOpenAPIToolsArgs) (any, error) {
	if err := m.toolBuildingAccess(); err != nil {
		return nil, err
	}
	if a.Offset < 0 || a.File == "" && a.Offset != 0 {
		return nil, invalid("invalid_argument", "offset must be non-negative and requires a selected file")
	}
	project, err := mcpopenapi.Generate([]byte(a.Document), mcpopenapi.Options{ServerURL: a.ServerURL, OperationIDs: a.OperationIDs})
	if err != nil {
		return nil, invalid("invalid_openapi", err.Error())
	}
	parent, err := os.MkdirTemp("", "gregale-openapi-tools-")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(parent) }()
	dir := filepath.Join(parent, "project")
	if _, err := m.runCLI(ctx, []string{"mcp", "server", "import", "--spec", "-", "--path", dir,
		"--server-url", a.ServerURL, "--operations", strings.Join(a.OperationIDs, ",")}, []byte(a.Document)); err != nil {
		return nil, err
	}
	manifest, err := readToolProjectFile(dir, "openapi-tools.json", api.ToolOpenAPIManifestMaxBytes)
	if err != nil || !bytes.Equal(manifest, project.Files["openapi-tools.json"]) {
		return nil, invalid("invalid_response", "CLI generator differs from the reviewed document and operations; use matching Gregale releases")
	}
	digest, err := toolSourceDigest(dir)
	if err != nil {
		return nil, err
	}
	return generatedOpenAPIResult(dir, a, project.Manifest, digest)
}

func generatedOpenAPIResult(dir string, a generateOpenAPIToolsArgs, manifest mcpopenapi.Manifest, digest string) (any, error) {
	ids := slices.Clone(a.OperationIDs)
	sort.Strings(ids)
	out := map[string]any{"schema_version": 1, "runtime": "node", "document_sha256": manifest.DocumentSHA256,
		"project_sha256": digest, "server_url": manifest.ServerURL, "operation_ids": ids, "fixtures_require_review": true}
	if a.File != "" {
		data, err := readToolProjectFile(dir, a.File, toolProjectHashBytes)
		if err != nil {
			return nil, err
		}
		if a.Offset > len(data) {
			return nil, invalid("invalid_argument", "file chunk offset exceeds the selected file")
		}
		end := min(len(data), a.Offset+toolTemplateChunkBytes)
		next := -1
		if end < len(data) {
			next = end
		}
		out["file"], out["offset"], out["next_offset"] = a.File, a.Offset, next
		out["encoding"], out["content"] = "base64", base64.StdEncoding.EncodeToString(data[a.Offset:end])
		return out, nil
	}
	files := []string{}
	err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() {
			name, err := filepath.Rel(dir, path)
			if err != nil {
				return err
			}
			files = append(files, filepath.ToSlash(name))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	out["files"] = files
	return out, nil
}
