package main

// gregalectl support-access: customer-granted support access
// (ADR-20261116163508). Support asks an organization for access, lists the
// grants customers gave, and deploys, retries, cancels, rolls back or
// restarts an app under one. Every app call names its grant with the
// Support-Grant header from the operator's own signed-in session; there is
// no other way to reach a customer's app.

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"text/tabwriter"

	"github.com/onebox-faas/faas/pkg/api"
)

const dispatchSupportAccess = "support-access"

const supportAccessUsage = "usage: gregalectl support-access <request|grants|status|deploy|retry|cancel|rollback|restart> [flags]"

func cmdSupportAccessDispatch(args []string) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		_, _ = fmt.Fprintln(osStderr, supportAccessUsage)
		return 2
	}
	switch args[0] {
	case "request":
		return cmdSupportAccessRequest(args[1:])
	case "grants":
		return cmdSupportAccessGrants(args[1:])
	case "status", "deploy", "retry", "cancel", "rollback", "restart":
		return cmdSupportAccessAct(args[0], args[1:])
	default:
		_, _ = fmt.Fprintf(osStderr, "gregalectl support-access: unknown subcommand %q\n%s\n", args[0], supportAccessUsage)
		return 2
	}
}

func cmdSupportAccessRequest(args []string) int {
	fs := flag.NewFlagSet("support-access request", flag.ContinueOnError)
	fs.SetOutput(osStderr)
	org := fs.String("org", "", "organization slug (a personal account's organization slug works too)")
	reason := fs.String("reason", "", "why support needs access; the customer reads it (3..400 characters)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if strings.TrimSpace(*org) == "" || len(strings.TrimSpace(*reason)) < 3 {
		_, _ = fmt.Fprintln(osStderr, "gregalectl support-access request: --org and --reason are required")
		return 2
	}
	var out api.SupportAccessRequest
	body := api.OperatorSupportAccessRequest{Org: strings.TrimSpace(*org), Reason: strings.TrimSpace(*reason)}
	if code := supportAccessCall("request", http.MethodPost, "/v1/admin/support-access/requests", "", body, &out); code != 0 {
		return code
	}
	if jsonEnabled() {
		return emitOperatorJSON(out)
	}
	_, _ = fmt.Fprintf(osStdout, "requested access to %s (request %s, expires %s); the owners and admins were mailed\n", out.Org, out.ID, out.ExpiresAt)
	return 0
}

func cmdSupportAccessGrants(args []string) int {
	fs := flag.NewFlagSet("support-access grants", flag.ContinueOnError)
	fs.SetOutput(osStderr)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	var out api.SupportGrantList
	if code := supportAccessCall("grants", http.MethodGet, "/v1/admin/support-access/grants", "", nil, &out); code != 0 {
		return code
	}
	if jsonEnabled() {
		return emitOperatorJSON(out)
	}
	tw := tabwriter.NewWriter(osStdout, 0, 2, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "GRANT\tORG\tAPPS\tEXPIRES\tGRANTED BY\tREASON")
	for _, g := range out.Grants {
		apps := strings.Join(g.Apps, ",")
		if g.AllApps {
			apps = "(all)"
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", g.ID, g.Org, apps, g.ExpiresAt, g.CreatedBy, g.Reason)
	}
	_ = tw.Flush()
	return 0
}

// supportActFlags are the flags of the app subcommands.
type supportActFlags struct {
	grant, app, repo, ref, deployment, fromStage, to, reason string
	fresh                                                    bool
}

func cmdSupportAccessAct(action string, args []string) int {
	fs := flag.NewFlagSet("support-access "+action, flag.ContinueOnError)
	fs.SetOutput(osStderr)
	var f supportActFlags
	fs.StringVar(&f.grant, "grant", "", "support grant id (gregalectl support-access grants)")
	fs.StringVar(&f.app, "app", "", "app slug")
	fs.StringVar(&f.repo, "repo", "", "deploy: the app's connected GitHub repository (owner/name)")
	fs.StringVar(&f.ref, "ref", "", "deploy: the app's production branch (the only ref a grant allows)")
	fs.StringVar(&f.deployment, "deployment", "", "retry/cancel: deployment id")
	fs.StringVar(&f.fromStage, "from-stage", "build", "retry: stage to retry from")
	fs.StringVar(&f.to, "to", "", "rollback: target deployment id (default: the previous one)")
	fs.StringVar(&f.reason, "reason", "", "deploy/rollback: reason recorded on the deployment")
	fs.BoolVar(&f.fresh, "fresh", false, "restart: discard snapshots and cold boot")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	method, path, body, prob := supportActRequest(action, f)
	if prob != "" {
		_, _ = fmt.Fprintf(osStderr, "gregalectl support-access %s: %s\n", action, prob)
		return 2
	}
	var out json.RawMessage
	if code := supportAccessCall(action, method, path, f.grant, body, &out); code != 0 {
		return code
	}
	if len(out) == 0 {
		out = json.RawMessage(`{}`)
	}
	if jsonEnabled() {
		return emitOperatorJSON(out)
	}
	_, _ = fmt.Fprintf(osStdout, "%s\n", out)
	return 0
}

// supportActRequest maps a subcommand to the customer route it calls.
func supportActRequest(action string, f supportActFlags) (method, path string, body any, problem string) {
	if strings.TrimSpace(f.grant) == "" {
		return "", "", nil, "--grant is required"
	}
	app := url.PathEscape(strings.TrimSpace(f.app))
	if action != "retry" && app == "" {
		return "", "", nil, "--app is required"
	}
	switch action {
	case "status":
		return http.MethodGet, "/v1/apps/" + app + "/deployments/latest", nil, ""
	case "deploy":
		if f.repo == "" || f.ref == "" {
			return "", "", nil, "--repo and --ref are required"
		}
		return http.MethodPost, "/v1/apps/" + app + "/deployments/source-ref", api.SourceRefDeployRequest{Repo: f.repo, Ref: f.ref, Reason: f.reason}, ""
	case "retry":
		if f.deployment == "" {
			return "", "", nil, "--deployment is required"
		}
		return http.MethodPost, "/v1/deployments/" + url.PathEscape(f.deployment) + "/retry", api.RetryDeploymentRequest{FromStage: f.fromStage}, ""
	case "cancel":
		if f.deployment == "" {
			return "", "", nil, "--deployment is required"
		}
		return http.MethodPost, "/v1/apps/" + app + "/deployments/" + url.PathEscape(f.deployment) + "/cancel", nil, ""
	case "rollback":
		req := api.RollbackRequest{Reason: f.reason}
		if f.to != "" {
			req.TargetDeploymentID = &f.to
		}
		return http.MethodPost, "/v1/apps/" + app + "/rollback", req, ""
	default: // restart
		path := "/v1/apps/" + app + "/restart"
		if f.fresh {
			path += "?fresh=true"
		}
		return http.MethodPost, path, nil, ""
	}
}

func supportAccessCall(action, method, path, grant string, input, output any) int {
	sess, err := loadOperatorSession()
	if err != nil {
		_, _ = fmt.Fprintf(osStderr, "gregalectl support-access %s: %v\n", action, err)
		return 1
	}
	var headers http.Header
	if grant != "" {
		headers = http.Header{api.SupportGrantHeader: []string{strings.TrimSpace(grant)}}
	}
	idempotent := method != http.MethodGet
	if err := newOperatorHTTPClient(&sess).doJSONWithHeaders(context.Background(), method, path, input, output, idempotent, nil, headers); err != nil {
		_, _ = fmt.Fprintf(osStderr, "gregalectl support-access %s: %v\n", action, err)
		return 1
	}
	return 0
}
