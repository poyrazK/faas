// commands_orgs.go — `gregale orgs <subcommand>` customer CLI for
// IAM-6 / ADR-061 / issue #190 (org CRUD + member management +
// invitations + ownership transfer).
//
// The org surface is the largest IAM feature behind MFA — five
// top-level subcommands, two nested dispatchers (`orgs members ...`,
// `orgs invitations ...`), plus a `transfer-ownership` leaf. The
// standalone `gregale invitations peek|accept` lives here too
// because the token-based entry points don't carry a slug context.
//
// Subcommand vocab mirrors the API verbs (the kebab-case surface in
// api/openapi.yaml, gated by the closed role vocab at
// pkg/api/orgs.go:AllowedOrgRoles). The CLI never validates the
// role locally beyond the closed-set typo check — apid re-validates
// at the boundary, and the CLI's job is to surface a clean error
// before the network round-trip.
//
// Invitation tokens are one-shot (per pkg/api/orgs.go:OrgInvitationResponse):
// the plaintext token is returned ONCE on the create call and the
// server-side row stores only its SHA-256 hash. The CLI prints the
// token to stdout immediately after the API call returns; the
// customer copies it into an email/off-channel ping. There is no
// re-fetch path — losing the token means re-inviting.

package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// orgSlugRe is the compiled OrgSlugPattern (pkg/api/errors.go:787)
// used by validateOrgsUpdateFlags. Compiled once at package init
// instead of recompiled on every CLI invocation — a CLI that
// loops over hundreds of orgs in a script would otherwise pay the
// regex-parse cost per call. Hoisted to a package var (mirrors
// how pkg/api/registry_auth.go exposes RegistryHostRe()) so a
// malformed api.OrgSlugPattern panics at CLI start, not at the
// first validateOrgsUpdateFlags call.
var orgSlugRe = regexp.MustCompile(api.OrgSlugPattern)

// cmdOrgs dispatches `gregale orgs <subcommand>`. The top-level
// fan-out mirrors the existing webhooks/crons dispatcher shape so
// the customer sees the same `list|create|info|rm` + nested
// namespacing across all three surfaces.
func cmdOrgs(args []string) int {
	if len(args) == 0 {
		PrintUsage(os.Stderr, "usage: gregale orgs <list|create|info|update|rm|members|invitations|keys|transfer-ownership|seat-usage> [args]", "orgs")
		return 1
	}
	switch args[0] {
	case "ls", subList:
		return cmdOrgsLs(args[1:])
	case "create":
		return cmdOrgsCreate(args[1:])
	case subInfo:
		return cmdOrgsInfo(args[1:])
	case subUpdate:
		return cmdOrgsUpdate(args[1:])
	case subRm:
		return cmdOrgsRm(args[1:])
	case "members":
		return cmdOrgsMembers(args[1:])
	case "invitations":
		return cmdOrgsInvitations(args[1:])
	case "keys":
		// Tier C audit gap: org-scoped API keys
		// (GET/POST/DELETE/rotate /v1/orgs/{slug}/keys). Mirror
		// the standalone `gregale keys` surface but bind each
		// leaf to --org <slug>; the org-scoped route is the
		// canonical one post-ADR-061.
		return cmdOrgsKeys(args[1:])
	case "transfer-ownership":
		return cmdOrgsTransferOwnership(args[1:])
	case "seat-usage":
		return cmdOrgsSeatUsage(args[1:])
	case "me":
		// Tier B audit gap: GET /v1/orgs/me reports the caller's
		// current active-org hint (per the X-Active-Org header). CI
		// scripts that switch orgs need to introspect which org
		// they're currently scoped to.
		return cmdOrgsMe(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "gregale orgs: unknown subcommand %q\n", args[0])
		return 1
	}
}

