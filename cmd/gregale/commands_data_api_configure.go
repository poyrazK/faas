// adr: 20261018170742 — owner settings for public reads, aggregates and server keys.
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
	names := strings.Split(value, ",")
	if value == "" || len(names) > api.DataAPIPublicReadMaxRelations {
		return false
	}
	seen := map[string]bool{}
	for _, name := range names {
		if !dataAPIPublicRelation.MatchString(name) || seen[name] {
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

func dataAPISettingPresent(ctx context.Context, client *api.Client, app, key, scope string) (bool, error) {
	secrets, err := client.ListSecretsWithScope(ctx, app, scope)
	if err != nil {
		return false, err
	}
	for _, secret := range secrets.Secrets {
		if secret.Key == key && (secret.Scope == "" || secret.Scope == scope) {
			return true, nil
		}
	}
	return false, nil
}

type dataAPIConfigureReceipt struct {
	App             string   `json:"app"`
	Scope           string   `json:"scope"`
	PublicRead      []string `json:"public_read,omitempty"`
	PublicReadCache string   `json:"public_read_cache,omitempty"`
	Changed         []string `json:"changed"`
	Restarted       bool     `json:"restarted"`
	WakeID          string   `json:"wake_id,omitempty"`
}

func cmdDataAPIConfigure(args []string) int {
	fs := newFlagSet("data-api configure", flag.ContinueOnError)
	scope := fs.String("scope", api.DefaultEnvScope, "environment scope")
	publicRead := fs.String("public-read", "", "comma-separated schema.relation list readable without a token")
	noPublicRead := fs.Bool("no-public-read", false, "disable anonymous reads")
	aggregates := fs.String("aggregates", "", "on or off; on also enables the SQL deadline")
	publicReadCache := fs.String("public-read-cache", "", "edge freshness for public reads such as 30s, or off")
	noRestart := fs.Bool("no-restart", false, "store settings without requesting a fresh restart")
	if err := parseInterspersed(fs, args); err != nil {
		return 1
	}
	if fs.NArg() != 1 || !api.ValidAppSlug(fs.Arg(0)) || api.ValidateScope(*scope) != nil || (*publicRead != "" && *noPublicRead) || (*publicRead == "" && !*noPublicRead && *aggregates == "" && *publicReadCache == "") || !dataAPIDiagnosticAllowed(*aggregates, "", "on", "off") ||
		(*noPublicRead && *publicReadCache != "") {
		PrintUsage(os.Stderr, "usage: gregale data-api configure NAME [--public-read SCHEMA.RELATION[,...] | --no-public-read] [--public-read-cache DURATION|off] [--aggregates on|off] [--scope SCOPE] [--no-restart]", "data-api")
		return 1
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
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()
	app, receipt := fs.Arg(0), dataAPIConfigureReceipt{App: fs.Arg(0), Scope: *scope, Changed: []string{}}
	switch {
	case *publicRead != "":
		err = client.SetSecretWithScope(ctx, app, "DATA_API_PUBLIC_READ", *publicRead, *scope)
		receipt.PublicRead, receipt.Changed = strings.Split(*publicRead, ","), append(receipt.Changed, "public_read")
	case *noPublicRead:
		// Freshness without public reads is a startup error; remove both together.
		if err = syncDataAPIPublicReadCache(ctx, client, app, 0); err == nil {
			if err = unsetDataAPISetting(ctx, client, app, "DATA_API_PUBLIC_READ_MAX_AGE", *scope); err == nil {
				err = unsetDataAPISetting(ctx, client, app, "DATA_API_PUBLIC_READ", *scope)
			}
		}
		receipt.Changed = append(receipt.Changed, "public_read")
	}
	if err != nil {
		return printErr("Could not store the public read setting", err)
	}
	switch {
	case cacheSeconds > 0:
		present := *publicRead != ""
		if !present {
			if present, err = dataAPISettingPresent(ctx, client, app, "DATA_API_PUBLIC_READ", *scope); err != nil {
				return printErr("Could not inspect Data API settings", err)
			}
		}
		if !present {
			return printErr("Public reads are off", errors.New("edge caching serves only public reads; pass --public-read as well"))
		}
		if err = client.SetSecretWithScope(ctx, app, "DATA_API_PUBLIC_READ_MAX_AGE", strconv.Itoa(cacheSeconds), *scope); err == nil {
			err = syncDataAPIPublicReadCache(ctx, client, app, cacheSeconds)
		}
		receipt.PublicReadCache = (time.Duration(cacheSeconds) * time.Second).String()
	case *publicReadCache == "off":
		if err = syncDataAPIPublicReadCache(ctx, client, app, 0); err == nil {
			err = unsetDataAPISetting(ctx, client, app, "DATA_API_PUBLIC_READ_MAX_AGE", *scope)
		}
		receipt.PublicReadCache = "off"
	}
	if err != nil {
		return printErr("Could not configure public read caching", err)
	}
	if *publicReadCache != "" {
		receipt.Changed = append(receipt.Changed, "public_read_cache")
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
	if *aggregates == "on" {
		_, _ = fmt.Fprintln(osStdout, "Aggregates require the SQL deadline functions; install them first (docs: https://gregale.dev/docs/data-api#sql-deadlines-across-transaction-retries).")
	}
	if !receipt.Restarted {
		_, _ = fmt.Fprintf(osStdout, "Run gregale data-api refresh %s to apply the settings.\n", app)
	}
	return 0
}

var dataAPIServerKeySlots = map[string]string{"a": "DATA_API_SERVER_KEY_A_SHA256", "b": "DATA_API_SERVER_KEY_B_SHA256"}

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
	Slot      string `json:"slot"`
	Present   bool   `json:"present"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

func listDataAPIServerKeySlots(ctx context.Context, client *api.Client, app, scope string) ([]dataAPIServerKeySlot, error) {
	secrets, err := client.ListSecretsWithScope(ctx, app, scope)
	if err != nil {
		return nil, err
	}
	slots := []dataAPIServerKeySlot{{Slot: "a"}, {Slot: "b"}}
	for _, secret := range secrets.Secrets {
		for i := range slots {
			if secret.Key == dataAPIServerKeySlots[slots[i].Slot] && (secret.Scope == "" || secret.Scope == scope) {
				slots[i].Present, slots[i].UpdatedAt = true, secret.UpdatedAt
			}
		}
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
	App       string                 `json:"app"`
	Scope     string                 `json:"scope"`
	Slot      string                 `json:"slot,omitempty"`
	Key       string                 `json:"key,omitempty"`
	Slots     []dataAPIServerKeySlot `json:"slots,omitempty"`
	Restarted bool                   `json:"restarted"`
	WakeID    string                 `json:"wake_id,omitempty"`
}

func cmdDataAPIServerKey(args []string) int {
	usage := "usage: gregale data-api server-key <create|list|revoke> NAME [--scope SCOPE] [--replace-oldest] [--slot a|b|all] [--no-restart]"
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
	if err := parseInterspersed(fs, args[1:]); err != nil {
		return 1
	}
	if fs.NArg() != 1 || !api.ValidAppSlug(fs.Arg(0)) || api.ValidateScope(*scope) != nil ||
		(action == "revoke") != (*slot != "") || (*slot != "" && !dataAPIDiagnosticAllowed(*slot, "a", "b", "all")) || (*replaceOldest && action != "create") || (*noRestart && action == "list") {
		PrintUsage(os.Stderr, usage, "data-api")
		return 1
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
		if err = client.SetSecretWithScope(ctx, app, dataAPIServerKeySlots[receipt.Slot], hash, *scope); err != nil {
			return printErr("Could not store the server key hash", err)
		}
	case "revoke":
		for _, name := range []string{"a", "b"} {
			if *slot == name || *slot == "all" {
				if err = unsetDataAPISetting(ctx, client, app, dataAPIServerKeySlots[name], *scope); err != nil {
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
	} else {
		PrintOK(osStdout, "Server key slot %s revoked for %s", receipt.Slot, app)
	}
	if !receipt.Restarted {
		_, _ = fmt.Fprintf(osStdout, "Run gregale data-api refresh %s to apply the change.\n", app)
	}
	return 0
}
