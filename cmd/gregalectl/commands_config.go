// commands_config.go — authenticated, zero-downtime runtime configuration.
//
// gregalectl deliberately mutates only catalog entries whose apply mode is
// hot. Graceful and rolling settings remain deployment-managed until their
// durable controllers are enabled, so this command cannot trigger a rollout.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/wire"
)

const dispatchConfig = "config"

var configTraceIDShape = regexp.MustCompile(`^[0-9a-f]{32}$`)

type operatorConfigListResponse struct {
	Items       []api.OperatorRuntimeConfig `json:"items"`
	GeneratedAt string                      `json:"generated_at"`
}

type operatorConfigHistoryResponse struct {
	Items []api.OperatorRuntimeConfigRevision `json:"items"`
}

type operatorConfigPatchRequest struct {
	Value           json.RawMessage `json:"value"`
	Reason          string          `json:"reason"`
	ExpectedVersion *int64          `json:"expected_version"`
}

type operatorConfigMutationOutput struct {
	api.OperatorRuntimeConfig
	TraceID string `json:"trace_id"`
}

func cmdConfigDispatch(args []string) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(osStderr, "gregalectl config: missing subcommand; want list|show|history|set|rollback")
		return 2
	}
	switch args[0] {
	case "list":
		return cmdConfigList(args[1:])
	case "show":
		return cmdConfigShow(args[1:])
	case "history":
		return cmdConfigHistory(args[1:])
	case "set":
		return cmdConfigSet(args[1:])
	case "rollback":
		return cmdConfigRollback(args[1:])
	default:
		_, _ = fmt.Fprintf(osStderr, "gregalectl config: unknown subcommand %q\n", args[0])
		return 2
	}
}

func cmdConfigList(args []string) int {
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	fs.SetOutput(osStderr)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		_, _ = fmt.Fprintln(osStderr, "gregalectl config list: positional arguments are not accepted")
		return 2
	}
	_, response, code := loadOperatorConfigCatalog("list")
	if code != 0 {
		return code
	}
	if jsonEnabled() {
		return emitOperatorJSON(response)
	}
	for _, entry := range response.Items {
		printOperatorConfigSummary(entry)
	}
	return 0
}

func cmdConfigShow(args []string) int {
	fs := flag.NewFlagSet("show", flag.ContinueOnError)
	fs.SetOutput(osStderr)
	key := fs.String("key", "", "runtime configuration key")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	cleanKey, ok := validateConfigKeyArgs(fs, "show", *key)
	if !ok {
		return 2
	}
	_, response, code := loadOperatorConfigCatalog("show")
	if code != 0 {
		return code
	}
	entry, found := findOperatorConfig(response.Items, cleanKey)
	if !found {
		_, _ = fmt.Fprintf(osStderr, "gregalectl config show: unknown configuration key %q\n", cleanKey)
		return 2
	}
	if jsonEnabled() {
		return emitOperatorJSON(entry)
	}
	printOperatorConfigSummary(entry)
	_, _ = fmt.Fprintf(osStdout, "label=%s\ncategory=%s\nkind=%s\nmutable=%t\ncontroller_enabled=%t\ndescription=%s\n",
		entry.Label, entry.Category, entry.Kind, entry.Mutable, entry.ControllerEnabled, entry.Description)
	if entry.LastError != "" {
		_, _ = fmt.Fprintf(osStdout, "last_error=%s\n", entry.LastError)
	}
	return 0
}

