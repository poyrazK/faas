package main

import (
	"context"
	"errors"
	"flag"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/hosting"
)

func hostingAutomationWorkstationEnabled() bool {
	v := strings.ToLower(os.Getenv("FAAS_FRONTEND_HOSTING_AUTOMATION_ENABLED"))
	return v == "1" || v == "true"
}
func cmdSitesTokens(args []string) int {
	if !hostingAutomationWorkstationEnabled() {
		return printErr("Site automation is disabled", errors.New("set FAAS_FRONTEND_HOSTING_AUTOMATION_ENABLED=1; the API needs its independent opt-in"))
	}
	if len(args) == 0 {
		return printErr("Credential action required", errors.New("use sites tokens <create|list|revoke>"))
	}
	verb := args[0]
	flags := newFlagSet("sites-tokens-"+verb, flag.ContinueOnError)
	site := flags.String("site", "", "site UUID")
	id := flags.String("credential", "", "credential UUID to revoke")
	label := flags.String("label", "", "credential label")
	ttl := flags.Duration("ttl", api.HostingAutomationTTLDefault, "credential lifetime")
	ops := flags.String("operations", "stage,preview", "stage and/or preview")
	out := flags.String("out", "", "new private token file (required for create; never stdout)")
	if err := flags.Parse(args[1:]); err != nil || rejectUnexpectedFlagArgs(flags) {
		return 1
	}
	if uuid.Validate(*site) != nil || verb != "create" && verb != "list" && verb != "revoke" || verb == "revoke" && uuid.Validate(*id) != nil || verb == "create" && (*out == "" || !hosting.ValidHostingAutomationLabel(*label) || !hosting.ValidHostingAutomationOperations(strings.Split(*ops, ",")) || *ttl < api.HostingAutomationTTLMin || *ttl > api.HostingAutomationTTLMax || *ttl%time.Second != 0) {
		return printErr("Invalid credential options", errors.New("use a site UUID, create/list/revoke, and bounded create options with --out"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()
	switch verb {
	case "list":
		rows, err := client.ListHostingAutomationCredentials(ctx, *site)
		if err != nil {
			return printErr("Credential inventory unavailable", err)
		}
		return jsonOut(writeJSON(rows))
	case "revoke":
		if err := client.RevokeHostingAutomationCredential(ctx, *site, *id); err != nil {
			return printErr("Credential revocation not confirmed", err)
		}
		return jsonOut(writeJSON(map[string]string{"state": "revoked", "credential_id": *id}))
	}
	file, err := os.OpenFile(*out, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return printErr("Cannot create private token file", errors.New("choose a new writable --out path"))
	}
	keep := false
	defer func() {
		_ = file.Close()
		if !keep {
			_ = os.Remove(*out)
		}
	}()
	response, err := client.CreateHostingAutomationCredential(ctx, *site, api.CreateHostingAutomationCredentialRequest{Label: *label, Operations: strings.Split(*ops, ","), TTLSeconds: int64(*ttl / time.Second)})
	if err != nil {
		return printErr("Credential issuance not confirmed; list credentials before retrying", err)
	}
	if !api.ValidHostingAutomationToken(response.Token) || uuid.Validate(response.Credential.ID) != nil || response.Credential.SiteID != *site {
		return printErr("Invalid credential response", errors.New("inspect and revoke the issued credential before retrying"))
	}
	if _, err = file.WriteString(response.Token + "\n"); err == nil {
		err = file.Sync()
	}
	if err != nil {
		_ = client.RevokeHostingAutomationCredential(ctx, *site, response.Credential.ID)
		return printErr("Could not save credential", errors.New("check the credential inventory before retrying"))
	}
	keep = true
	return jsonOut(writeJSON(response.Credential))
}
