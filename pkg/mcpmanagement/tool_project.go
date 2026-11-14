package mcpmanagement

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/mcphosting"
)

// Local review/file-transfer bounds; deployment quotas remain native.
const toolProjectHashBytes int64 = 512 << 20
const toolFixtureBytes int64 = 64 << 10
const toolTemplateChunkBytes = 16 << 10

type toolProject struct {
	path, digest, fixtures, fixturesDigest string
	config                                 mcphosting.Config
	suite                                  mcphosting.SmokeSuite
	caseNames                              []string
}

func loadToolProject(a toolProjectArgs) (toolProject, error) {
	var p toolProject
	if !tokenEnvAllowed(a.AccessMode, a.TokenEnv) {
		return p, invalid("invalid_argument", "declare open access without a token, or external-oauth with a named MCP_* client-token variable")
	}
	dir, err := localProjectDir(a.Path)
	if err != nil {
		return p, err
	}
	p.path = dir
	p.digest, err = toolSourceDigest(dir)
	if err != nil {
		return p, err
	}
	p.config, err = mcphosting.Load(dir)
	if err != nil {
		return p, invalid("invalid_project", "invalid gregale-mcp.json hosting contract")
	}
	if p.config.Auth.Mode != a.AccessMode || len(p.config.Auth.ToolScopes) == 0 {
		return p, invalid("invalid_project", "access_mode must match the project and tool_scopes must explicitly declare the tools")
	}
	if p.config.Tasks != nil && p.config.Tasks.Enabled != nil && *p.config.Tasks.Enabled {
		return p, invalid("unsupported_project", "durable MCP Tasks require their dedicated stack release workflow")
	}
	p.fixtures = a.Fixtures
	if p.fixtures == "" {
		p.fixtures = "gregale-mcp.tests.json"
	}
	data, err := readToolProjectFile(dir, p.fixtures, toolFixtureBytes)
	if err != nil {
		return p, err
	}
	p.suite, err = mcphosting.LoadSmokeSuite(bytes.NewReader(data))
	if err != nil || !p.suite.RequiresToolExecution() {
		return p, invalid("invalid_fixtures", "publication requires valid reviewed behavioral fixtures with at least one tool case")
	}
	for _, c := range p.suite.Cases {
		if c.Kind == "tool" {
			if _, ok := p.config.Auth.ToolScopes[c.Target]; !ok {
				return p, invalid("invalid_fixtures", "a tool fixture target is absent from the declared tool policy")
			}
		}
		p.caseNames = append(p.caseNames, c.Name)
	}
	digest := sha256.Sum256(data)
	p.fixturesDigest = hex.EncodeToString(digest[:])
	return p, nil
}

func readToolProjectFile(dir, name string, maxBytes int64) ([]byte, error) {
	if !fs.ValidPath(name) || name == "." || strings.Contains(name, "\\") {
		return nil, invalid("invalid_path", "file must be a literal project-relative path")
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	info, err := root.Lstat(name)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxBytes {
		return nil, invalid("invalid_path", "project file is missing, non-regular or exceeds the read bound")
	}
	f, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, maxBytes+1))
	if err != nil || int64(len(data)) > maxBytes {
		return nil, invalid("invalid_path", "project file exceeds the read bound")
	}
	return data, nil
}

