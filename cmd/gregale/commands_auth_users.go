package main

// adr: 1190 — user and organization administration from a terminal through
// the owner's admin key: the /admin/v1 routes the dashboard's Users and
// Organizations tabs call from the owner's browser. User records go from the
// Auth app to this terminal and never through Gregale.

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const (
	authUsersUsage = "usage: gregale auth users <list|get|impersonate|sign-out|ban|unban|delete> NAME [USER] [--search TEXT] [--page N] [--limit N] [--reason TEXT] [--minutes N] [--for DURATION] [--yes] [--admin-key-stdin]"
	authOrgsUsage  = "usage: gregale auth orgs <list|get> NAME [ORG] [--search TEXT] [--page N] [--limit N] [--admin-key-stdin]"
	// authBanForever is the ban `--for forever` asks for: a hundred years,
	// GoTrue's usual stand-in for a permanent ban.
	authBanForever = "876000h"
	// authLookupPages bounds the pages an email or slug lookup reads.
	authLookupPages = 10
)

var (
	authBanDuration = regexp.MustCompile(`^([1-9][0-9]{0,5})([hd])$`)
	authOrgSlug     = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,62}$`)
)

type authAdminUser struct {
	ID               string   `json:"id"`
	Email            *string  `json:"email"`
	Phone            *string  `json:"phone"`
	CreatedAt        *string  `json:"created_at"`
	LastSignInAt     *string  `json:"last_sign_in_at"`
	EmailConfirmedAt *string  `json:"email_confirmed_at"`
	BannedUntil      *string  `json:"banned_until"`
	Providers        []string `json:"providers"`
	Anonymous        bool     `json:"anonymous"`
}

type authAdminUserPage struct {
	Users   []authAdminUser `json:"users"`
	Total   *int            `json:"total"`
	Page    int             `json:"page"`
	PerPage int             `json:"per_page"`
}

type authAdminOrg struct {
	ID        string `json:"id"`
	Slug      string `json:"slug"`
	Name      string `json:"name"`
	CreatedAt string `json:"created_at"`
	Members   *int   `json:"members,omitempty"`
}

type authAdminOrgPage struct {
	Organizations []authAdminOrg `json:"organizations"`
	Total         int            `json:"total"`
	Page          int            `json:"page"`
	PerPage       int            `json:"per_page"`
}

// authListFlags are the paging flags list subcommands share.
type authListFlags struct {
	search      *string
	page, limit *int
}

func addAuthListFlags(fs *flag.FlagSet, noun string) authListFlags {
	return authListFlags{
		search: fs.String("search", "", "part of an "+noun),
		page:   fs.Int("page", 1, "page to show"),
		limit:  fs.Int("limit", 50, fmt.Sprintf("entries per page (1-%d)", api.AuthAdminPageSize)),
	}
}

func (f authListFlags) query() (string, error) {
	if *f.page < 1 || *f.page > 100000 || *f.limit < 1 || *f.limit > api.AuthAdminPageSize {
		return "", fmt.Errorf("--page must be 1 or more and --limit 1 to %d", api.AuthAdminPageSize)
	}
	if len(*f.search) > 200 || strings.ContainsFunc(*f.search, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		return "", errors.New("--search takes at most 200 printable characters")
	}
	query := url.Values{"page": {strconv.Itoa(*f.page)}, "per_page": {strconv.Itoa(*f.limit)}}
	if *f.search != "" {
		query.Set("search", *f.search)
	}
	return query.Encode(), nil
}

// authAdminTarget reads the owner's admin key and finds the Auth app.
func authAdminTarget(slug string, keyStdin bool) (origin, key string, code int) {
	key, _, err := readRealtimeEndpointSecret("", keyStdin, "admin-key", "Auth admin key: ", 128, true)
	if err != nil || !authAdminKeyFormat.MatchString(key) {
		return "", "", printErr("Invalid admin key", errors.New("expected a key from `gregale auth admin-key create`"))
	}
	client, err := authedClient()
	if err != nil {
		return "", "", printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	app, err := client.GetApp(ctx, slug)
	if err != nil {
		return "", "", printErr("Could not find the Auth app", err)
	}
	if origin, err = authExternalOrigin(app); err != nil {
		return "", "", printErr("Could not resolve the Auth app", err)
	}
	return origin, key, 0
}

// authUserFlags lists the flags each users subcommand takes besides
// --admin-key-stdin.
var authUserFlags = map[string][]string{
	"list": {"search", "page", "limit"}, "get": nil, "impersonate": {"reason", "minutes"}, "sign-out": nil, "ban": {"for"}, "unban": nil, "delete": {"yes", "quiet"},
}

func cmdAuthUsers(args []string) int {
	if len(args) == 0 {
		PrintUsage(os.Stderr, authUsersUsage, "auth")
		return 1
	}
	fs := newFlagSet("auth users "+args[0], flag.ContinueOnError)
	list := addAuthListFlags(fs, "email address or name")
	duration := fs.String("for", "", "how long a ban lasts: hours (24h), days (7d) or forever")
	yes := fs.Bool("yes", false, "delete without the typed confirmation")
	fs.BoolVar(yes, "quiet", false, "delete without the typed confirmation (for scripts)")
	reason := fs.String("reason", "", "why you open a session as the user (recorded; required)")
	minutes := fs.Int("minutes", api.AuthImpersonationDefaultMinutes, fmt.Sprintf("how long the session lasts, 5 to %d minutes", api.AuthImpersonationMaxMinutes))
	keyStdin := fs.Bool("admin-key-stdin", false, "read the admin key from stdin (prompted on a terminal)")
	if err := parseInterspersed(fs, args[1:]); err != nil {
		return 1
	}
	allowed, known := authUserFlags[args[0]]
	want := 2
	if args[0] == "list" {
		want = 1
	}
	if !known || fs.NArg() != want || !api.ValidAppSlug(fs.Arg(0)) {
		PrintUsage(os.Stderr, authUsersUsage, "auth")
		return 1
	}
	if err := authOnlyFlags(fs, allowed); err != nil {
		return printErr("Invalid flags", err)
	}
	ban := ""
	switch args[0] {
	case "ban":
		var err error
		if ban, err = authBanFor(*duration); err != nil {
			return printErr("Invalid ban", err)
		}
	case "unban":
		ban = "none"
	case "impersonate":
		if strings.TrimSpace(*reason) == "" || len(*reason) > 200 {
			return printErr("Reason required", errors.New("give --reason, up to 200 characters, such as a support ticket"))
		}
		if *minutes < 5 || *minutes > api.AuthImpersonationMaxMinutes {
			return printErr("Invalid flags", fmt.Errorf("--minutes must be 5 to %d", api.AuthImpersonationMaxMinutes))
		}
	case "delete":
		if code := requireAutomationConfirmation(*yes, "--yes"); code != 0 {
			return code
		}
		if !*yes && *keyStdin {
			return printErr("Confirmation required", errors.New("--admin-key-stdin uses stdin for the key; confirm the deletion with --yes"))
		}
	}
	query := ""
	if args[0] == "list" {
		var err error
		if query, err = list.query(); err != nil {
			return printErr("Invalid flags", err)
		}
	} else if err := authUserRefValid(fs.Arg(1)); err != nil {
		return printErr("Invalid user", err)
	}
	origin, key, code := authAdminTarget(fs.Arg(0), *keyStdin)
	if code != 0 {
		return code
	}
	if args[0] == "list" {
		return authUsersList(origin, key, query)
	}
	user, err := authResolveUser(origin, key, fs.Arg(1))
	if err != nil {
		return printErr("Could not find the user", err)
	}
	switch args[0] {
	case "get":
		return authUsersGet(origin, key, user)
	case "impersonate":
		return authUsersImpersonate(origin, key, user, strings.TrimSpace(*reason), *minutes)
	case "sign-out":
		return authUsersSignOut(origin, key, user)
	case "delete":
		return authUsersDelete(origin, key, fs.Arg(0), user, *yes)
	default:
		return authUsersBan(origin, key, user, ban)
	}
}

// authOnlyFlags refuses a flag that belongs to another subcommand.
func authOnlyFlags(fs *flag.FlagSet, allowed []string) error {
	var err error
	fs.Visit(func(f *flag.Flag) {
		if err == nil && f.Name != "admin-key-stdin" && !slices.Contains(allowed, f.Name) {
			err = fmt.Errorf("%s does not take --%s", fs.Name(), f.Name)
		}
	})
	return err
}

// authBanFor turns --for into GoTrue's ban duration in hours.
func authBanFor(value string) (string, error) {
	if value == "forever" {
		return authBanForever, nil
	}
	match := authBanDuration.FindStringSubmatch(value)
	if match == nil {
		return "", errors.New("--for takes hours (24h), days (7d) or forever")
	}
	hours, _ := strconv.Atoi(match[1])
	if match[2] == "d" {
		hours *= 24
	}
	if hours > 999999 {
		return "", errors.New("--for is at most 999999h; use forever")
	}
	return strconv.Itoa(hours) + "h", nil
}

// authUserRefValid accepts a user ID or an email address.
func authUserRefValid(ref string) error {
	if authImportUUID.MatchString(ref) || (len(ref) <= 320 && authImportEmail.MatchString(ref)) {
		return nil
	}
	return errors.New("name a user by ID or email address")
}

// authResolveUser returns the ID of the user a reference names. GoTrue's
// search matches part of an address, so an email lookup keeps only exact
// matches; an SSO account may share its address with another account.
func authResolveUser(origin, key, ref string) (authAdminUser, error) {
	if authImportUUID.MatchString(ref) {
		return authAdminUser{ID: strings.ToLower(ref)}, nil
	}
	email := strings.ToLower(ref)
	var found []authAdminUser
	for page := 1; page <= authLookupPages; page++ {
		query := url.Values{"search": {email}, "page": {strconv.Itoa(page)}, "per_page": {strconv.Itoa(api.AuthAdminPageSize)}}
		var users authAdminUserPage
		if err := authAdminCall(http.MethodGet, origin, key, "/admin/v1/users?"+query.Encode(), nil, &users); err != nil {
			return authAdminUser{}, err
		}
		for _, user := range users.Users {
			if user.Email != nil && strings.EqualFold(*user.Email, email) {
				found = append(found, user)
			}
		}
		if len(users.Users) < api.AuthAdminPageSize {
			break
		}
	}
	switch len(found) {
	case 0:
		return authAdminUser{}, fmt.Errorf("no user has the email address %s", email)
	case 1:
		return found[0], nil
	}
	ids := make([]string, len(found))
	for i, user := range found {
		ids[i] = user.ID
	}
	return authAdminUser{}, fmt.Errorf("%d users have the email address %s; name one by ID: %s", len(found), email, strings.Join(ids, ", "))
}

func authUsersList(origin, key, query string) int {
	var page authAdminUserPage
	if err := authAdminCall(http.MethodGet, origin, key, "/admin/v1/users?"+query, nil, &page); err != nil {
		return printErr("Could not list users", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(page))
	}
	if len(page.Users) == 0 {
		_, _ = fmt.Fprintln(osStdout, "No users match.")
		return 0
	}
	now := time.Now()
	for _, user := range page.Users {
		_, _ = fmt.Fprintf(osStdout, "%s  %-32s  %-24s  last sign-in %s\n", user.ID, authUserName(user), authUserStatus(user, now), authOr(user.LastSignInAt, "never"))
	}
	if page.Total != nil && page.Page*page.PerPage < *page.Total {
		_, _ = fmt.Fprintf(osStdout, "Page %d of %d users; next: --page %d\n", page.Page, *page.Total, page.Page+1)
	}
	return 0
}

// authUsersImpersonate opens a time-limited session as the user and prints
// the link that hands it to the app. Anyone holding the link acts as the
// user until the session ends.
func authUsersImpersonate(origin, key string, ref authAdminUser, reason string, minutes int) int {
	var started struct {
		ID        string `json:"id"`
		SessionID string `json:"session_id"`
		ExpiresAt string `json:"expires_at"`
		URL       string `json:"url"`
	}
	if err := authAdminCall(http.MethodPost, origin, key, "/admin/v1/users/"+ref.ID+"/impersonate", map[string]any{"reason": reason, "minutes": minutes}, &started); err != nil {
		return printErr("Could not open a session as the user", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(started))
	}
	PrintOK(osStdout, "Session %s opened as %s until %s", started.ID, authUserLabel(ref), started.ExpiresAt)
	_, _ = fmt.Fprintf(osStdout, "Open this link in a private window; it signs in as the user, so do not share it:\n%s\n", started.URL)
	_, _ = fmt.Fprintf(osStdout, "The user's password, email, second factors, linked sign-ins and account cannot be changed from it.\nEnd it early with: gregale auth impersonations end NAME %s\n", started.ID)
	return 0
}

func authUsersGet(origin, key string, ref authAdminUser) int {
	var detail struct {
		User           authAdminUser `json:"user"`
		ActiveSessions int           `json:"active_sessions"`
		Organizations  []struct {
			ID   string `json:"id"`
			Slug string `json:"slug"`
			Name string `json:"name"`
			Role string `json:"role"`
		} `json:"organizations"`
	}
	if err := authAdminCall(http.MethodGet, origin, key, "/admin/v1/users/"+ref.ID, nil, &detail); err != nil {
		return printErr("Could not read the user", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(detail))
	}
	user := detail.User
	email := authOr(user.Email, "-")
	if user.Email != nil && *user.Email != "" {
		email += map[bool]string{true: " (confirmed)", false: " (unconfirmed)"}[user.EmailConfirmedAt != nil]
	}
	orgs := make([]string, len(detail.Organizations))
	for i, org := range detail.Organizations {
		orgs[i] = org.Slug + " (" + org.Role + ")"
	}
	rows := [][2]string{
		{"email", email}, {"phone", authOr(user.Phone, "-")}, {"status", authUserStatus(user, time.Now())},
		{"sign-in", strings.Join(user.Providers, ", ")}, {"created", authOr(user.CreatedAt, "-")},
		{"last sign-in", authOr(user.LastSignInAt, "never")}, {"sessions", fmt.Sprintf("%d active", detail.ActiveSessions)},
		{"organizations", strings.Join(orgs, ", ")},
	}
	_, _ = fmt.Fprintf(osStdout, "User %s\n", user.ID)
	for _, row := range rows {
		if row[1] == "" {
			row[1] = "-"
		}
		_, _ = fmt.Fprintf(osStdout, "  %-14s %s\n", row[0], row[1])
	}
	return 0
}

func authUsersSignOut(origin, key string, user authAdminUser) int {
	var out struct {
		SessionsEnded int `json:"sessions_ended"`
	}
	if err := authAdminCall(http.MethodPost, origin, key, "/admin/v1/users/"+user.ID+"/sign-out", map[string]any{}, &out); err != nil {
		return printErr("Could not sign the user out", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(map[string]any{"id": user.ID, "sessions_ended": out.SessionsEnded}))
	}
	PrintOK(osStdout, "Signed out %s everywhere: %d sessions ended", authUserLabel(user), out.SessionsEnded)
	authIssuedTokensNote()
	return 0
}

func authUsersBan(origin, key string, user authAdminUser, duration string) int {
	var out struct {
		User authAdminUser `json:"user"`
	}
	if err := authAdminCall(http.MethodPost, origin, key, "/admin/v1/users/"+user.ID+"/ban", map[string]string{"duration": duration}, &out); err != nil {
		return printErr("Could not change the ban", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	if out.User.ID != "" {
		user = out.User
	}
	if duration == "none" {
		PrintOK(osStdout, "Lifted the ban on %s; they can sign in again", authUserLabel(user))
		return 0
	}
	PrintOK(osStdout, "Banned %s until %s and ended their sessions", authUserLabel(user), authOr(user.BannedUntil, "-"))
	authIssuedTokensNote()
	return 0
}

func authUsersDelete(origin, key, app string, user authAdminUser, confirmed bool) int {
	if !confirmed {
		_, _ = fmt.Fprintf(osStderr, "About to delete user %s of %s with their sessions and sign-in methods. Organizations whose only member they are go with them.\n", authUserLabel(user), app)
		if !requireTyped("delete user") {
			return 1
		}
	}
	status, raw, err := authAdminDo(http.MethodDelete, origin, key, "/admin/v1/users/"+user.ID, nil)
	if err != nil {
		return printErr("Could not delete the user", err)
	}
	if status == http.StatusConflict {
		var refusal struct {
			Code          string   `json:"code"`
			Organizations []string `json:"organizations"`
		}
		if json.Unmarshal(raw, &refusal) == nil && refusal.Code == "last_owner" {
			return printErr("Could not delete the user", fmt.Errorf("they are the last owner of %s, which have other members; make another member an owner first", strings.Join(refusal.Organizations, ", ")))
		}
	}
	if status != http.StatusOK {
		return printErr("Could not delete the user", authAdminError(status, raw))
	}
	var out struct {
		OrganizationsDeleted int `json:"organizations_deleted"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return printErr("Could not delete the user", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(map[string]any{"id": user.ID, "deleted": true, "organizations_deleted": out.OrganizationsDeleted}))
	}
	PrintOK(osStdout, "Deleted user %s", authUserLabel(user))
	if out.OrganizationsDeleted > 0 {
		_, _ = fmt.Fprintf(osStdout, "  %d organizations they alone belonged to were deleted too\n", out.OrganizationsDeleted)
	}
	authIssuedTokensNote()
	return 0
}

