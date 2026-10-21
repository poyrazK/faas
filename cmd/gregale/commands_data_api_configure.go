// adr: 20261018170742 — owner settings for public reads and RPCs, aggregates and server keys.
package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

var dataAPIPublicRelation = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}\.[A-Za-z_][A-Za-z0-9_]{0,62}$`)

// The runtime rechecks exposure, RLS and security-invoker views at startup.
func validDataAPIPublicRead(value string) bool {
	return validDataAPIAllowlist(value, dataAPIPublicRelation, api.DataAPIPublicReadMaxRelations)
}

// The runtime rechecks approval and STABLE or IMMUTABLE volatility at startup.
func validDataAPIPublicRPC(value string) bool {
	return validDataAPIAllowlist(value, dataAPIFunctionName, api.DataAPIPublicRPCMaxFunctions)
}

func validDataAPIAllowlist(value string, pattern *regexp.Regexp, limit int) bool {
	names := strings.Split(value, ",")
	if value == "" || len(names) > limit {
		return false
	}
	seen := map[string]bool{}
	for _, name := range names {
		if !pattern.MatchString(name) || seen[name] {
			return false
		}
		seen[name] = true
	}
	return true
}

// Removing an absent setting is already the requested state.
func unsetDataAPISetting(ctx context.Context, client *api.Client, app, key, scope string) error {
	err := client.UnsetSecretWithScope(ctx, app, key, scope)
	var ae *APIError
	if err != nil && errors.As(err, &ae) && ae.Problem.Status == http.StatusNotFound {
		return nil
	}
	return err
}

// dataAPIPublicReadCacheRule names the one edge cache rule configure owns.
const dataAPIPublicReadCacheRule = "data-api-public-read-cache"

// syncDataAPIPublicReadCache keeps exactly one named GET /rest/v1/* cache rule.
// The runtime marks only canonical anonymous responses cacheable; requests with
// Authorization bypass the edge cache, so the broad path is safe. maxAge 0
// removes the rule, which also invalidates its entries.
func syncDataAPIPublicReadCache(ctx context.Context, client *api.Client, app string, maxAge int) error {
	rules, err := client.ListEdgeRulesForApp(ctx, app)
	if err != nil {
		return err
	}
	var owned []api.EdgeRuleResponse
	for _, rule := range rules {
		if rule.Name == dataAPIPublicReadCacheRule && rule.Kind == "cache" {
			owned = append(owned, rule)
		}
	}
	keep := 0
	if maxAge > 0 {
		keep = 1
		action, actionErr := json.Marshal(api.EdgeRuleCacheAction{MaxAgeSeconds: maxAge, StaleIfErrorSeconds: api.ResponseCacheStaleIfErrorMaxSeconds, Methods: []string{http.MethodGet}})
		if actionErr != nil {
			return actionErr
		}
		details, appErr := client.GetApp(ctx, app)
		if appErr != nil {
			return appErr
		}
		host, hostErr := cacheHostFromApp(details)
		if hostErr != nil {
			return hostErr
		}
		raw, path, methods, enabled := json.RawMessage(action), "/rest/v1/*", []string{http.MethodGet}, true
		if len(owned) > 0 {
			_, err = client.UpdateEdgeRule(ctx, owned[0].ID, api.UpdateEdgeRuleRequest{MatchHost: &host, MatchPath: &path, MatchMethods: &methods, Enabled: &enabled, Action: &raw})
		} else {
			_, err = client.CreateEdgeRule(ctx, app, api.CreateEdgeRuleRequest{Name: dataAPIPublicReadCacheRule, MatchHost: host, MatchPath: path, MatchMethods: methods, Enabled: &enabled, Kind: "cache", Action: raw})
		}
		if err != nil {
			return err
		}
	}
	for _, rule := range owned[min(keep, len(owned)):] {
		if err = client.DeleteEdgeRule(ctx, rule.ID); err != nil {
			return err
		}
	}
	return nil
}

// dataAPIAnonymousSetting is one requested anonymous allowlist change: a value
// stores it, remove unsets it, and neither keeps whatever is stored.
type dataAPIAnonymousSetting struct {
	key, value string
	remove     bool
}

// dataAPIAnonymousRemains reports whether a public read or RPC allowlist is
// left after the requested changes, listing stored settings only when needed.
func dataAPIAnonymousRemains(ctx context.Context, client *api.Client, app, scope string, settings ...dataAPIAnonymousSetting) (bool, error) {
	kept := map[string]bool{}
	for _, setting := range settings {
		if setting.value != "" {
			return true, nil
		}
		if !setting.remove {
			kept[setting.key] = true
		}
	}
	if len(kept) == 0 {
		return false, nil
	}
	secrets, err := client.ListSecretsWithScope(ctx, app, scope)
	if err != nil {
		return false, err
	}
	for _, secret := range secrets.Secrets {
		if kept[secret.Key] && (secret.Scope == "" || secret.Scope == scope) {
			return true, nil
		}
	}
	return false, nil
}

type dataAPIConfigureReceipt struct {
	App             string              `json:"app"`
	Scope           string              `json:"scope"`
	PublicRead      []string            `json:"public_read,omitempty"`
	PublicRPC       []string            `json:"public_rpc,omitempty"`
	PublicReadCache string              `json:"public_read_cache,omitempty"`
	UsageReporting  string              `json:"usage_reporting,omitempty"`
	MonthlyCaps     *dataAPIMonthlyCaps `json:"monthly_caps,omitempty"`
	// Capacity settings (ADR-20261021180534) as given on the command line.
	Pool                 string   `json:"pool,omitempty"`
	ServerKeyConcurrency string   `json:"server_key_concurrency,omitempty"`
	ServerKeyRateLimit   string   `json:"server_key_rate_limit,omitempty"`
	Changed              []string `json:"changed"`
	Restarted            bool     `json:"restarted"`
	WakeID               string   `json:"wake_id,omitempty"`
}

func cmdDataAPIConfigure(args []string) int {
	fs := newFlagSet("data-api configure", flag.ContinueOnError)
	scope := fs.String("scope", api.DefaultEnvScope, "environment scope")
	publicRead := fs.String("public-read", "", "comma-separated schema.relation list readable without a token")
	noPublicRead := fs.Bool("no-public-read", false, "disable anonymous reads")
	publicRPC := fs.String("public-rpc", "", "comma-separated schema.function list callable without a token by GET")
	noPublicRPC := fs.Bool("no-public-rpc", false, "disable anonymous RPC calls")
	aggregates := fs.String("aggregates", "", "on or off; on also enables the SQL deadline")
	publicReadCache := fs.String("public-read-cache", "", "edge freshness for public reads and RPC calls such as 30s, or off")
	usageReporting := fs.String("usage-reporting", "", "on or off: report usage to the platform")
	monthlyCaps := fs.String("monthly-caps", "", "requests=N[,anonymous=N] or off: monthly owner caps (turns usage reporting on)")
	noRestart := fs.Bool("no-restart", false, "store settings without requesting a fresh restart")
	var scale dataAPIScaleSettings
	fs.StringVar(&scale.Pool, "pool", "", "engine connections per instance, 1 to 8, or default")
	fs.StringVar(&scale.ServerKeyConcurrency, "server-key-concurrency", "", "admission slots server keys may hold together, 1 to 8, or default")
	fs.StringVar(&scale.ServerKeyRateLimit, "server-key-rate-limit", "", "requests per second per server key: N or N,burst=M, or off")
	if err := parseInterspersed(fs, args); err != nil {
		return 1
	}
	anonymousOff := *noPublicRead && *noPublicRPC
	if fs.NArg() != 1 || !api.ValidAppSlug(fs.Arg(0)) || api.ValidateScope(*scope) != nil || (*publicRead != "" && *noPublicRead) || (*publicRPC != "" && *noPublicRPC) ||
		(*publicRead == "" && !*noPublicRead && *publicRPC == "" && !*noPublicRPC && *aggregates == "" && *publicReadCache == "" && *usageReporting == "" && *monthlyCaps == "" && scale.empty()) || !dataAPIDiagnosticAllowed(*aggregates, "", "on", "off") ||
		(anonymousOff && *publicReadCache != "") || !dataAPIDiagnosticAllowed(*usageReporting, "", "on", "off") || (*usageReporting == "off" && *monthlyCaps != "" && *monthlyCaps != "off") {
		PrintUsage(os.Stderr, "usage: gregale data-api configure NAME [--public-read SCHEMA.RELATION[,...] | --no-public-read] [--public-rpc SCHEMA.FUNCTION[,...] | --no-public-rpc] [--public-read-cache DURATION|off] [--aggregates on|off] [--usage-reporting on|off] [--monthly-caps requests=N[,anonymous=N]|off] [--pool N|default] [--server-key-concurrency N|default] [--server-key-rate-limit N[,burst=M]|off] [--scope SCOPE] [--no-restart]", "data-api")
		return 1
	}
	if err := validateDataAPIScale(scale); err != nil {
		return printErr("Invalid capacity setting", err)
	}
	cacheSeconds := 0
	if *publicReadCache != "" && *publicReadCache != "off" {
		var durationErr error
		if cacheSeconds, durationErr = cacheDurationSeconds("public-read-cache", *publicReadCache, api.DataAPIPublicReadMaxAgeSeconds, false); durationErr != nil {
			return printErr("Invalid --public-read-cache", durationErr)
		}
	}
	if *publicReadCache != "" && *scope != api.DefaultEnvScope {
		return printErr("Invalid --public-read-cache", errors.New("edge caching follows the app's canonical host; configure it on the default scope"))
	}
	if *publicRead != "" && !validDataAPIPublicRead(*publicRead) {
		return printErr("Invalid --public-read", fmt.Errorf("list at most %d unique schema.relation names", api.DataAPIPublicReadMaxRelations))
	}
	if *publicRPC != "" && !validDataAPIPublicRPC(*publicRPC) {
		return printErr("Invalid --public-rpc", fmt.Errorf("list at most %d unique schema.function names", api.DataAPIPublicRPCMaxFunctions))
	}
	if *monthlyCaps != "" && *monthlyCaps != "off" {
		if _, capsErr := parseDataAPIMonthlyCaps(*monthlyCaps); capsErr != nil {
			return printErr("Invalid --monthly-caps", capsErr)
		}
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()
	app, receipt := fs.Arg(0), dataAPIConfigureReceipt{App: fs.Arg(0), Scope: *scope, Changed: []string{}}
	if err = configureDataAPIAnonymous(ctx, client, app, *scope, []dataAPIAnonymousSetting{
		{key: "DATA_API_PUBLIC_READ", value: *publicRead, remove: *noPublicRead},
		{key: "DATA_API_PUBLIC_RPC", value: *publicRPC, remove: *noPublicRPC},
	}, cacheSeconds, *publicReadCache == "off", &receipt); err != nil {
		return printErr("Could not configure anonymous access", err)
	}
	switch *aggregates {
	case "on":
		// The SQL deadline is a startup requirement for aggregates; its owner
		// functions must already be installed or the runtime refuses to start.
		if err = client.SetSecretWithScope(ctx, app, "DATA_API_SQL_DEADLINE", "1", *scope); err == nil {
			err = client.SetSecretWithScope(ctx, app, "DATA_API_AGGREGATES", "1", *scope)
		}
	case "off":
		err = unsetDataAPISetting(ctx, client, app, "DATA_API_AGGREGATES", *scope)
	}
	if err != nil {
		return printErr("Could not store the aggregates setting", err)
	}
	if *aggregates != "" {
		receipt.Changed = append(receipt.Changed, "aggregates")
	}
	if err = configureDataAPIUsage(ctx, client, app, *scope, *usageReporting, *monthlyCaps, &receipt); err != nil {
		return printErr("Could not configure usage reporting", err)
	}
	warnings, err := configureDataAPIScale(ctx, client, app, *scope, scale, &receipt)
	if err != nil {
		return printErr("Could not configure capacity", err)
	}
	for _, warning := range warnings {
		PrintWarn(osStderr, "%s", warning)
	}
	if !*noRestart {
		restart, restartErr := client.RestartAppFresh(ctx, app)
		if restartErr != nil {
			return printErr("Settings stored; fresh restart failed", restartErr)
		}
		receipt.Restarted, receipt.WakeID = true, restart.WakeID
	}
	if jsonOutput {
		return jsonOut(writeJSON(receipt))
	}
	PrintOK(osStdout, "Data API settings updated for %s (%s)", app, strings.Join(receipt.Changed, ", "))
	if *publicRPC != "" {
		_, _ = fmt.Fprintln(osStdout, "Public RPCs answer GET and HEAD only. Each function must be approved (@gregale:rpc) and STABLE or IMMUTABLE, or the runtime refuses to start.")
	}
	if *aggregates == "on" {
		_, _ = fmt.Fprintln(osStdout, "Aggregates require the SQL deadline functions; install them first (docs: https://gregale.dev/docs/data-api#sql-deadlines-across-transaction-retries).")
	}
	if !receipt.Restarted {
		_, _ = fmt.Fprintf(osStdout, "Run gregale data-api refresh %s to apply the settings.\n", app)
	}
	return 0
}

// configureDataAPIAnonymous stores the public read and RPC allowlists and the
// edge freshness that serves them. Freshness without an allowlist is a startup
// error, so it is removed before the last allowlist and required before it is
// set; nothing is written when that check fails.
func configureDataAPIAnonymous(ctx context.Context, client *api.Client, app, scope string, settings []dataAPIAnonymousSetting, cacheSeconds int, cacheOff bool, receipt *dataAPIConfigureReceipt) error {
	removing := false
	for _, setting := range settings {
		removing = removing || setting.remove
	}
	anonymous := true
	if cacheSeconds > 0 || removing {
		var err error
		if anonymous, err = dataAPIAnonymousRemains(ctx, client, app, scope, settings...); err != nil {
			return err
		}
	}
	if cacheSeconds > 0 && !anonymous {
		return errors.New("edge caching serves only public reads and RPC calls; pass --public-read or --public-rpc as well")
	}
	names := map[string]string{"DATA_API_PUBLIC_READ": "public_read", "DATA_API_PUBLIC_RPC": "public_rpc"}
	for _, setting := range settings {
		if setting.value == "" {
			continue
		}
		if err := client.SetSecretWithScope(ctx, app, setting.key, setting.value, scope); err != nil {
			return err
		}
		if setting.key == "DATA_API_PUBLIC_READ" {
			receipt.PublicRead = strings.Split(setting.value, ",")
		} else {
			receipt.PublicRPC = strings.Split(setting.value, ",")
		}
		receipt.Changed = append(receipt.Changed, names[setting.key])
	}
	if cacheOff || (removing && !anonymous) {
		if err := syncDataAPIPublicReadCache(ctx, client, app, 0); err != nil {
			return err
		}
		if err := unsetDataAPISetting(ctx, client, app, "DATA_API_PUBLIC_READ_MAX_AGE", scope); err != nil {
			return err
		}
	}
	for _, setting := range settings {
		if !setting.remove {
			continue
		}
		if err := unsetDataAPISetting(ctx, client, app, setting.key, scope); err != nil {
			return err
		}
		receipt.Changed = append(receipt.Changed, names[setting.key])
	}
	switch {
	case cacheSeconds > 0:
		if err := client.SetSecretWithScope(ctx, app, "DATA_API_PUBLIC_READ_MAX_AGE", strconv.Itoa(cacheSeconds), scope); err != nil {
			return err
		}
		if err := syncDataAPIPublicReadCache(ctx, client, app, cacheSeconds); err != nil {
			return err
		}
		receipt.PublicReadCache = (time.Duration(cacheSeconds) * time.Second).String()
	case cacheOff:
		receipt.PublicReadCache = "off"
	}
	if receipt.PublicReadCache != "" {
		receipt.Changed = append(receipt.Changed, "public_read_cache")
	}
	return nil
}

var dataAPIServerKeySlots = map[string]string{"a": "DATA_API_SERVER_KEY_A_SHA256", "b": "DATA_API_SERVER_KEY_B_SHA256"}

func dataAPIServerKeyPermissionsKey(slot string) string {
	return "DATA_API_SERVER_KEY_" + strings.ToUpper(slot) + "_PERMISSIONS"
}

var dataAPIFunctionName = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}\.[a-z][a-z0-9_]{0,62}$`)