// Hash bytes as well as names and modes. Symlinks/devices cannot enter a
// reviewed tool project; no customer code is executed during this review.
func toolSourceDigest(dir string) (string, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return "", err
	}
	defer func() { _ = root.Close() }()
	h := sha256.New()
	var total int64
	count := 0
	err = fs.WalkDir(root.FS(), ".", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if name == ".git" && entry.IsDir() {
			return fs.SkipDir
		}
		if entry.IsDir() {
			info, err := entry.Info()
			if err != nil {
				return err
			}
			_, err = fmt.Fprintf(h, "directory:%s\x00%o\x00", name, info.Mode().Perm())
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return invalid("invalid_project", "tool source must contain only regular files and directories")
		}
		count++
		total += info.Size()
		if count > maxLocalSourceFiles || total > toolProjectHashBytes {
			return invalid("source_too_large", "tool source exceeds local review bounds; remove generated dependencies or build outputs")
		}
		if _, err := fmt.Fprintf(h, "%s\x00%d\x00%o\x00", name, info.Size(), info.Mode().Perm()); err != nil {
			return err
		}
		f, err := root.Open(name)
		if err != nil {
			return err
		}
		n, copyErr := io.Copy(h, io.LimitReader(f, info.Size()+1))
		closeErr := f.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		if n != info.Size() {
			return invalid("source_changed", "tool source changed while it was being reviewed")
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

type toolTemplateArgs struct {
	Runtime string `json:"runtime" jsonschema:"node, go or python"`
	File    string `json:"file,omitempty" jsonschema:"Exact file from the template manifest; omit to list files"`
	Offset  int    `json:"offset,omitempty" jsonschema:"Byte offset from next_offset for a text chunk"`
}

func (m *management) getToolTemplate(ctx context.Context, a toolTemplateArgs) (any, error) {
	if err := m.toolBuildingAccess(); err != nil {
		return nil, err
	}
	if a.Runtime != "node" && a.Runtime != "go" && a.Runtime != "python" || a.Offset < 0 {
		return nil, invalid("invalid_argument", "runtime must be node, go or python and offset non-negative")
	}
	if m.remote() && a.Runtime != "node" {
		return nil, invalid("unsupported_project", "hosted tool publishing currently supports the Node starter")
	}
	parent, err := os.MkdirTemp("", "gregale-tool-template-")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(parent) }()
	// init publishes the scaffold with an atomic rename, so its destination
	// must not already exist. Keep the containing directory private.
	dir := filepath.Join(parent, "project")
	if _, err := m.runCLI(ctx, []string{"mcp", "init", "--path", dir, "--runtime", a.Runtime}, nil); err != nil {
		return nil, err
	}
	digest, err := toolSourceDigest(dir)
	if err != nil {
		return nil, err
	}
	if a.File != "" {
		data, err := readToolProjectFile(dir, a.File, toolProjectHashBytes)
		if err != nil {
			return nil, err
		}
		if a.Offset > len(data) {
			return nil, invalid("invalid_argument", "template chunk requires a returned byte offset")
		}
		end := min(len(data), a.Offset+toolTemplateChunkBytes)
		next := -1
		if end < len(data) {
			next = end
		}
		return map[string]any{"schema_version": 1, "runtime": a.Runtime, "template_sha256": digest, "file": a.File, "offset": a.Offset, "next_offset": next, "encoding": "base64", "content": base64.StdEncoding.EncodeToString(data[a.Offset:end])}, nil
	}
	if a.Offset != 0 {
		return nil, invalid("invalid_argument", "offset is only available for a selected file")
	}
	files := []string{}
	err = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
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
	return map[string]any{"schema_version": 1, "runtime": a.Runtime, "template_sha256": digest, "files": files, "protocol_version": mcphosting.ProtocolVersion}, nil
}

func (m *management) testTool(ctx context.Context, a testToolArgs) (any, error) {
	if m.remote() {
		return nil, invalid("tool_build_access_denied", "hosted fixtures run only within a reviewed publication")
	}
	if err := m.toolBuildingAccess(); err != nil {
		return nil, err
	}
	if !m.opts.AllowWrites || !a.AllowToolExecution {
		return nil, invalid("writes_disabled", "behavioral tests require writes enabled and explicit allow_tool_execution")
	}
	if err := validateSegment(a.DeploymentID, "deployment_id"); err != nil {
		return nil, err
	}
	if err := validateSegment(a.App, "app"); err != nil {
		return nil, err
	}
	p, err := loadToolProject(toolProjectArgs{Path: a.Path, Fixtures: a.Fixtures, AccessMode: a.AccessMode, TokenEnv: a.TokenEnv})
	if err != nil {
		return nil, err
	}
	app, err := m.client.GetApp(ctx, a.App)
	if err != nil {
		return nil, err
	}
	dep, err := m.client.GetDeployment(ctx, a.DeploymentID)
	if err != nil {
		return nil, err
	}
	if app.ID == "" || app.Slug != a.App || dep.ID != a.DeploymentID || dep.AppID != app.ID || dep.Status != "live" || dep.TrafficPercent != 0 || normalizedScope(dep.Scope) != api.DefaultEnvScope {
		return nil, invalid("invalid_target", "behavioral tests require this app's exact live zero-traffic default-scope candidate")
	}
	preview, err := m.client.GetDeploymentURL(ctx, a.DeploymentID)
	if err != nil {
		return nil, err
	}
	if !preview.Alive {
		return nil, invalid("invalid_target", "candidate preview is unavailable")
	}
	endpoint, err := p.config.URL(preview.URL)
	if err != nil {
		return nil, err
	}
	args := []string{"mcp", "test", "--url", endpoint, "--fixtures", filepath.Join(p.path, filepath.FromSlash(p.fixtures)), "--allow-tool-execution", "--timeout", "20s"}
	if a.TokenEnv != "" {
		args = append(args, "--token-env", a.TokenEnv)
	}
	result, err := m.opts.LocalCLI(ctx, args, nil)
	if err != nil {
		return nil, err
	}
	var report mcphosting.SmokeReport
	if json.Unmarshal(result.Stdout, &report) != nil || report.Checks == nil {
		return nil, invalid("invalid_response", "test runner returned no structured smoke report; test outcome is unknown")
	}
	return map[string]any{"schema_version": 1, "app": a.App, "deployment_id": a.DeploymentID, "passed": result.ExitCode == 0 && report.OK, "report": report}, nil
}