// authIssuedTokensNote says what a sign-out, ban or deletion cannot stop.
func authIssuedTokensNote() {
	_, _ = fmt.Fprintf(osStderr, "Access tokens already issued stay valid for up to %d minutes; authorize() checks refuse them from the next request.\n", api.AuthAccessTokenTTLSeconds/60)
}

func authUserName(user authAdminUser) string {
	switch {
	case user.Email != nil && *user.Email != "":
		return *user.Email
	case user.Phone != nil && *user.Phone != "":
		return *user.Phone
	case user.Anonymous:
		return "(anonymous)"
	}
	return "-"
}

func authUserLabel(user authAdminUser) string {
	if name := authUserName(user); name != "-" && name != "(anonymous)" {
		return user.ID + " (" + name + ")"
	}
	return user.ID
}

func authUserStatus(user authAdminUser, now time.Time) string {
	if user.BannedUntil != nil {
		if until, err := time.Parse(time.RFC3339Nano, *user.BannedUntil); err == nil && until.After(now) {
			return "banned until " + until.UTC().Format("2006-01-02")
		}
	}
	switch {
	case user.Anonymous:
		return "anonymous"
	case user.Email != nil && *user.Email != "" && user.EmailConfirmedAt == nil:
		return "unconfirmed"
	}
	return "active"
}

