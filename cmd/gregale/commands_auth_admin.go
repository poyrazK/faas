package main

// adr: 1190 — owner-held admin keys and user import for application Auth.

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const authAdminKeyPrefix = "AUTH_ADMIN_KEY_"

// authAdminKeyScopes are the scopes an Auth admin key may hold (the
// template's adminScopes), besides "all".
var authAdminKeyScopes = []string{"users:read", "users:write", "impersonate", "sso", "oauth-clients", "service-accounts", "emails"}

const authAdminKeyUsage = "usage: gregale auth admin-key <create|list|revoke> NAME [ID] [--scopes all|SCOPES] [--expires-in DAYS | --no-expiry]"

var (
	authAdminKeyID     = regexp.MustCompile(`^[0-9A-F]{8}$`)
	authAdminKeyName   = regexp.MustCompile(`^AUTH_ADMIN_KEY_[0-9A-F]{8}$`)
	authAdminKeyFormat = regexp.MustCompile(`^gaak_[A-Za-z0-9_-]{43}$`)
	// authAdminHTTPClient talks to the Auth app itself; it never follows a
	// redirect, so the admin key cannot be forwarded to another host.
	authAdminHTTPClient = &http.Client{Timeout: 2 * time.Minute, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	// authRestartRetryWindow and authRestartRetryPause pace restarts refused
	// because one is already running; tests shorten them.
	authRestartRetryWindow = time.Minute
	authRestartRetryPause  = 2 * time.Second
)

func cmdAuthAdminKey(args []string) int {
	if len(args) == 0 {
		PrintUsage(os.Stderr, authAdminKeyUsage, "auth")
		return 1
	}
	fs := newFlagSet("auth admin-key "+args[0], flag.ContinueOnError)
	scope := fs.String("scope", api.DefaultEnvScope, "environment scope")
	scopes := fs.String("scopes", "all", "what the key may do: all, or a comma-separated list of "+strings.Join(authAdminKeyScopes, ", "))
	expiresIn := fs.Int("expires-in", api.AuthAdminKeyDefaultDays, fmt.Sprintf("days until the key expires (1-%d)", api.AuthAdminKeyMaxDays))
	noExpiry := fs.Bool("no-expiry", false, "the key lasts until revoked")
	if err := parseInterspersed(fs, args[1:]); err != nil {
		return 1
	}
	want := map[string]int{"create": 1, "list": 1, "revoke": 2}[args[0]]
	if want == 0 || fs.NArg() != want || !api.ValidAppSlug(fs.Arg(0)) || (args[0] == "revoke" && !authAdminKeyID.MatchString(fs.Arg(1))) {
		PrintUsage(os.Stderr, authAdminKeyUsage, "auth")
		return 1
	}
	given := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { given[f.Name] = true })
	if args[0] != "create" && (given["scopes"] || given["expires-in"] || given["no-expiry"]) {
		return printErr("Invalid flags", errors.New("--scopes, --expires-in and --no-expiry apply to create only"))
	}
	grant, err := authAdminKeyGrant(*scopes, *expiresIn, given["expires-in"], *noExpiry, time.Now())
	if err != nil {
		return printErr("Invalid admin key settings", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	switch args[0] {
	case "create":
		return authAdminKeyCreate(ctx, client, fs.Arg(0), *scope, grant)
	case "list":
		return authAdminKeyList(ctx, client, fs.Arg(0), *scope)
	default:
		return authAdminKeyRevoke(ctx, client, fs.Arg(0), fs.Arg(1), *scope)
	}
}

// authAdminKeyGrantSpec is what a new admin key may do and until when.
// suffix is what the app stores after the key's digest.
type authAdminKeyGrantSpec struct {
	Scopes    []string
	ExpiresAt *time.Time
}

func (g authAdminKeyGrantSpec) suffix() string {
	out := ""
	if len(g.Scopes) > 0 && g.Scopes[0] != "all" {
		out += ";scopes=" + strings.Join(g.Scopes, ",")
	}
	if g.ExpiresAt != nil {
		out += ";expires=" + strconv.FormatInt(g.ExpiresAt.Unix(), 10)
	}
	return out
}

func authAdminKeyGrant(scopes string, days int, daysGiven, noExpiry bool, now time.Time) (authAdminKeyGrantSpec, error) {
	var g authAdminKeyGrantSpec
	if scopes == "" || scopes == "all" {
		g.Scopes = []string{"all"}
	} else {
		seen := map[string]bool{}
		for _, s := range strings.Split(scopes, ",") {
			s = strings.TrimSpace(s)
			if !slices.Contains(authAdminKeyScopes, s) {
				return g, fmt.Errorf("unknown scope %q; use all or %s", s, strings.Join(authAdminKeyScopes, ", "))
			}
			if !seen[s] {
				seen[s] = true
				g.Scopes = append(g.Scopes, s)
			}
		}
		sort.Strings(g.Scopes)
	}
	if noExpiry && daysGiven {
		return g, errors.New("use --expires-in or --no-expiry, not both")
	}
	if !noExpiry {
		if days < 1 || days > api.AuthAdminKeyMaxDays {
			return g, fmt.Errorf("--expires-in must be 1 to %d days; got %d", api.AuthAdminKeyMaxDays, days)
		}
		at := now.Add(time.Duration(days) * 24 * time.Hour).UTC().Truncate(time.Second)
		g.ExpiresAt = &at
	}
	return g, nil
}

// authAdminKeyCreate prints the key once; the app stores only its SHA-256,
// followed by the key's scopes and expiry.
func authAdminKeyCreate(ctx context.Context, client *api.Client, app, scope string, grant authAdminKeyGrantSpec) int {
	if _, err := client.GetApp(ctx, app); err != nil {
		return printErr("Could not find the Auth app", err)
	}
	var raw [32]byte
	var id [4]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return printErr("Could not generate an admin key", err)
	}
	if _, err := rand.Read(id[:]); err != nil {
		return printErr("Could not generate an admin key", err)
	}
	key := "gaak_" + base64.RawURLEncoding.EncodeToString(raw[:])
	sum := sha256.Sum256([]byte(key))
	keyID := strings.ToUpper(hex.EncodeToString(id[:]))
	if err := client.SetSecretWithScope(ctx, app, authAdminKeyPrefix+keyID, hex.EncodeToString(sum[:])+grant.suffix(), scope); err != nil {
		return printErr("Could not store the admin key", err)
	}
	restartAuthApp(ctx, client, app)
	dashboard := dashboardBaseURL(client.BaseURL()) + "/dashboard/apps/" + app + "/auth"
	expires := "never"
	var expiresAt *string
	if grant.ExpiresAt != nil {
		expires = grant.ExpiresAt.Format(time.RFC3339)
		expiresAt = &expires
	}
	if jsonOutput {
		return jsonOut(writeJSON(map[string]any{"id": keyID, "key": key, "app": app, "dashboard_url": dashboard, "scopes": grant.Scopes, "expires_at": expiresAt}))
	}
	PrintOK(osStdout, "Created admin key %s for %s", keyID, app)
	_, _ = fmt.Fprintf(osStdout, "  key:       %s\n  scopes:    %s\n  expires:   %s\n  dashboard: %s\n", key, strings.Join(grant.Scopes, ", "), expires, dashboard)
	PrintWarn(osStderr, "the key is shown once and only its hash is stored; keep it in a password manager")
	return 0
}

func authAdminKeyList(ctx context.Context, client *api.Client, app, scope string) int {
	list, err := client.ListSecretsWithScope(ctx, app, scope)
	if err != nil {
		return printErr("Could not list admin keys", err)
	}
	type keyRow struct {
		ID        string   `json:"id"`
		CreatedAt string   `json:"created_at"`
		Scopes    []string `json:"scopes,omitempty"`
		ExpiresAt *string  `json:"expires_at"`
		// Unknown says why scopes are missing: not_loaded, rate_limited or
		// unreachable.
		Unknown string `json:"scopes_unknown,omitempty"`
	}
	// The running app reports each key's scopes and expiry (secrets are
	// write-only). Apps on older templates list them in /settings/v1;
	// current ones answer only by ID, so a key missing there is asked for
	// by ID. Lookups share
	// one deadline and stop at the first one the app refuses or cannot
	// answer, so a slow or limiting app costs at most one timeout.
	running := map[string]authAdminKeySetting{}
	found, err := client.GetApp(ctx, app)
	reason := "unreachable"
	if err == nil {
		if settings, err := fetchAuthSettings(ctx, found); err == nil {
			reason = ""
			for _, k := range settings.AdminKeys {
				running[k.ID] = k
			}
		}
	}
	lookups, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	rows := []keyRow{}
	for _, secret := range list.Secrets {
		if !authAdminKeyName.MatchString(secret.Key) {
			continue
		}
		id := strings.TrimPrefix(secret.Key, authAdminKeyPrefix)
		row := keyRow{ID: id, CreatedAt: secret.CreatedAt}
		k, listed := running[id]
		if !listed && reason == "" {
			switch got, result := fetchAuthAdminKey(lookups, found, id); result {
			case authAdminKeyFound:
				k, listed = got, true
			case authAdminKeyMissing:
				row.Unknown = "not_loaded"
			case authAdminKeyLimited:
				reason = "rate_limited"
			default:
				reason = "unreachable"
			}
		}
		if listed {
			row.Scopes, row.ExpiresAt = k.Scopes, k.ExpiresAt
		} else if row.Unknown == "" {
			row.Unknown = reason
		}
		rows = append(rows, row)
	}
	if jsonOutput {
		return jsonOut(writeJSON(map[string]any{"admin_keys": rows}))
	}
	if len(rows) == 0 {
		_, _ = fmt.Fprintf(osStdout, "No admin keys for %s. Create one with `gregale auth admin-key create %s`.\n", app, app)
		return 0
	}
	for _, row := range rows {
		scopes, expires := map[string]string{
			"not_loaded":   "unknown (restart the app to load it)",
			"rate_limited": "unknown (the app limits key lookups; retry within the hour)",
			"unreachable":  "unknown (could not reach the app)",
		}[row.Unknown], "unknown"
		if row.Scopes != nil {
			scopes, expires = strings.Join(row.Scopes, ", "), "never"
			if row.ExpiresAt != nil {
				expires = *row.ExpiresAt
			}
		}
		_, _ = fmt.Fprintf(osStdout, "%s  created %s  scopes %s  expires %s\n", row.ID, row.CreatedAt, scopes, expires)
	}
	return 0
}

func authAdminKeyRevoke(ctx context.Context, client *api.Client, app, id, scope string) int {
	if err := client.UnsetSecretWithScope(ctx, app, authAdminKeyPrefix+id, scope); err != nil {
		return printErr("Could not revoke the admin key", err)
	}
	restartAuthApp(ctx, client, app)
	PrintOK(osStdout, "Revoked admin key %s for %s", id, app)
	return 0
}

// restartAuthApp loads changed keys into running instances.
// A restart already in progress may have read the configuration before this
// change, so a refused restart is retried until it is accepted.
func restartAuthApp(ctx context.Context, client *api.Client, app string) {
	deadline := time.Now().Add(authRestartRetryWindow)
	for {
		_, err := client.RestartAppFresh(ctx, app)
		var problem *api.APIError
		if err == nil {
			return
		}
		if !errors.As(err, &problem) || problem.Problem.Status != http.StatusConflict || time.Now().After(deadline) {
			PrintWarn(osStderr, "restarting %s failed (%v); run `gregale app restart %s --fresh` to apply the change", app, err, app)
			return
		}
		select {
		case <-ctx.Done():
			PrintWarn(osStderr, "a restart of %s was already in progress; run `gregale app restart %s --fresh` once it finishes to apply the change", app, app)
			return
		case <-time.After(authRestartRetryPause):
		}
	}
}

type authImportResult struct {
	Created int                  `json:"created"`
	Skipped []authImportSkipped  `json:"skipped"`
	Records int                  `json:"records"`
	DryRun  bool                 `json:"dry_run,omitempty"`
	Failed  *authImportBatchFail `json:"failed,omitempty"`
}

type authImportSkipped struct {
	Record int    `json:"record"`
	Code   string `json:"code"`
}

type authImportBatchFail struct {
	FirstRecord int    `json:"first_record"`
	Error       string `json:"error"`
}

func cmdAuthImport(args []string) int {
	fs := newFlagSet("auth import", flag.ContinueOnError)
	file := fs.String("file", "", "JSON array or JSON Lines file of users")
	dryRun := fs.Bool("dry-run", false, "validate the file without importing")
	keyStdin := fs.Bool("admin-key-stdin", false, "read the admin key from stdin (prompted on a terminal)")
	if err := parseInterspersed(fs, args); err != nil {
		return 1
	}
	if fs.NArg() != 1 || !api.ValidAppSlug(fs.Arg(0)) || *file == "" || *file == "-" {
		PrintUsage(os.Stderr, "usage: gregale auth import NAME --file USERS.json [--dry-run] [--admin-key-stdin]", "auth")
		return 1
	}
	records, err := readAuthImportFile(*file)
	if err != nil {
		return printErr("Invalid import file", err)
	}
	result := authImportResult{Records: len(records), DryRun: *dryRun, Skipped: []authImportSkipped{}}
	valid := make([]int, 0, len(records))
	seen := map[string]bool{}
	for i, record := range records {
		if code := validateAuthImportRecord(record, seen); code != "" {
			result.Skipped = append(result.Skipped, authImportSkipped{Record: i + 1, Code: code})
			continue
		}
		valid = append(valid, i)
	}
	if !*dryRun && len(valid) > 0 {
		key, _, err := readRealtimeEndpointSecret("", *keyStdin, "admin-key", "Auth admin key: ", 128, true)
		if err != nil {
			return printErr("Could not read the admin key", err)
		}
		if !authAdminKeyFormat.MatchString(key) {
			return printErr("Invalid admin key", errors.New("expected a key from `gregale auth admin-key create`"))
		}
		client, err := authedClient()
		if err != nil {
			return printErr("Not logged in", err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		app, err := client.GetApp(ctx, fs.Arg(0))
		cancel()
		if err != nil {
			return printErr("Could not find the Auth app", err)
		}
		origin, err := authExternalOrigin(app)
		if err != nil {
			return printErr("Could not resolve the Auth app", err)
		}
		importAuthBatches(origin, key, records, valid, &result)
	}
	return renderAuthImport(result)
}

// importAuthBatches stops at the first failed request. Re-running the same
// file is safe: users created earlier come back as email_exists.
func importAuthBatches(origin, key string, records []json.RawMessage, valid []int, result *authImportResult) {
	for start := 0; start < len(valid); start += api.AuthImportBatch {
		batch := valid[start:min(start+api.AuthImportBatch, len(valid))]
		users := make([]json.RawMessage, len(batch))
		for i, index := range batch {
			users[i] = records[index]
		}
		var response struct {
			Created int `json:"created"`
			Skipped []struct {
				Index int    `json:"index"`
				Code  string `json:"code"`
			} `json:"skipped"`
		}
		if err := authAdminRequest(origin, key, "/admin/v1/users/import", map[string]any{"users": users}, &response); err != nil {
			result.Failed = &authImportBatchFail{FirstRecord: batch[0] + 1, Error: err.Error()}
			return
		}
		result.Created += response.Created
		for _, skipped := range response.Skipped {
			if skipped.Index >= 0 && skipped.Index < len(batch) {
				result.Skipped = append(result.Skipped, authImportSkipped{Record: batch[skipped.Index] + 1, Code: skipped.Code})
			}
		}
	}
}

func authAdminRequest(origin, key, path string, body, out any) error {
	return authAdminCall(http.MethodPost, origin, key, path, body, out)
}

// authAdminCall sends one /admin/v1 request; body is nil for a GET.
func authAdminCall(method, origin, key, path string, body, out any) error {
	status, raw, err := authAdminDo(method, origin, key, path, body)
	if err != nil {
		return err
	}
	if status != http.StatusOK && status != http.StatusCreated {
		return authAdminError(status, raw)
	}
	return json.Unmarshal(raw, out)
}

// authAdminDo returns the Auth app's status and body for one /admin/v1
// request, so a caller can read the details of a refusal.
func authAdminDo(method, origin, key, path string, body any) (int, []byte, error) {
	var reader io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return 0, nil, err
		}
		reader = bytes.NewReader(payload)
	}
	request, err := http.NewRequest(method, origin+path, reader)
	if err != nil {
		return 0, nil, err
	}
	request.Header.Set("Authorization", "Bearer "+key)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := authAdminHTTPClient.Do(request)
	if err != nil {
		return 0, nil, fmt.Errorf("reach the Auth app: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(response.Body, api.AuthAdminBodyBytes))
	if err != nil {
		return 0, nil, fmt.Errorf("read the Auth app response: %w", err)
	}
	return response.StatusCode, raw, nil
}

func authAdminError(status int, raw []byte) error {
	var problem struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(raw, &problem)
	if problem.Code == "" {
		problem.Code = http.StatusText(status)
	}
	return fmt.Errorf("the Auth app answered %d %s", status, problem.Code)
}

func renderAuthImport(result authImportResult) int {
	sort.Slice(result.Skipped, func(i, j int) bool { return result.Skipped[i].Record < result.Skipped[j].Record })
	failed := result.Failed != nil
	for _, skipped := range result.Skipped {
		if skipped.Code != "email_exists" {
			failed = true
		}
	}
	if jsonOutput {
		if code := jsonOut(writeJSON(result)); code != 0 {
			return code
		}
	} else {
		if result.DryRun {
			PrintOK(osStdout, "Checked %d records: %d would be imported", result.Records, result.Records-len(result.Skipped))
		} else {
			PrintOK(osStdout, "Imported %d of %d users", result.Created, result.Records)
		}
		for _, skipped := range result.Skipped {
			_, _ = fmt.Fprintf(osStdout, "  record %d: %s\n", skipped.Record, skipped.Code)
		}
		if result.Failed != nil {
			PrintWarn(osStderr, "stopped at record %d: %s; re-run the same file to continue", result.Failed.FirstRecord, result.Failed.Error)
		}
	}
	if failed {
		return 1
	}
	return 0
}

// readAuthImportFile accepts a JSON array or JSON Lines.
func readAuthImportFile(path string) ([]json.RawMessage, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > api.AuthImportFileMaxBytes {
		return nil, fmt.Errorf("%s must be a regular file of at most %d bytes", path, api.AuthImportFileMaxBytes)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var records []json.RawMessage
	if trimmed := bytes.TrimSpace(body); len(trimmed) > 0 && trimmed[0] == '[' {
		if err := json.Unmarshal(trimmed, &records); err != nil {
			return nil, fmt.Errorf("parse JSON array: %w", err)
		}
	} else {
		scanner := bufio.NewScanner(bytes.NewReader(body))
		scanner.Buffer(make([]byte, 0, 64<<10), 1<<20)
		for line := 1; scanner.Scan(); line++ {
			text := bytes.TrimSpace(scanner.Bytes())
			if len(text) == 0 {
				continue
			}
			if !json.Valid(text) {
				return nil, fmt.Errorf("line %d is not valid JSON", line)
			}
			records = append(records, append(json.RawMessage(nil), text...))
		}
		if err := scanner.Err(); err != nil {
			return nil, err
		}
	}
	if len(records) == 0 {
		return nil, errors.New("the file holds no users")
	}
	return records, nil
}

// supportedAuthPasswordHash accepts the hash families GoTrue v2.197.0 verifies.
func supportedAuthPasswordHash(hash string) bool {
	for _, prefix := range []string{"$2a$", "$2b$", "$2y$", "$argon2", "$fbscrypt$"} {
		if strings.HasPrefix(hash, prefix) {
			return true
		}
	}
	return false
}

var (
	authImportEmail = regexp.MustCompile(`^[^\s@]+@[^\s@]+$`)
	authImportUUID  = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
)

// validateAuthImportRecord mirrors the Auth app's checks so --dry-run finds
// problems before any user is created. It returns "" for a valid record.
func validateAuthImportRecord(raw json.RawMessage, seen map[string]bool) string {
	var record map[string]json.RawMessage
	if err := json.Unmarshal(raw, &record); err != nil || record == nil {
		return "invalid_record"
	}
	for field := range record {
		switch field {
		case "id", "email", "password_hash", "email_confirmed", "user_metadata", "app_metadata":
		default:
			return "unknown_field"
		}
	}
	var email, id, hash string
	if json.Unmarshal(record["email"], &email) != nil || len(email) > 320 || !authImportEmail.MatchString(email) {
		return "invalid_email"
	}
	if value, ok := record["id"]; ok && (json.Unmarshal(value, &id) != nil || !authImportUUID.MatchString(id)) {
		return "invalid_id"
	}
	if value, ok := record["password_hash"]; ok {
		if json.Unmarshal(value, &hash) != nil || len(hash) > 512 || !supportedAuthPasswordHash(hash) {
			return "unsupported_password_hash"
		}
	}
	if value, ok := record["email_confirmed"]; ok {
		var confirmed bool
		if json.Unmarshal(value, &confirmed) != nil {
			return "invalid_email_confirmed"
		}
	}
	for _, field := range []string{"user_metadata", "app_metadata"} {
		if value, ok := record[field]; ok {
			var object map[string]any
			if json.Unmarshal(value, &object) != nil || object == nil || len(value) > 16384 {
				return "invalid_" + field
			}
		}
	}
	keys := []string{"email:" + strings.ToLower(email)}
	if id != "" {
		keys = append(keys, "id:"+strings.ToLower(id))
	}
	for _, key := range keys {
		if seen[key] {
			return "duplicate_in_file"
		}
	}
	for _, key := range keys {
		seen[key] = true
	}
	return ""
}

type authActivityEntry struct {
	ID        string  `json:"id"`
	CreatedAt string  `json:"created_at"`
	Action    string  `json:"action"`
	Actor     string  `json:"actor"`
	UserID    *string `json:"user_id"`
	Email     *string `json:"email"`
}

// authSecurityEvent is one row of the Auth app's security events: alike
// events (one account, one network, one minute; or one hold) share a row.
type authSecurityEvent struct {
	Kind    string         `json:"kind"`
	UserID  *string        `json:"user_id"`
	Email   *string        `json:"email"`
	Phone   *string        `json:"phone"`
	Network *string        `json:"network"`
	Detail  map[string]any `json:"detail"`
	Count   int            `json:"count"`
	FirstAt string         `json:"first_at"`
	LastAt  string         `json:"last_at"`
}

// authSecurityKinds are the security event kinds the Auth app records.
var authSecurityKinds = []string{"password_failed", "code_failed", "held", "captcha_pass", "trusted_pass", "message_limited", "new_device_alert"}

// cmdAuthActivity lists GoTrue's audit log through the owner's admin key:
// sign-ins, sign-ups, password and account changes, newest first. With
// --security it lists what the sign-in protection did instead.
func cmdAuthActivity(args []string) int {
	fs := newFlagSet("auth activity", flag.ContinueOnError)
	user := fs.String("user", "", "only this user's activity (user ID)")
	search := fs.String("search", "", "an event such as login, or part of an email address")
	security := fs.Bool("security", false, "list security events: wrong passwords and codes, holds, passes, message limits, new-device alerts")
	kind := fs.String("kind", "", "with --security, only this kind: "+strings.Join(authSecurityKinds, ", "))
	network := fs.String("network", "", "with --security, only this network (203.0.113.0/24)")
	limit := fs.Int("limit", 50, fmt.Sprintf("entries to show (1-%d)", api.AuthAdminPageSize))
	keyStdin := fs.Bool("admin-key-stdin", false, "read the admin key from stdin (prompted on a terminal)")
	if err := parseInterspersed(fs, args); err != nil {
		return 1
	}
	misused := (*security && *search != "") || (!*security && (*kind != "" || *network != "")) || (*kind != "" && !slices.Contains(authSecurityKinds, *kind)) || len(*network) > 64 || (*security && *user != "" && !authImportUUID.MatchString(*user))
	if fs.NArg() != 1 || !api.ValidAppSlug(fs.Arg(0)) || *limit < 1 || *limit > api.AuthAdminPageSize || len(*search) > 200 || misused {
		PrintUsage(os.Stderr, "usage: gregale auth activity NAME [--user ID] [--search TEXT | --security [--kind KIND] [--network NET]] [--limit N] [--admin-key-stdin]", "auth")
		return 1
	}
	key, _, err := readRealtimeEndpointSecret("", *keyStdin, "admin-key", "Auth admin key: ", 128, true)
	if err != nil || !authAdminKeyFormat.MatchString(key) {
		return printErr("Invalid admin key", errors.New("expected a key from `gregale auth admin-key create`"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	app, err := client.GetApp(ctx, fs.Arg(0))
	cancel()
	if err != nil {
		return printErr("Could not find the Auth app", err)
	}
	origin, err := authExternalOrigin(app)
	if err != nil {
		return printErr("Could not resolve the Auth app", err)
	}
	query := url.Values{"per_page": {strconv.Itoa(*limit)}}
	if *user != "" {
		query.Set("user", *user)
	}
	if *security {
		return authSecurityEvents(origin, key, query, *kind, *network)
	}
	if *search != "" {
		query.Set("search", *search)
	}
	var page struct {
		Entries []authActivityEntry `json:"entries"`
		More    bool                `json:"more"`
	}
	if err := authAdminCall(http.MethodGet, origin, key, "/admin/v1/activity?"+query.Encode(), nil, &page); err != nil {
		return printErr("Could not read activity", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(page))
	}
	if len(page.Entries) == 0 {
		_, _ = fmt.Fprintln(osStdout, "No activity matches.")
		return 0
	}
	deref := func(value *string) string {
		if value == nil || *value == "" {
			return "-"
		}
		return *value
	}
	for _, entry := range page.Entries {
		who := deref(entry.Email)
		if who == "-" {
			who = deref(entry.UserID)
		}
		action := entry.Action
		if entry.Actor == "administrator" {
			action += " (administrator)"
		}
		_, _ = fmt.Fprintf(osStdout, "%s  %-36s  %s\n", entry.CreatedAt, action, who)
	}
	if page.More {
		_, _ = fmt.Fprintln(osStdout, "More entries exist; narrow with --user or --search.")
	}
	return 0
}

// authSecurityEvents prints the Auth app's security events, newest first:
// when, what and how many, the network, the account and the detail.
func authSecurityEvents(origin, key string, query url.Values, kind, network string) int {
	if kind != "" {
		query.Set("kind", kind)
	}
	if network != "" {
		query.Set("network", network)
	}
	var page struct {
		Entries []authSecurityEvent `json:"entries"`
		More    bool                `json:"more"`
	}
	if err := authAdminCall(http.MethodGet, origin, key, "/admin/v1/security-events?"+query.Encode(), nil, &page); err != nil {
		return printErr("Could not read security events", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(page))
	}
	if len(page.Entries) == 0 {
		_, _ = fmt.Fprintln(osStdout, "No security events match.")
		return 0
	}
	for _, event := range page.Entries {
		what := event.Kind
		if event.Count > 1 {
			what = fmt.Sprintf("%s x%d", what, event.Count)
		}
		who := "unknown account"
		for _, value := range []*string{event.Email, event.Phone, event.UserID} {
			if value != nil && *value != "" {
				who = *value
				break
			}
		}
		where := "-"
		if event.Network != nil {
			where = *event.Network
		}
		_, _ = fmt.Fprintf(osStdout, "%s  %-24s  %-18s  %s%s\n", event.LastAt, what, where, who, authSecurityDetail(event.Detail))
	}
	if page.More {
		_, _ = fmt.Fprintln(osStdout, "More entries exist; narrow with --user, --kind or --network.")
	}
	return 0
}

// authSecurityDetail renders an event's detail (budget, scope, device,
// until) as sorted key=value pairs.
func authSecurityDetail(detail map[string]any) string {
	keys := make([]string, 0, len(detail))
	for name := range detail {
		keys = append(keys, name)
	}
	slices.Sort(keys)
	var b strings.Builder
	for _, name := range keys {
		fmt.Fprintf(&b, "  %s=%v", name, detail[name])
	}
	return b.String()
}