// cmdOrgsMembers is the nested dispatcher for `gregale orgs members
// <list|invite|change-role|rm>`. The vocabulary mirrors the API verbs
// rather than the dashboard's "add member / edit role" labels —
// `gregale crons list` already established this posture for the
// scheduled-requests surface.
func cmdOrgsMembers(args []string) int {
	if len(args) == 0 {
		PrintUsage(os.Stderr, "usage: gregale orgs members <list|invite|change-role|rm> [args]", "orgs")
		return 1
	}
	switch args[0] {
	case subList:
		return cmdOrgsMembersLs(args[1:])
	case "invite":
		return cmdOrgsMembersInvite(args[1:])
	case "change-role":
		return cmdOrgsMembersChangeRole(args[1:])
	case subRm:
		return cmdOrgsMembersRm(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "gregale orgs members: unknown subcommand %q\n", args[0])
		return 1
	}
}

// cmdOrgsInvitations is the nested dispatcher for `gregale orgs
// invitations <list|list-all|revoke>`. The standalone `gregale
// invitations peek|accept` (token-based, no slug context) lives in
// cmdInvitations below.
func cmdOrgsInvitations(args []string) int {
	if len(args) == 0 {
		PrintUsage(os.Stderr, "usage: gregale orgs invitations <list|list-all|revoke> [args]", "orgs")
		return 1
	}
	switch args[0] {
	case subList:
		return cmdOrgsInvitationsLs(args[1:])
	case "list-all":
		// Tier C audit gap: ListOrgInvitationsAll walks the
		// next_before cursor end-to-end (issue #394 follow-up).
		// Used by admins running periodic audit sweeps across
		// every open invitation row for a given org.
		return cmdOrgsInvitationsListAll(args[1:])
	case "revoke":
		return cmdOrgsInvitationsRevoke(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "gregale orgs invitations: unknown subcommand %q\n", args[0])
		return 1
	}
}

// cmdInvitations is the standalone dispatcher for `gregale invitations
// <peek|accept>`. Token-based entry points without a slug context —
// peek is unauth-friendly (the server validates the token itself),
// accept requires an authenticated session + 5-min strict step-up
// (server-side; CLI surfaces the 403 as-is).
func cmdInvitations(args []string) int {
	if len(args) == 0 {
		PrintUsage(os.Stderr, "usage: gregale invitations <peek|accept> <token>", "invitations")
		return 1
	}
	switch args[0] {
	case "peek":
		return cmdInvitationsPeek(args[1:])
	case "accept":
		return cmdInvitationsAccept(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "gregale invitations: unknown subcommand %q\n", args[0])
		return 1
	}
}

// orgRoleForInviteVocab is the invite-eligible subset mirrored from
// pkg/api.AllowedOrgMemberRolesForInvite — excludes owner because
// transfer-ownership is the only path to ownership. Used by the
// `invite` leaf only.
var orgRoleForInviteVocab = api.AllowedOrgMemberRolesForInvite

// orgRoleForPatchVocab is the change-role-eligible subset mirrored
// from pkg/api.AllowedOrgDirectPatchRoles. Same as
// orgRoleForInviteVocab per pkg/api/orgs.go:73-77 — PATCH cannot
// promote a member to owner; only transfer-ownership can.
var orgRoleForPatchVocab = api.AllowedOrgDirectPatchRoles

// --- org CRUD leaves -------------------------------------------------------

