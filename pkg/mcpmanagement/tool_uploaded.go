package mcpmanagement

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/markers"
)

type planUploadedToolArgs struct {
	App                string       `json:"app"`
	Reason             string       `json:"reason"`
	Runtime            string       `json:"runtime,omitempty" jsonschema:"node (default), go or python; must match the uploaded project's native build markers"`
	UploadID           string       `json:"upload_id,omitempty"`
	Files              []inlineFile `json:"files,omitempty"`
	Fixtures           string       `json:"fixtures,omitempty"`
	AccessMode         string       `json:"access_mode"`
	AllowToolExecution bool         `json:"allow_tool_execution"`
}

func (m *management) planUploadedToolDeploy(ctx context.Context, a planUploadedToolArgs) (any, error) {
	if err := m.toolBuildingAccess(); err != nil {
		return nil, err
	}
	if !m.remote() || a.AccessMode != "open" || !a.AllowToolExecution || !api.ValidAppSlug(a.App) || (a.UploadID == "") == (len(a.Files) == 0) {
		return nil, invalid("invalid_argument", "hosted publication requires open access, explicit fixture consent, a valid app and either upload_id or files")
	}
	if err := validateReason(a.App, a.Reason); err != nil {
		return nil, err
	}
	if a.Runtime == "" {
		a.Runtime = "node"
	}
	if a.Runtime != "node" && a.Runtime != "go" && a.Runtime != "python" {
		return nil, invalid("invalid_argument", "runtime must be node, go or python")
	}
	account, err := m.client.Whoami(ctx)
	if err != nil {
		return nil, err
	}
	if account.KeyApp != nil || account.KeyOrg != nil || len(account.KeyProjects) != 0 {
		return nil, invalid("tool_build_access_denied", "hosted publication requires an unscoped personal-account publishing credential")
	}
	dir, err := m.uploadedProject(a.App, a.UploadID, a.Files)
	if err != nil {
		return nil, err
	}
	return m.planHostedToolProject(ctx, dir, a)
}

func (m *management) planHostedToolProject(ctx context.Context, dir string, a planUploadedToolArgs) (any, error) {
	if err := validateHostedToolProject(dir, a.Runtime); err != nil {
		m.opts.uploads.removeDir(dir)
		return nil, err
	}
	out, err := m.planToolProject(ctx, planToolDeployArgs{Path: dir, App: a.App, Reason: a.Reason, Fixtures: a.Fixtures, AccessMode: a.AccessMode, AllowToolExecution: a.AllowToolExecution})
	if err != nil {
		m.opts.uploads.removeDir(dir)
		return nil, err
	}
	id := out["plan_id"].(string)
	m.mu.Lock()
	m.toolPublications[id].Uploaded = true
	m.mu.Unlock()
	// Runtime is derived from the reviewed source, not an independent deploy
	// override. The source digest and frozen apply snapshot bind its markers.
	out["runtime"] = a.Runtime
	return out, nil
}

// Use the same marker precedence as the CLI and native builder. Dependencies
// and customer build commands execute only in normal builder VMs on apply.
func validateHostedToolProject(dir, runtime string) error {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	family, err := markers.DetectFromFS(root.FS())
	if err != nil || string(family) != runtime {
		return invalid("unsupported_project", "runtime must match native project detection; hosted publication supports Node, Go and Python application projects")
	}
	entries, err := fs.ReadDir(root.FS(), ".")
	if err != nil {
		return err
	}
	for _, entry := range entries {
		marker := markers.MarkerFor(entry.Name())
		if !entry.IsDir() && marker != markers.FrameworkUnknown && marker != family {
			return invalid("unsupported_project", "hosted publication requires root build markers from a single runtime family")
		}
	}
	if family != markers.FrameworkNode {
		return nil
	}
	data, err := readToolProjectFile(dir, "package.json", maxInlineBytes)
	var pkg struct {
		Scripts map[string]string `json:"scripts"`
	}
	if err != nil || json.Unmarshal(data, &pkg) != nil || pkg.Scripts["start"] == "" {
		return invalid("unsupported_project", "hosted publication requires a Node package.json with a start script")
	}
	return nil
}