// dataAPIServerKeyPermissions is the version-1 runtime contract; nil means the
// key keeps full server access.
type dataAPIServerKeyPermissions struct {
	Version   int      `json:"version"`
	Access    string   `json:"access,omitempty"`
	Relations []string `json:"relations,omitempty"`
	RPCs      []string `json:"rpcs,omitempty"`
	Subjects  *bool    `json:"subjects,omitempty"`
}

func dataAPIPermissionList(value string, pattern *regexp.Regexp) ([]string, error) {
	if value == "" {
		return nil, nil
	}
	names := strings.Split(value, ",")
	seen := map[string]bool{}
	for _, name := range names {
		if !pattern.MatchString(name) || seen[name] {
			return nil, fmt.Errorf("%q must be a unique schema.name entry", name)
		}
		seen[name] = true
	}
	if len(names) > api.DataAPIServerKeyPermissionsMaxEntries {
		return nil, fmt.Errorf("list at most %d entries", api.DataAPIServerKeyPermissionsMaxEntries)
	}
	return names, nil
}

func buildDataAPIServerKeyPermissions(readOnly bool, relations, rpcs string, noSubjects bool) (*dataAPIServerKeyPermissions, error) {
	if !readOnly && relations == "" && rpcs == "" && !noSubjects {
		return nil, nil
	}
	permissions := &dataAPIServerKeyPermissions{Version: 1}
	if readOnly {
		permissions.Access = "read"
	}
	var err error
	if permissions.Relations, err = dataAPIPermissionList(relations, dataAPIPublicRelation); err != nil {
		return nil, fmt.Errorf("--relations: %w", err)
	}
	if permissions.RPCs, err = dataAPIPermissionList(rpcs, dataAPIFunctionName); err != nil {
		return nil, fmt.Errorf("--rpcs: %w", err)
	}
	if noSubjects {
		disabled := false
		permissions.Subjects = &disabled
	}
	encoded, err := json.Marshal(permissions)
	if err != nil {
		return nil, err
	}
	if len(encoded) > api.DataAPIServerKeyPermissionsMaxBytes {
		return nil, fmt.Errorf("permissions exceed %d bytes", api.DataAPIServerKeyPermissionsMaxBytes)
	}
	return permissions, nil
}