func cmdOrgsLs(args []string) int {
	fs := flag.NewFlagSet("orgs ls", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 0 {
		PrintUsage(os.Stderr, "usage: gregale orgs ls", "orgs")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	resp, err := client.ListOrgs(context.Background())
	if err != nil {
		return printErr("Could not list orgs", err)
	}
	if jsonOutput {
		return jsonOut(writeNDJSON(resp.Orgs))
	}
	if len(resp.Orgs) == 0 {
		PrintProgress(osStdout, "(no orgs)")
		return 0
	}
	for _, o := range resp.Orgs {
		flag := ""
		if o.Personal {
			flag = " (personal)"
		}
		fmt.Printf("%-32s %-12s %-10s %s%s\n", o.Slug, o.Plan, o.Status, o.Name, flag)
	}
	return 0
}

func cmdOrgsCreate(args []string) int {
	fs := flag.NewFlagSet("orgs create", flag.ContinueOnError)
	slug := fs.String("slug", "", "org slug (required; lowercase alphanumeric + dashes)")
	name := fs.String("name", "", "display name (required)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if *slug == "" || *name == "" {
		PrintUsage(os.Stderr, "usage: gregale orgs create --slug <slug> --name <display>", "orgs")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	o, err := client.CreateOrg(context.Background(), api.CreateOrgRequest{Slug: *slug, Name: *name})
	if err != nil {
		return printErr("Could not create org", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(o))
	}
	PrintOK(osStdout, "Org created: %s (%s)", o.Slug, o.Plan)
	return 0
}

func cmdOrgsInfo(args []string) int {
	fs := flag.NewFlagSet("orgs info", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 1 {
		PrintUsage(os.Stderr, "usage: gregale orgs info <slug>", "orgs")
		return 1
	}
	slug := fs.Arg(0)
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	o, err := client.GetOrg(context.Background(), slug)
	if err != nil {
		return printErr("Could not fetch org", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(o))
	}
	fmt.Printf("slug:      %s\n", o.Slug)
	fmt.Printf("name:      %s\n", o.Name)
	fmt.Printf("plan:      %s\n", o.Plan)
	fmt.Printf("status:    %s\n", o.Status)
	fmt.Printf("personal:  %v\n", o.Personal)
	fmt.Printf("created:   %s\n", o.CreatedAt)
	fmt.Printf("updated:   %s\n", o.UpdatedAt)
	return 0
}

func cmdOrgsRm(args []string) int {
	fs := flag.NewFlagSet("orgs rm", flag.ContinueOnError)
	quiet := fs.Bool("q", false, "suppress confirmation prompt")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 1 {
		PrintUsage(os.Stderr, "usage: gregale orgs rm <slug> [-q]", "orgs")
		return 1
	}
	slug := fs.Arg(0)
	if !*quiet {
		fmt.Fprintf(os.Stderr,
			"This will soft-delete the org %q (apps + members retained; restore via dashboard).\n"+
				"Continue? [y/N] ", slug)
		var ans string
		_, _ = fmt.Scanln(&ans)
		if ans != "y" {
			fmt.Println("aborted")
			return 1
		}
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	if err := client.DeleteOrg(context.Background(), slug); err != nil {
		return printErr("Could not delete org", err)
	}
	PrintOK(osStdout, "Org %s scheduled for deletion", slug)
	return 0
}

// --- members ---------------------------------------------------------------

func cmdOrgsMembersLs(args []string) int {
	fs := flag.NewFlagSet("orgs members ls", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 1 {
		PrintUsage(os.Stderr, "usage: gregale orgs members ls <slug>", "orgs")
		return 1
	}
	slug := fs.Arg(0)
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	resp, err := client.ListOrgMembers(context.Background(), slug)
	if err != nil {
		return printErr("Could not list members", err)
	}
	if jsonOutput {
		return jsonOut(writeNDJSON(resp.Members))
	}
	for _, m := range resp.Members {
		fmt.Printf("%-36s %-32s %-12s %s\n", m.AccountID, m.Email, m.Role, m.JoinedAt)
	}
	return 0
}

func cmdOrgsMembersInvite(args []string) int {
	fs := flag.NewFlagSet("orgs members invite", flag.ContinueOnError)
	slug := fs.String("org", "", "org slug (required)")
	email := fs.String("email", "", "invitee email (required)")
	role := fs.String("role", "developer", "role: admin|developer|viewer|billing (owner is rejected)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if *slug == "" || *email == "" {
		PrintUsage(os.Stderr, "usage: gregale orgs members invite --org <slug> --email <addr> [--role admin|developer|viewer|billing]", "orgs")
		return 1
	}
	if !strInSlice(*role, orgRoleForInviteVocab) {
		return printErr("Invalid --role", fmt.Errorf("must be one of %v (owner is invite-rejected); got %q", orgRoleForInviteVocab, *role))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	resp, err := client.InviteOrgMember(context.Background(), *slug, api.InviteMemberRequest{Email: *email, Role: *role})
	if err != nil {
		return printErr("Could not create invitation", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(resp))
	}
	// One-shot token: print immediately, do NOT log elsewhere.
	// The customer copies the token into an off-channel ping.
	PrintOK(osStdout, "Invitation created for %s (role=%s)", *email, *role)
	PrintProgress(osStdout, "token: %s", resp.Token)
	PrintProgress(osStdout, "expires_at: %s", resp.ExpiresAt)
	PrintProgress(osStdout, "Send the token to the invitee; it is single-use and will NOT be shown again.")
	return 0
}

func cmdOrgsMembersChangeRole(args []string) int {
	fs := flag.NewFlagSet("orgs members change-role", flag.ContinueOnError)
	slug := fs.String("org", "", "org slug (required)")
	userID := fs.String("user", "", "user-id (required)")
	role := fs.String("role", "", "new role: admin|developer|viewer|billing (owner is rejected)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if *slug == "" || *userID == "" || *role == "" {
		PrintUsage(os.Stderr, "usage: gregale orgs members change-role --org <slug> --user <user-id> --role <role>", "orgs")
		return 1
	}
	if !strInSlice(*role, orgRoleForPatchVocab) {
		return printErr("Invalid --role", fmt.Errorf("must be one of %v (owner is patch-rejected; use transfer-ownership); got %q", orgRoleForPatchVocab, *role))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	if _, err := client.ChangeOrgMemberRole(context.Background(), *slug, *userID, api.ChangeMemberRoleRequest{Role: *role}); err != nil {
		return printErr("Could not change role", err)
	}
	PrintOK(osStdout, "Member %s role changed to %s", *userID, *role)
	return 0
}

func cmdOrgsMembersRm(args []string) int {
	fs := flag.NewFlagSet("orgs members rm", flag.ContinueOnError)
	slug := fs.String("org", "", "org slug (required)")
	userID := fs.String("user", "", "user-id (required)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if *slug == "" || *userID == "" {
		PrintUsage(os.Stderr, "usage: gregale orgs members rm --org <slug> --user <user-id>", "orgs")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	if err := client.RemoveOrgMember(context.Background(), *slug, *userID); err != nil {
		return printErr("Could not remove member", err)
	}
	PrintOK(osStdout, "Member %s removed from org %s", *userID, *slug)
	return 0
}

// --- org-level invitations -------------------------------------------------

func cmdOrgsInvitationsLs(args []string) int {
	fs := flag.NewFlagSet("orgs invitations list", flag.ContinueOnError)
	slug := fs.String("org", "", "org slug (required)")
	limit := fs.Int("limit", 50, "max rows (1..200; server caps at 200)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if *slug == "" {
		PrintUsage(os.Stderr, "usage: gregale orgs invitations list --org <slug> [--limit N]", "orgs")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	resp, err := client.ListOrgInvitations(context.Background(), *slug, "", *limit)
	if err != nil {
		return printErr("Could not list invitations", err)
	}
	if jsonOutput {
		return jsonOut(writeNDJSON(resp.Invitations))
	}
	for _, inv := range resp.Invitations {
		fmt.Printf("%-36s %-30s %-12s %-10s %s\n", inv.ID, inv.Email, inv.Role, inv.Status, inv.ExpiresAt)
	}
	return 0
}

func cmdOrgsInvitationsRevoke(args []string) int {
	fs := flag.NewFlagSet("orgs invitations revoke", flag.ContinueOnError)
	slug := fs.String("org", "", "org slug (required)")
	invID := fs.String("invitation", "", "invitation-id (required)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if *slug == "" || *invID == "" {
		PrintUsage(os.Stderr, "usage: gregale orgs invitations revoke --org <slug> --invitation <id>", "orgs")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	if err := client.RevokeInvitation(context.Background(), *slug, *invID); err != nil {
		return printErr("Could not revoke invitation", err)
	}
	PrintOK(osStdout, "Invitation %s revoked", *invID)
	return 0
}

// --- ownership + seats -----------------------------------------------------

func cmdOrgsTransferOwnership(args []string) int {
	fs := flag.NewFlagSet("orgs transfer-ownership", flag.ContinueOnError)
	slug := fs.String("org", "", "org slug (required)")
	to := fs.String("to", "", "user-id of the new owner (required)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if *slug == "" || *to == "" {
		PrintUsage(os.Stderr, "usage: gregale orgs transfer-ownership --org <slug> --to <user-id>", "orgs")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	if _, err := client.TransferOrgOwnership(context.Background(), *slug, api.TransferOwnershipRequest{NewOwnerAccountID: *to}); err != nil {
		return printErr("Could not transfer ownership", err)
	}
	PrintOK(osStdout, "Ownership of %s transferred to %s. The previous owner is now admin.", *slug, *to)
	return 0
}

func cmdOrgsSeatUsage(args []string) int {
	fs := flag.NewFlagSet("orgs seat-usage", flag.ContinueOnError)
	slug := fs.String("org", "", "org slug (required)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if *slug == "" {
		PrintUsage(os.Stderr, "usage: gregale orgs seat-usage --org <slug>", "orgs")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	resp, err := client.GetOrgSeatUsage(context.Background(), *slug)
	if err != nil {
		return printErr("Could not fetch seat usage", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(resp))
	}
	fmt.Printf("plan:  %s\n", resp.Plan)
	fmt.Printf("used:  %d\n", resp.Used)
	fmt.Printf("limit: %d\n", resp.Limit)
	return 0
}

// --- standalone invitations (token-based, no slug context) ----------------

func cmdInvitationsPeek(args []string) int {
	fs := flag.NewFlagSet("invitations peek", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 1 {
		PrintUsage(os.Stderr, "usage: gregale invitations peek <token>", "invitations")
		return 1
	}
	token := fs.Arg(0)
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	resp, err := client.PeekInvitation(context.Background(), token)
	if err != nil {
		return printErr("Could not peek invitation", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(resp))
	}
	fmt.Printf("org:        %s\n", resp.OrgSlug)
	fmt.Printf("email:      %s\n", resp.Email)
	fmt.Printf("role:       %s\n", resp.Role)
	fmt.Printf("status:     %s\n", resp.Status)
	fmt.Printf("expires_at: %s\n", resp.ExpiresAt)
	return 0
}

func cmdInvitationsAccept(args []string) int {
	fs := flag.NewFlagSet("invitations accept", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 1 {
		PrintUsage(os.Stderr, "usage: gregale invitations accept <token>", "invitations")
		return 1
	}
	token := fs.Arg(0)
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	m, err := client.AcceptInvitation(context.Background(), token)
	if err != nil {
		return printErr("Could not accept invitation", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(m))
	}
	PrintOK(osStdout, "Joined %s as %s", m.AccountID, m.Role)
	return 0
}

// cmdOrgsMe fetches GET /v1/orgs/me (Tier B audit gap, IAM-6
// follow-up). Returns the caller's currently-active org plus their
// role on it, or {org:null} when no X-Active-Org hint was sent (the
// caller is operating in the account scope). CI scripts that switch
// orgs need this to introspect which org they're currently scoped
// to without re-parsing env vars.
//
// Auth: self, no admin scope required.
func cmdOrgsMe(args []string) int {
	fs := flag.NewFlagSet("orgs me", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 0 {
		PrintUsage(os.Stderr, "usage: gregale orgs me", "orgs")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	resp, err := client.GetMyOrg(context.Background())
	if err != nil {
		return printErr("Could not fetch active org", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(resp))
	}
	if resp.Org == nil {
		PrintOK(osStdout, "No active org (account scope).")
		return 0
	}
	fmt.Printf("slug:  %s\n", resp.Org.Slug)
	fmt.Printf("name:  %s\n", resp.Org.Name)
	fmt.Printf("role:  %s\n", resp.Org.Role)
	fmt.Printf("plan:  %s\n", resp.Org.Plan)
	fmt.Printf("status: %s\n", resp.Org.Status)
	return 0
}

// cmdOrgsKeys is the nested dispatcher for `gregale orgs keys
// <list|add|info|rm|rotate> --org <slug>`. The standalone `gregale
// keys ...` surface is account-scoped; this dispatcher mirrors it
// but binds every leaf to the org-scoped route
// (GET/POST/DELETE/POST /v1/orgs/{slug}/keys[/...]).
//
// Org-scoped keys are the canonical post-ADR-061 form: every key
// minted via these routes is stamped with api_keys.org_id (per
// pkg/api/dto.go:1177 — migration 00127 makes the column NOT NULL),
// so a developer-role member cannot silently escalate to a
// contractor key in another org via the personal-scope route.
func cmdOrgsKeys(args []string) int {
	if len(args) == 0 {
		PrintUsage(os.Stderr, "usage: gregale orgs keys <list|add|info|rm|rotate> --org <slug> [args]", "orgs")
		return 1
	}
	switch args[0] {
	case subList:
		return cmdOrgsKeysList(args[1:])
	case subAdd:
		return cmdOrgsKeysAdd(args[1:])
	case "info":
		return cmdOrgsKeysInfo(args[1:])
	case subRm:
		return cmdOrgsKeysRm(args[1:])
	case "rotate":
		return cmdOrgsKeysRotate(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "gregale orgs keys: unknown subcommand %q\n", args[0])
		return 1
	}
}

// cmdOrgsKeysList mirrors cmdOrgsInvitationsLs: --org <slug>
// required; --json emits NDJSON. Server-side filters out revoked
// rows (per pkg/api/dto.go:1264-1272); to see revoked rows the
// operator runs --json and filters client-side.
func cmdOrgsKeysList(args []string) int {
	fs := flag.NewFlagSet("orgs keys list", flag.ContinueOnError)
	slug := fs.String("org", "", "org slug (required)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if *slug == "" {
		PrintUsage(os.Stderr, "usage: gregale orgs keys list --org <slug>", "orgs")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	resp, err := client.ListOrgAPIKeys(context.Background(), *slug)
	if err != nil {
		return printErr("Could not list keys", err)
	}
	if jsonOutput {
		return jsonOut(writeNDJSON(resp.Keys))
	}
	if len(resp.Keys) == 0 {
		_, _ = fmt.Fprintln(osStdout, "(no keys)")
		return 0
	}
	for _, k := range resp.Keys {
		fmt.Printf("%-32s %-20s %s\n", k.ID, k.Prefix, k.Label)
	}
	return 0
}

// cmdOrgsKeysAdd mirrors cmdKeysAdd: --label is required,
// --scopes is optional (comma-separated; closed-set validated
// server-side per pkg/api/dto.go:1252-1262). The plaintext is
// returned ONCE on the create call — the CLI prints it to stdout
// immediately and never persists it (consistent with the
// cmdKeysAdd contract).
func cmdOrgsKeysAdd(args []string) int {
	fs := flag.NewFlagSet("orgs keys add", flag.ContinueOnError)
	slug := fs.String("org", "", "org slug (required)")
	label := fs.String("label", "", "key label (required)")
	scopesCSV := fs.String("scopes", "", "comma-separated scopes (default: [admin])")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if *slug == "" || *label == "" {
		PrintUsage(os.Stderr, "usage: gregale orgs keys add --org <slug> --label <text> [--scopes admin,apps:read,...]", "orgs")
		return 1
	}
	var scopes []string
	if *scopesCSV != "" {
		for _, s := range strings.Split(*scopesCSV, ",") {
			s = strings.TrimSpace(s)
			if s != "" {
				scopes = append(scopes, s)
			}
		}
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	k, err := client.CreateOrgAPIKey(context.Background(), *slug, *label, scopes)
	if err != nil {
		return printErr("Create failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(k))
	}
	PrintOK(osStdout, "New org API key (shown ONCE):\n  %s", k.Plaintext)
	PrintProgress(osStdout, "  id:     %s", k.ID)
	PrintProgress(osStdout, "  prefix: %s", k.Prefix)
	PrintProgress(osStdout, "  label:  %s", k.Label)
	return 0
}

// cmdOrgsKeysInfo mirrors cmdAuditEventsGet: single id positional,
// multi-line labelled block. Plaintext is NEVER returned (per
// pkg/api/dto.go:1196-1197) — the renderer sticks to id/prefix/
// label/scopes/status.
func cmdOrgsKeysInfo(args []string) int {
	fs := flag.NewFlagSet("orgs keys info", flag.ContinueOnError)
	slug := fs.String("org", "", "org slug (required)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if *slug == "" || fs.NArg() != 1 {
		PrintUsage(os.Stderr, "usage: gregale orgs keys info --org <slug> <key-id>", "orgs")
		return 1
	}
	id := fs.Arg(0)
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	k, err := client.GetOrgAPIKey(context.Background(), *slug, id)
	if err != nil {
		return printErr("Fetch failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(k))
	}
	fmt.Printf("id:         %s\n", k.ID)
	fmt.Printf("prefix:     %s\n", k.Prefix)
	fmt.Printf("label:      %s\n", k.Label)
	fmt.Printf("scopes:     %s\n", strings.Join(k.Scopes, ","))
	fmt.Printf("status:     %s\n", k.Status)
	fmt.Printf("created_at: %s\n", k.CreatedAt)
	if k.ExpiresAt != "" {
		fmt.Printf("expires_at: %s\n", k.ExpiresAt)
	}
	if k.LastUsedAt != "" {
		fmt.Printf("last_used:  %s\n", k.LastUsedAt)
	}
	if k.RotatedFromID != "" {
		fmt.Printf("rotated_from: %s\n", k.RotatedFromID)
	}
	return 0
}

// cmdOrgsKeysRm mirrors cmdKeysRm: 204 No Content on success.
func cmdOrgsKeysRm(args []string) int {
	fs := flag.NewFlagSet("orgs keys rm", flag.ContinueOnError)
	slug := fs.String("org", "", "org slug (required)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if *slug == "" || fs.NArg() != 1 {
		PrintUsage(os.Stderr, "usage: gregale orgs keys rm --org <slug> <key-id>", "orgs")
		return 1
	}
	id := fs.Arg(0)
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	if err := client.RevokeOrgAPIKey(context.Background(), *slug, id); err != nil {
		return printErr("Revoke failed", err)
	}
	PrintOK(osStdout, "Key %s revoked.", id)
	return 0
}

// cmdOrgsKeysRotate mirrors cmdKeysRotate: --label is optional
// (empty = inherit the old label per pkg/api/dto.go:1280-1282).
// The new plaintext is shown ONCE; the old key remains usable
// until old_key_expires_at (default 7 days, override via
// `gregale keys grace-window` — that's account-scoped and
// applies to org-scoped rotations too).
func cmdOrgsKeysRotate(args []string) int {
	fs := flag.NewFlagSet("orgs keys rotate", flag.ContinueOnError)
	slug := fs.String("org", "", "org slug (required)")
	label := fs.String("label", "", "new label (empty = inherit)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if *slug == "" || fs.NArg() != 1 {
		PrintUsage(os.Stderr, "usage: gregale orgs keys rotate --org <slug> [--label <text>] <key-id>", "orgs")
		return 1
	}
	id := fs.Arg(0)
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	resp, err := client.RotateOrgAPIKey(context.Background(), *slug, id, *label)
	if err != nil {
		return printErr("Rotate failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(resp))
	}
	PrintOK(osStdout, "Rotated key. New plaintext (shown ONCE):\n  %s", resp.KeyPlaintext)
	PrintProgress(osStdout, "  new id:        %s", resp.Key.ID)
	PrintProgress(osStdout, "  old key id:    %s", resp.OldKeyID)
	PrintProgress(osStdout, "  old key grace: %s", resp.OldKeyExpiresAt)
	return 0
}

// cmdOrgsInvitationsListAll walks the next_before cursor end-to-end
// (Tier C audit gap, mirrors cmdDeploymentsAll). Server caps the
// page at 200; the SDK helper raises an APIError on the first
// short-page so we surface pagination errors verbatim. --json emits
// a bare slice (no envelope) so jq pipelines stay clean.
func cmdOrgsInvitationsListAll(args []string) int {
	fs := flag.NewFlagSet("orgs invitations list-all", flag.ContinueOnError)
	slug := fs.String("org", "", "org slug (required)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if *slug == "" {
		PrintUsage(os.Stderr, "usage: gregale orgs invitations list-all --org <slug>", "orgs")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	items, err := client.ListOrgInvitationsAll(context.Background(), *slug)
	if err != nil {
		return printErr("Could not list invitations", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(items))
	}
	if len(items) == 0 {
		_, _ = fmt.Fprintln(osStdout, "(no invitations)")
		return 0
	}
	for _, inv := range items {
		fmt.Printf("%-36s %-30s %-12s %-10s %s\n", inv.ID, inv.Email, inv.Role, inv.Status, inv.ExpiresAt)
	}
	return 0
}

// cmdOrgsUpdate implements `gregale orgs update --org <slug>
// [--name <text>] [--plan <free|hobby|pro|scale>]` (PATCH /v1/orgs/{slug},
// issue #190 / ADR-061).
//
// Mirrors cmdAlertUpdate (commands_alerts.go:245): pointer-everything
// request so the wire body carries only the fields the operator
// actually set. fs.Visit would help for *bool, but Name/Plan are
// pointer strings — the empty-string sentinel is already enough to
// distinguish "omit" from "set to empty" because an org name is
// required (handlers_orgs.go:rename_validate) so --name "" would
// always fail closed at the handler anyway.
//
// Authz: name write requires OrgActionManageBilling (owner + billing
// roles); plan change requires OrgActionChangePlan (owner only).
// The server is the source of truth — the CLI never tries to model
// role-gated branching locally.
//
// Output: --json emits the full OrgResponse; human mode prints a
// one-line confirmation echoing only the fields the operator sent
// (the server returns the post-patch state for both, but echoing
// the input keeps the result visible without re-reading).
func cmdOrgsUpdate(args []string) int {
	fs := flag.NewFlagSet("orgs update", flag.ContinueOnError)
	slug := fs.String("org", "", "org slug (required, OrgSlugPattern)")
	name := fs.String("name", "", "new display name (1..120 chars; non-empty)")
	plan := fs.String("plan", "", "new plan (free|hobby|pro|scale)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if !validateOrgsUpdateFlags(slug, name, plan) {
		return 1
	}
	req := api.PatchOrgRequest{}
	if *name != "" {
		s := *name
		req.Name = &s
	}
	if *plan != "" {
		p := *plan
		req.Plan = &p
	}
	if req.Name == nil && req.Plan == nil {
		PrintUsage(os.Stderr, "usage: gregale orgs update --org <slug> [--name <text>] [--plan <free|hobby|pro|scale>]", "orgs")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	resp, err := client.PatchOrg(context.Background(), *slug, req)
	if err != nil {
		return printErr("Update failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(resp))
	}
	PrintOK(osStdout, "Org %s updated.", resp.Slug)
	if req.Name != nil {
		_, _ = fmt.Fprintf(osStdout, "  name:  %s\n", *req.Name)
	}
	if req.Plan != nil {
		_, _ = fmt.Fprintf(osStdout, "  plan:  %s\n", *req.Plan)
	}
	return 0
}

// validateOrgsUpdateFlags enforces the per-field presence + closed
// gates. Returns true on success; otherwise fires printErr with the
// matching error and returns false. Extracted to keep cmdOrgsUpdate
// under the 50-line handler cap (CLAUDE.md).
//
// Order matters: --org required (covers the no-flag "what am I
// updating?" case), then --name / --plan are independently optional.
// The Plan.Valid gate runs against api.Plans (pkg/api/limits.go:34)
// so a typo costs zero latency.
func validateOrgsUpdateFlags(slug, name, plan *string) bool {
	if *slug == "" {
		PrintUsage(os.Stderr, "usage: gregale orgs update --org <slug> [--name <text>] [--plan <free|hobby|pro|scale>]", "orgs")
		return false
	}
	if !orgSlugRe.MatchString(*slug) {
		printErr("Invalid --org", fmt.Errorf("must match OrgSlugPattern (lowercase, dashes, 3..32 chars); got %q", *slug))
		return false
	}
	if *name != "" && (len(*name) > 120) {
		printErr("Invalid --name", fmt.Errorf("--name length %d exceeds 120", len(*name)))
		return false
	}
	if *plan != "" && !api.Plan(*plan).Valid() {
		printErr("Invalid --plan", fmt.Errorf("must be one of free|hobby|pro|scale; got %q", *plan))
		return false
	}
	return true
}
