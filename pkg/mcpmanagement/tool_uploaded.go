package mcpmanagement

import (
	"context"
	"encoding/json"

	"github.com/onebox-faas/faas/pkg/api"
)

type planUploadedToolArgs struct {
	App                string       `json:"app"`
	Reason             string       `json:"reason"`
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
	if err = validateHostedNodeProject(dir); err != nil {
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
	return out, nil
}

// This preview accepts Node application projects, not shell commands or a
// host-side dependency installer. Build scripts execute in normal builder VMs.
func validateHostedNodeProject(dir string) error {
	data, err := readToolProjectFile(dir, "package.json", maxInlineBytes)
	var pkg struct {
		Scripts map[string]string `json:"scripts"`
	}
	if err != nil || json.Unmarshal(data, &pkg) != nil || pkg.Scripts["start"] == "" {
		return invalid("unsupported_project", "hosted publication requires a Node package.json with a start script")
	}
	return nil
}