func authOr(value *string, fallback string) string {
	if value == nil || *value == "" {
		return fallback
	}
	return *value
}

func cmdAuthOrgs(args []string) int {
	if len(args) == 0 {
		PrintUsage(os.Stderr, authOrgsUsage, "auth")
		return 1
	}
	fs := newFlagSet("auth orgs "+args[0], flag.ContinueOnError)
	list := addAuthListFlags(fs, "organization's slug or name")
	keyStdin := fs.Bool("admin-key-stdin", false, "read the admin key from stdin (prompted on a terminal)")
	if err := parseInterspersed(fs, args[1:]); err != nil {
		return 1
	}
	want := map[string]int{"list": 1, "get": 2}[args[0]]
	if want == 0 || fs.NArg() != want || !api.ValidAppSlug(fs.Arg(0)) || (want == 2 && !authImportUUID.MatchString(fs.Arg(1)) && !authOrgSlug.MatchString(fs.Arg(1))) {
		PrintUsage(os.Stderr, authOrgsUsage, "auth")
		return 1
	}
	if args[0] == "get" {
		if err := authOnlyFlags(fs, nil); err != nil {
			return printErr("Invalid flags", err)
		}
	}
	query, err := list.query()
	if err != nil {
		return printErr("Invalid flags", err)
	}
	origin, key, code := authAdminTarget(fs.Arg(0), *keyStdin)
	if code != 0 {
		return code
	}
	if args[0] == "list" {
		return authOrgsList(origin, key, query)
	}
	id, err := authResolveOrg(origin, key, fs.Arg(1))
	if err != nil {
		return printErr("Could not find the organization", err)
	}
	return authOrgsGet(origin, key, id)
}