// A server key is 32 random bytes; only its SHA-256 hash leaves this process.
func newDataAPIServerKey() (string, string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", "", err
	}
	key := "gdsk_" + base64.RawURLEncoding.EncodeToString(raw)
	sum := sha256.Sum256([]byte(key))
	return key, hex.EncodeToString(sum[:]), nil
}

type dataAPIServerKeySlot struct {
	Slot       string `json:"slot"`
	Present    bool   `json:"present"`
	Restricted bool   `json:"restricted"`
	UpdatedAt  string `json:"updated_at,omitempty"`
}

func listDataAPIServerKeySlots(ctx context.Context, client *api.Client, app, scope string) ([]dataAPIServerKeySlot, error) {
	secrets, err := client.ListSecretsWithScope(ctx, app, scope)
	if err != nil {
		return nil, err
	}
	slots := []dataAPIServerKeySlot{{Slot: "a"}, {Slot: "b"}}
	for _, secret := range secrets.Secrets {
		for i := range slots {
			if secret.Scope != "" && secret.Scope != scope {
				continue
			}
			switch secret.Key {
			case dataAPIServerKeySlots[slots[i].Slot]:
				slots[i].Present, slots[i].UpdatedAt = true, secret.UpdatedAt
			case dataAPIServerKeyPermissionsKey(slots[i].Slot):
				slots[i].Restricted = true
			}
		}
	}
	// Permissions left behind by a removed key do not restrict anything.
	for i := range slots {
		slots[i].Restricted = slots[i].Restricted && slots[i].Present
	}
	return slots, nil
}

