package mcpmanagement

import (
	"slices"
	"strings"
)

// accessDeploy is the "Deploy and manage" choice on the page before sign-in.
const accessDeploy = "deploy"

// deployToolsetSpec is the tool selection a deploy choice loads when the
// person kept the default core tools.
const deployToolsetSpec = "core,release,provision,operate"

// consentAccessChoice lets the person pick what a client that asked only to
// inspect may do. Clients request the scope the 401 challenge names, which is
// inspection, so without it every new connection (claude.ai, Cursor) could
// read but never deploy (ADR-20261028205920 amendment 1). Deploy is
// preselected; the review page still requires the write confirmation.
const consentAccessChoice = `<fieldset><legend>What may this agent do?</legend><label><input type="radio" name="access" value="deploy" form="gregale-connect" checked> Deploy and manage apps (recommended). You confirm write access on the next page.</label><br><label><input type="radio" name="access" value="inspect" form="gregale-connect"> Inspect only: apps, deployments, logs and health</label></fieldset>`

// offersWrites reports whether the page before sign-in offers deploy access:
// the bridge allows writes, the client did not already ask for them, and
// sign-in has not started.
func (r *remoteServer) offersWrites(flow *authorizationRequest) bool {
	return r.opts.AllowWrites && r.opts.Onboarding == "browser" && flow.NativeCode == "" && flow.GrantID == "" && !slices.Contains(flow.Scopes, scopeWrite)
}

// deployToolsets upgrades the default core selection to the deploy tools when
// this bridge offers them; any other explicit selection is kept.
func deployToolsets(spec string, maximum []string) string {
	if trimmed := strings.TrimSpace(spec); trimmed != "" && trimmed != ToolsetCore {
		return spec
	}
	if _, err := connectionToolsets(deployToolsetSpec, maximum); err != nil {
		return spec
	}
	return deployToolsetSpec
}