// authResolveOrg returns the ID of the organization an ID or slug names.
func authResolveOrg(origin, key, ref string) (string, error) {
	if authImportUUID.MatchString(ref) {
		return strings.ToLower(ref), nil
	}
	for page := 1; page <= authLookupPages; page++ {
		query := url.Values{"search": {ref}, "page": {strconv.Itoa(page)}, "per_page": {strconv.Itoa(api.AuthAdminPageSize)}}
		var orgs authAdminOrgPage
		if err := authAdminCall(http.MethodGet, origin, key, "/admin/v1/orgs?"+query.Encode(), nil, &orgs); err != nil {
			return "", err
		}
		for _, org := range orgs.Organizations {
			if org.Slug == ref {
				return org.ID, nil
			}
		}
		if len(orgs.Organizations) < api.AuthAdminPageSize {
			break
		}
	}
	return "", fmt.Errorf("no organization has the slug %s", ref)
}

func authOrgsList(origin, key, query string) int {
	var page authAdminOrgPage
	if err := authAdminCall(http.MethodGet, origin, key, "/admin/v1/orgs?"+query, nil, &page); err != nil {
		return printErr("Could not list organizations", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(page))
	}
	if len(page.Organizations) == 0 {
		_, _ = fmt.Fprintln(osStdout, "No organizations match.")
		return 0
	}
	for _, org := range page.Organizations {
		members := 0
		if org.Members != nil {
			members = *org.Members
		}
		_, _ = fmt.Fprintf(osStdout, "%s  %-24s  %4d members  %s\n", org.ID, org.Slug, members, org.Name)
	}
	if page.Page*page.PerPage < page.Total {
		_, _ = fmt.Fprintf(osStdout, "Page %d of %d organizations; next: --page %d\n", page.Page, page.Total, page.Page+1)
	}
	return 0
}

