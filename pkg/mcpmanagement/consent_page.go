package mcpmanagement

import (
	"html"
	"strings"
)

// consentCapabilityChoices are the optional scopes chosen before sign-in.
const consentCapabilityChoices = `<fieldset><legend>With write access, also allow (optional)</legend><label><input type="checkbox" name="capability" value="secrets:request"> Asking you to enter secrets on a Gregale page; the agent never sees their values</label><br><label><input type="checkbox" name="capability" value="storage:manage"> Creating object storage buckets and binding them to apps</label><br><label><input type="checkbox" name="capability" value="postgres:manage"> Creating managed PostgreSQL databases and binding them to apps (billed)</label></fieldset>`

// connectionLifetime states how long an approved connection lasts: a browser
// sign-in's slides while used (ADR-20261028205920), a pasted key's does not.
func connectionLifetime(renewable bool) string {
	if renewable {
		return "The connection stays while your agent uses it: it ends after a week unused, and 30 days after you approve at the latest."
	}
	return "The connection lasts at most eight hours."
}

// consentPage renders the bridge's authorization page. It escapes every value
// with html.EscapeString instead of executing an html/template: template
// execution reaches reflect.Value.MethodByName, which makes the linker keep
// every exported method of every type converted to an interface in each
// daemon that links this package (docs/adr/20261020160744-daemon-blast-radius.md).
// Every URL it writes is server-built or checked first (startNativeLogin
// accepts only https or loopback http login URLs).
func consentPage(v consentView) string {
	e := html.EscapeString
	var b strings.Builder
	w := func(parts ...string) {
		for _, part := range parts {
			b.WriteString(part)
		}
	}
	hidden := `<input type="hidden" name="request_id" value="` + e(v.RequestID) + `"><input type="hidden" name="csrf" value="` + e(v.CSRF) + `">`
	w(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>Connect to Gregale</title></head>
<body><main><h1>Connect to Gregale</h1><p>Client: <strong>`, e(v.Client), `</strong></p><p>Return address: `, e(v.Redirect), `</p><p>Gregale API: `, e(v.APIBase), "</p>\n")
	switch {
	case v.Account != "":
		w(`<p>Account: <strong>`, e(v.Account), `</strong></p><p>This client can inspect apps, deployments, logs, metrics, health and routes in this account. App logs may contain private data.</p>`)
		if len(v.Capabilities) > 0 {
			w(`<p>With write access it may also:</p><ul>`)
			for _, capability := range v.Capabilities {
				w(`<li>`, e(capability), `</li>`)
			}
			w(`</ul>`)
		}
		w("\n", `<form method="post" action="`, e(v.ConsentAction), `">`, hidden, "\n")
		if v.Writes {
			w(`<p><label><input type="checkbox" name="allow_writes" value="yes" required> Also allow reviewed deployments, production promotion, and checked rollback. These actions can change traffic and incur costs.</label></p>`)
		}
		w("\n", `<fieldset><legend>Apps this agent may access</legend><label><input type="radio" name="app_access" value="all" required> All apps in this account, including future apps</label><br><label><input type="radio" name="app_access" value="selected" required> Only selected apps</label>`)
		for _, app := range v.Apps {
			w(`<p><label><input type="checkbox" name="apps" value="`, e(app.Slug), `"> `, e(app.Slug), `</label></p>`)
		}
		w(`</fieldset>`, "\n", `<fieldset><legend>App diagnostics (optional)</legend><p>Allow retained request status, routes, timing, performance evidence, retained wake observations, trace-correlated invocation lifecycle metadata, sampled CPU profile function labels and collection/attribution summaries, and scheduler residency causes across all environments of each selected app. This may contain private application data. Future apps are excluded. Native plan and retention limits still apply. This does not grant environment, log or write access.</p>`)
		for _, app := range v.Apps {
			w(`<p><label><input type="checkbox" name="request_read_apps" value="`, e(app.Slug), `"> Read app diagnostics for `, e(app.Slug), `</label></p>`)
		}
		w(`</fieldset>`, "\n")
		if v.Writes {
			w(`<fieldset><legend>Regression triage (optional)</legend><p>Allow reviewed acknowledge, dismiss, resolve and reopen actions for selected apps. Select diagnostic read access above and write access to each target environment below. These actions update triage metadata.</p>`)
			for _, app := range v.Apps {
				w(`<p><label><input type="checkbox" name="regression_write_apps" value="`, e(app.Slug), `"> Triage regressions for `, e(app.Slug), `</label></p>`)
			}
			w(`</fieldset>`)
		}
		w("\n", `<fieldset><legend>Environment access</legend><p>Default app inspection is included. Named environments require separate consent for the selected apps.</p>`)
		if v.Writes {
			w(`<p><label><input type="checkbox" name="default_write" value="yes"> Allow writes to the default environment</label></p>`)
		}
		for _, env := range v.Environments {
			value := e(env.Project) + "/" + e(env.Environment)
			name := e(env.Project) + " / " + e(env.Environment)
			w(`<p><label><input type="checkbox" name="environments" value="`, value, `"> Read `, name, `</label>`)
			if v.Writes {
				w(` <label><input type="checkbox" name="environment_writes" value="`, value, `"> Write `, name, `</label>`)
			}
			w(`</p>`)
		}
		w(`</fieldset>`, "\n", `<p>Environment promotions affect every project workload and require consent for all of them. Protected targets still need a human-issued Gregale approval. `, connectionLifetime(v.Native), ` Revoke it through your client or revoke its credential in Gregale.</p>`, "\n",
			`<button name="decision" value="approve" type="submit">Connect this account</button> <button name="decision" value="deny" type="submit" formnovalidate>Cancel</button></form>`)
	case v.Native && v.SameTab && v.LoginURL == "":
		// The hosted bridge sends the browser to sign-in and back here.
		w(`<p>Continue to Gregale to sign in, or to create an account with GitHub, Google or email. You come back here to review the agent's access.</p>`, "\n")
		w("\n", `<form method="post" action="`, e(v.ConsentAction), `">`, hidden)
		if v.Writes {
			w(consentCapabilityChoices)
		}
		w(`<button name="decision" value="start-login" type="submit">Continue to Gregale</button><button name="decision" value="deny" type="submit">Cancel</button></form>`)
	case v.Native:
		w(`<p>Sign in to Gregale to choose your account. Then return here to review the agent's access.</p>`, "\n")
		if v.LoginURL != "" {
			w(`<p><a href="`, e(v.LoginURL), `" target="_blank" rel="noopener noreferrer">Sign in to Gregale</a></p>`)
		}
		w("\n", `<form method="post" action="`, e(v.ConsentAction), `">`, hidden)
		if v.Writes && v.LoginURL == "" {
			w(consentCapabilityChoices)
		}
		decision, label := "start-login", "Start Gregale sign-in"
		if v.LoginURL != "" {
			decision, label = "check-login", "I authorized in Gregale; review account"
		}
		w(`<button name="decision" value="`, decision, `" type="submit">`, label, `</button><button name="decision" value="deny" type="submit">Cancel</button></form>`)
	default:
		w(`<p>Enter a Gregale API key for the account you want to connect. Use a dedicated key with the smallest needed API scopes. The next screen identifies the account before you approve access.</p>`, "\n",
			`<form method="post" action="`, e(v.ConsentAction), `" autocomplete="off">`, hidden, "\n",
			`<p><label>Gregale API key <input name="api_key" type="password" required autocomplete="off" maxlength="4096"></label></p>`, "\n",
			`<button name="decision" value="verify" type="submit">Review account</button> <button name="decision" value="deny" type="submit" formnovalidate>Cancel</button></form>`)
	}
	w("\n</main></body></html>")
	return b.String()
}