func cmdConfigHistory(args []string) int {
	fs := flag.NewFlagSet("history", flag.ContinueOnError)
	fs.SetOutput(osStderr)
	key := fs.String("key", "", "runtime configuration key")
	limit := fs.Int("limit", 50, "maximum revisions to return (1..200)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	cleanKey, ok := validateConfigKeyArgs(fs, "history", *key)
	if !ok {
		return 2
	}
	if *limit < 1 || *limit > 200 {
		_, _ = fmt.Fprintln(osStderr, "gregalectl config history: --limit must be between 1 and 200")
		return 2
	}
	sess, err := loadOperatorSession()
	if err != nil {
		_, _ = fmt.Fprintln(osStderr, "gregalectl config history:", err)
		return 1
	}
	path := "/v1/admin/config/" + url.PathEscape(cleanKey) + "/revisions?limit=" + strconv.Itoa(*limit)
	var response operatorConfigHistoryResponse
	if err := newOperatorHTTPClient(&sess).doJSON(context.Background(), http.MethodGet, path, nil, &response, false, nil); err != nil {
		_, _ = fmt.Fprintln(osStderr, "gregalectl config history:", err)
		return 1
	}
	if jsonEnabled() {
		return emitOperatorJSON(response)
	}
	for _, revision := range response.Items {
		_, _ = fmt.Fprintf(osStdout, "version=%d old=%s new=%s actor=%s reason=%q created_at=%s\n",
			revision.Version, displayConfigJSON(revision.OldValue), displayConfigJSON(revision.NewValue),
			revision.ActorID, revision.Reason, revision.CreatedAt)
	}
	return 0
}

func cmdConfigSet(args []string) int {
	fs := flag.NewFlagSet("set", flag.ContinueOnError)
	fs.SetOutput(osStderr)
	key := fs.String("key", "", "runtime configuration key")
	value := fs.String("value", "", "new value (JSON scalar or plain string)")
	reason := fs.String("reason", "", "audit reason (3..500 characters)")
	traceIDFlag := fs.String("trace-id", "", "OTel 32-char-hex trace id (auto-generated when empty)")
	ack := fs.Bool("yes", false, "acknowledge the runtime configuration change")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	cleanKey, ok := validateConfigKeyArgs(fs, "set", *key)
	if !ok {
		return 2
	}
	if strings.TrimSpace(*value) == "" {
		_, _ = fmt.Fprintln(osStderr, "gregalectl config set: --value required")
		return 2
	}
	cleanReason, ok := validateConfigMutation("set", *reason, *ack)
	if !ok {
		return 2
	}
	traceID, ok := configMutationTraceID("set", *traceIDFlag)
	if !ok {
		return 2
	}
	sess, catalog, code := loadOperatorConfigCatalog("set")
	if code != 0 {
		return code
	}
	entry, found := findOperatorConfig(catalog.Items, cleanKey)
	if !found {
		_, _ = fmt.Fprintf(osStderr, "gregalectl config set: unknown configuration key %q\n", cleanKey)
		return 2
	}
	if !configHotMutationAllowed("set", entry) {
		return 2
	}
	valueJSON := normalizeOperatorConfigValue(*value)
	request := operatorConfigPatchRequest{Value: valueJSON, Reason: cleanReason, ExpectedVersion: &entry.Version}
	return mutateOperatorConfig(&sess, http.MethodPatch, "/v1/admin/config/"+url.PathEscape(cleanKey), request, traceID, "set")
}

func cmdConfigRollback(args []string) int {
	fs := flag.NewFlagSet("rollback", flag.ContinueOnError)
	fs.SetOutput(osStderr)
	key := fs.String("key", "", "runtime configuration key")
	version := fs.Int64("version", 0, "historical version to restore")
	reason := fs.String("reason", "", "audit reason (3..500 characters)")
	traceIDFlag := fs.String("trace-id", "", "OTel 32-char-hex trace id (auto-generated when empty)")
	ack := fs.Bool("yes", false, "acknowledge the runtime configuration rollback")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	cleanKey, ok := validateConfigKeyArgs(fs, "rollback", *key)
	if !ok {
		return 2
	}
	if *version < 1 {
		_, _ = fmt.Fprintln(osStderr, "gregalectl config rollback: --version must be positive")
		return 2
	}
	cleanReason, ok := validateConfigMutation("rollback", *reason, *ack)
	if !ok {
		return 2
	}
	traceID, ok := configMutationTraceID("rollback", *traceIDFlag)
	if !ok {
		return 2
	}
	sess, catalog, code := loadOperatorConfigCatalog("rollback")
	if code != 0 {
		return code
	}
	entry, found := findOperatorConfig(catalog.Items, cleanKey)
	if !found {
		_, _ = fmt.Fprintf(osStderr, "gregalectl config rollback: unknown configuration key %q\n", cleanKey)
		return 2
	}
	if !configHotMutationAllowed("rollback", entry) {
		return 2
	}
	request := api.RollbackOperatorRuntimeConfigRequest{
		Version: *version, Reason: cleanReason, ExpectedVersion: &entry.Version,
	}
	path := "/v1/admin/config/" + url.PathEscape(cleanKey) + "/rollback"
	return mutateOperatorConfig(&sess, http.MethodPost, path, request, traceID, "rollback")
}

func loadOperatorConfigCatalog(action string) (operatorSession, operatorConfigListResponse, int) {
	sess, err := loadOperatorSession()
	if err != nil {
		_, _ = fmt.Fprintf(osStderr, "gregalectl config %s: %v\n", action, err)
		return operatorSession{}, operatorConfigListResponse{}, 1
	}
	var response operatorConfigListResponse
	if err := newOperatorHTTPClient(&sess).doJSON(context.Background(), http.MethodGet, "/v1/admin/config", nil, &response, false, nil); err != nil {
		_, _ = fmt.Fprintf(osStderr, "gregalectl config %s: %v\n", action, err)
		return operatorSession{}, operatorConfigListResponse{}, 1
	}
	return sess, response, 0
}

func mutateOperatorConfig(sess *operatorSession, method, path string, input any, traceID, action string) int {
	headers := make(http.Header)
	headers.Set(operatorTraceIDHeader, traceID)
	var response api.OperatorRuntimeConfig
	if err := newOperatorHTTPClient(sess).doJSONWithHeaders(context.Background(), method, path, input, &response, true, nil, headers); err != nil {
		_, _ = fmt.Fprintf(osStderr, "gregalectl config %s: %v\n", action, err)
		return 1
	}
	output := operatorConfigMutationOutput{OperatorRuntimeConfig: response, TraceID: traceID}
	if jsonEnabled() {
		return emitOperatorJSON(output)
	}
	printOperatorConfigSummary(response)
	_, _ = fmt.Fprintf(osStdout, "trace_id=%s\n", traceID)
	return 0
}

func validateConfigKeyArgs(fs *flag.FlagSet, action, rawKey string) (string, bool) {
	if fs.NArg() != 0 {
		_, _ = fmt.Fprintf(osStderr, "gregalectl config %s: positional arguments are not accepted\n", action)
		return "", false
	}
	key := strings.TrimSpace(rawKey)
	if key == "" {
		_, _ = fmt.Fprintf(osStderr, "gregalectl config %s: --key required\n", action)
		return "", false
	}
	return key, true
}

func validateConfigMutation(action, rawReason string, ack bool) (string, bool) {
	reason := strings.TrimSpace(rawReason)
	if len(reason) < 3 || len(reason) > 500 {
		_, _ = fmt.Fprintf(osStderr, "gregalectl config %s: --reason must be 3..500 characters\n", action)
		return "", false
	}
	if !ack {
		_, _ = fmt.Fprintf(osStderr, "gregalectl config %s: --yes required\n", action)
		return "", false
	}
	return reason, true
}

func configMutationTraceID(action, raw string) (string, bool) {
	traceID := strings.TrimSpace(raw)
	if traceID == "" {
		traceID = wire.NewTraceID()
	}
	if !configTraceIDShape.MatchString(traceID) {
		_, _ = fmt.Fprintf(osStderr, "gregalectl config %s: --trace-id must be 32 lowercase hex characters\n", action)
		return "", false
	}
	return traceID, true
}

func configHotMutationAllowed(action string, entry api.OperatorRuntimeConfig) bool {
	if !entry.Mutable {
		_, _ = fmt.Fprintf(osStderr, "gregalectl config %s: %s is deployment-managed and cannot be changed at runtime\n", action, entry.Key)
		return false
	}
	if entry.ApplyMode != "hot" {
		_, _ = fmt.Fprintf(osStderr, "gregalectl config %s: refusing %s apply for %s; this CLI only changes hot settings and cannot trigger a rollout\n",
			action, entry.ApplyMode, entry.Key)
		return false
	}
	return true
}

func findOperatorConfig(items []api.OperatorRuntimeConfig, key string) (api.OperatorRuntimeConfig, bool) {
	for _, item := range items {
		if item.Key == key {
			return item, true
		}
	}
	return api.OperatorRuntimeConfig{}, false
}

func normalizeOperatorConfigValue(raw string) json.RawMessage {
	value := strings.TrimSpace(raw)
	if json.Valid([]byte(value)) {
		return json.RawMessage(value)
	}
	encoded, _ := json.Marshal(value)
	return json.RawMessage(encoded)
}

func printOperatorConfigSummary(entry api.OperatorRuntimeConfig) {
	_, _ = fmt.Fprintf(osStdout, "key=%s desired=%s effective=%s source=%s apply_mode=%s status=%s version=%d mutable=%t\n",
		entry.Key, displayConfigJSON(entry.DesiredValue), displayConfigJSON(entry.EffectiveValue),
		entry.Source, entry.ApplyMode, entry.Status, entry.Version, entry.Mutable)
}

func displayConfigJSON(value json.RawMessage) string {
	if len(value) == 0 {
		return "null"
	}
	return string(value)
}