// Choose the free slot; with both used, only an explicit request replaces the
// slot updated longest ago (RFC3339 timestamps order lexically in UTC).
func chooseDataAPIServerKeySlot(slots []dataAPIServerKeySlot, replaceOldest bool) (string, error) {
	for _, slot := range slots {
		if !slot.Present {
			return slot.Slot, nil
		}
	}
	if !replaceOldest {
		return "", errors.New("both server key slots are in use; revoke one or pass --replace-oldest")
	}
	if slots[1].UpdatedAt < slots[0].UpdatedAt {
		return "b", nil
	}
	return "a", nil
}

type dataAPIServerKeyReceipt struct {
	App         string                       `json:"app"`
	Scope       string                       `json:"scope"`
	Slot        string                       `json:"slot,omitempty"`
	Key         string                       `json:"key,omitempty"`
	Permissions *dataAPIServerKeyPermissions `json:"permissions,omitempty"`
	Slots       []dataAPIServerKeySlot       `json:"slots,omitempty"`
	Restarted   bool                         `json:"restarted"`
	WakeID      string                       `json:"wake_id,omitempty"`
}

func cmdDataAPIServerKey(args []string) int {
	usage := "usage: gregale data-api server-key <create|list|revoke> NAME [--scope SCOPE] [--read-only] [--relations SCHEMA.RELATION,...] [--rpcs SCHEMA.FUNCTION,...] [--no-subjects] [--replace-oldest] [--slot a|b|all] [--no-restart]"
	if len(args) == 0 || !dataAPIDiagnosticAllowed(args[0], "create", "list", "revoke") {
		PrintUsage(os.Stderr, usage, "data-api")
		return 1
	}
	action := args[0]
	fs := newFlagSet("data-api server-key "+action, flag.ContinueOnError)
	scope := fs.String("scope", api.DefaultEnvScope, "environment scope")
	replaceOldest := fs.Bool("replace-oldest", false, "replace the older slot when both are in use (create)")
	slot := fs.String("slot", "", "slot to revoke: a, b or all (revoke)")
	noRestart := fs.Bool("no-restart", false, "store the change without requesting a fresh restart")
	readOnly := fs.Bool("read-only", false, "allow only GET and HEAD; RPCs need --rpcs (create)")
	relations := fs.String("relations", "", "comma-separated schema.relation allowlist (create)")
	rpcs := fs.String("rpcs", "", "comma-separated schema.function allowlist (create)")
	noSubjects := fs.Bool("no-subjects", false, "refuse X-Gregale-Subject (create)")
	if err := parseInterspersed(fs, args[1:]); err != nil {
		return 1
	}
	restricting := *readOnly || *relations != "" || *rpcs != "" || *noSubjects
	if fs.NArg() != 1 || !api.ValidAppSlug(fs.Arg(0)) || api.ValidateScope(*scope) != nil ||
		(action == "revoke") != (*slot != "") || (*slot != "" && !dataAPIDiagnosticAllowed(*slot, "a", "b", "all")) || (*replaceOldest && action != "create") || (*noRestart && action == "list") || (restricting && action != "create") {
		PrintUsage(os.Stderr, usage, "data-api")
		return 1
	}
	permissions, err := buildDataAPIServerKeyPermissions(*readOnly, *relations, *rpcs, *noSubjects)
	if err != nil {
		return printErr("Invalid server key permissions", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, app := context.Background(), fs.Arg(0)
	receipt := dataAPIServerKeyReceipt{App: app, Scope: *scope}
	slots, err := listDataAPIServerKeySlots(ctx, client, app, *scope)
	if err != nil {
		return printErr("Could not inspect server keys", err)
	}
	switch action {
	case "list":
		receipt.Slots = slots
		if jsonOutput {
			return jsonOut(writeJSON(receipt))
		}
		for _, s := range slots {
			state := "empty"
			if s.Present {
				state = "in use, updated " + s.UpdatedAt
				if s.Restricted {
					state += ", restricted"
				}
			}
			_, _ = fmt.Fprintf(osStdout, "slot %s: %s\n", s.Slot, state)
		}
		return 0
	case "create":
		if receipt.Slot, err = chooseDataAPIServerKeySlot(slots, *replaceOldest); err != nil {
			return printErr("No free server key slot", err)
		}
		var hash string
		if receipt.Key, hash, err = newDataAPIServerKey(); err != nil {
			return printErr("Could not generate a server key", err)
		}
		// Store the slot's permissions before its hash, so the key never exists
		// with a previous key's permissions or none at all.
		if permissions != nil {
			encoded, _ := json.Marshal(permissions)
			err = client.SetSecretWithScope(ctx, app, dataAPIServerKeyPermissionsKey(receipt.Slot), string(encoded), *scope)
		} else {
			err = unsetDataAPISetting(ctx, client, app, dataAPIServerKeyPermissionsKey(receipt.Slot), *scope)
		}
		if err != nil {
			return printErr("Could not store the server key permissions", err)
		}
		receipt.Permissions = permissions
		if err = client.SetSecretWithScope(ctx, app, dataAPIServerKeySlots[receipt.Slot], hash, *scope); err != nil {
			return printErr("Could not store the server key hash", err)
		}
	case "revoke":
		for _, name := range []string{"a", "b"} {
			if *slot == name || *slot == "all" {
				// Remove the key before its permissions: never an unrestricted window.
				if err = unsetDataAPISetting(ctx, client, app, dataAPIServerKeySlots[name], *scope); err == nil {
					err = unsetDataAPISetting(ctx, client, app, dataAPIServerKeyPermissionsKey(name), *scope)
				}
				if err != nil {
					return printErr("Could not revoke the server key", err)
				}
			}
		}
		receipt.Slot = *slot
	}
	if !*noRestart {
		restart, restartErr := client.RestartAppFresh(ctx, app)
		if restartErr != nil {
			return printErr("Server key change stored; fresh restart failed", restartErr)
		}
		receipt.Restarted, receipt.WakeID = true, restart.WakeID
	}
	if jsonOutput {
		return jsonOut(writeJSON(receipt))
	}
	if action == "create" {
		PrintOK(osStdout, "Server key stored in slot %s for %s. It is shown once; keep it in your backend's secret store:", receipt.Slot, app)
		_, _ = fmt.Fprintln(osStdout, receipt.Key)
		if permissions != nil {
			encoded, _ := json.Marshal(permissions)
			_, _ = fmt.Fprintf(osStdout, "Permissions: %s\n", encoded)
		}
	} else {
		PrintOK(osStdout, "Server key slot %s revoked for %s", receipt.Slot, app)
	}
	if !receipt.Restarted {
		_, _ = fmt.Fprintf(osStdout, "Run gregale data-api refresh %s to apply the change.\n", app)
	}
	return 0
}