func authOrgsGet(origin, key, id string) int {
	var detail struct {
		Organization authAdminOrg `json:"organization"`
		Members      []struct {
			UserID    string  `json:"user_id"`
			Email     *string `json:"email"`
			Role      string  `json:"role"`
			CreatedAt string  `json:"created_at"`
		} `json:"members"`
		Invitations []struct {
			ID        string `json:"id"`
			Email     string `json:"email"`
			Role      string `json:"role"`
			ExpiresAt string `json:"expires_at"`
		} `json:"invitations"`
	}
	if err := authAdminCall(http.MethodGet, origin, key, "/admin/v1/orgs/"+id, nil, &detail); err != nil {
		return printErr("Could not read the organization", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(detail))
	}
	org := detail.Organization
	_, _ = fmt.Fprintf(osStdout, "Organization %s (%s)\n  id       %s\n  created  %s\n", org.Slug, org.Name, org.ID, org.CreatedAt)
	_, _ = fmt.Fprintf(osStdout, "Members (%d):\n", len(detail.Members))
	for _, member := range detail.Members {
		_, _ = fmt.Fprintf(osStdout, "  %s  %-10s  %s\n", member.UserID, member.Role, authOr(member.Email, "-"))
	}
	if len(detail.Invitations) > 0 {
		_, _ = fmt.Fprintf(osStdout, "Pending invitations (%d):\n", len(detail.Invitations))
		for _, invitation := range detail.Invitations {
			_, _ = fmt.Fprintf(osStdout, "  %-10s  %s  expires %s\n", invitation.Role, invitation.Email, invitation.ExpiresAt)
		}
	}
	return 0
}
