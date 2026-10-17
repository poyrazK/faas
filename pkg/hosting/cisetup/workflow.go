package cisetup

import (
	"fmt"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

type workflow struct {
	Name        string            `yaml:"name"`
	On          map[string]any    `yaml:"on"`
	Permissions map[string]string `yaml:"permissions"`
	Jobs        map[string]job    `yaml:"jobs"`
}
type job struct {
	RunsOn      string            `yaml:"runs-on"`
	Timeout     int               `yaml:"timeout-minutes"`
	If          string            `yaml:"if"`
	Environment string            `yaml:"environment,omitempty"`
	Permissions map[string]string `yaml:"permissions"`
	Steps       []step            `yaml:"steps"`
}
type step struct {
	Name  string            `yaml:"name"`
	ID    string            `yaml:"id,omitempty"`
	Uses  string            `yaml:"uses,omitempty"`
	With  map[string]any    `yaml:"with,omitempty"`
	Env   map[string]string `yaml:"env,omitempty"`
	Shell string            `yaml:"shell,omitempty"`
	Run   string            `yaml:"run,omitempty"`
}

func expressionLiteral(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
func buildWorkflow(t api.HostingOIDCTrust, c api.HostingCISetupConfig) workflow {
	on := map[string]any{}
	switch t.EventName {
	case "push":
		on["push"] = map[string]any{"branches": []string{strings.TrimPrefix(t.Ref, "refs/heads/")}}
	case "workflow_dispatch":
		on["workflow_dispatch"] = map[string]any{}
	case "schedule":
		on["schedule"] = []map[string]string{{"cron": c.Cron}}
	}
	guard := "${{ github.repository == " + expressionLiteral(t.Repository) + " && github.ref == " + expressionLiteral(t.Ref) + " && github.event_name == " + expressionLiteral(t.EventName) + " }}"
	verify := fmt.Sprintf(`python3 - <<'PY'
import hashlib, os, pathlib, sys
path = pathlib.Path(os.environ['GREGALE_CLI_PATH'])
if not path.is_file() or not os.access(path, os.X_OK) or path.stat().st_size > %d:
    sys.exit('Prepared Gregale CLI is missing, non-executable or oversized.')
digest = hashlib.sha256()
with path.open('rb') as source:
    for chunk in iter(lambda: source.read(1048576), b''):
        digest.update(chunk)
if digest.hexdigest() != os.environ['GREGALE_CLI_SHA256']:
    sys.exit('Prepared Gregale CLI does not match the reviewed SHA-256.')
PY
`, api.HostingCISetupCLIMaxBytes)
	out := workflow{Name: "Gregale internal frontend preview", On: on, Permissions: map[string]string{}, Jobs: map[string]job{"frontend": {
		RunsOn: "ubuntu-latest", Timeout: api.HostingCISetupJobTimeoutMinutes, If: guard, Environment: t.Environment,
		Permissions: map[string]string{"contents": "read", "id-token": "write"},
		Steps: []step{
			{Name: "Check out the exact trusted run commit", Uses: "actions/checkout@" + c.CheckoutCommit, With: map[string]any{"ref": "${{ github.sha }}", "persist-credentials": false}},
			{Name: "Prepare trusted CLI and prebuilt output", Uses: c.PrepareAction},
			{Name: "Verify the reviewed Gregale CLI", Shell: "bash", Env: map[string]string{"GREGALE_CLI_PATH": c.CLIPath, "GREGALE_CLI_SHA256": c.CLISHA256}, Run: verify},
			{Name: "Stage frontend and publish private preview", ID: "frontend", Uses: "poyrazK/faas/.github/actions/frontend-hosting@" + c.ActionCommit,
				With: map[string]any{"enabled": "true", "oidc-enabled": "true", "oidc-trust": t.ID, "api-base": c.APIBase, "site": t.SiteID, "cli-path": c.CLIPath, "path": c.OutputPath, "spa": fmt.Sprint(c.SPAFallback), "preview-ttl": fmt.Sprintf("%ds", c.PreviewTTLSeconds), "wait-timeout": fmt.Sprintf("%ds", c.WaitTimeoutSeconds)}},
		},
	}}}
	if c.ReservePreview {
		out.Jobs["frontend"].Steps[3].With["reserve-preview"] = "true"
	}
	return out
}
