// commands_flags.go — platform flags (ADR-1700): release gates that enable a
// platform feature for selected accounts without a deploy.
//
// Reads use /v1/admin/flags. Every mutation reads the flag, edits its
// definition locally, validates it, and writes it back through
// PATCH /v1/admin/config/platform_flag.<key> with the version it read, so a
// concurrent edit fails with a conflict instead of being overwritten. The
// server re-validates and enforces MFA step-up, the operator allowlist and
// audit.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/platformflags"
)

const dispatchFlags = "flags"

const (
	flagsAllowRuleID    = "accounts"
	flagsDenyRuleID     = "denied-accounts"
	flagsOrgAllowRuleID = "orgs"
	flagsOrgDenyRuleID  = "denied-orgs"
	flagsRolloutRuleID  = "rollout"
)

// flagsExplicitRuleOrder is where `allow` and `deny` put their rules, ahead
// of every other rule: a person's decision beats their company's, and a deny
// beats an allow.
var flagsExplicitRuleOrder = []string{flagsDenyRuleID, flagsAllowRuleID, flagsOrgDenyRuleID, flagsOrgAllowRuleID}

// flagsMutation is one write. reason, traceID and yes point at the parsed
// command-line values; edit (or editGrps for the groups row) changes the
// value read from apid.
type flagsMutation struct {
	name     string
	reason   *string
	traceID  *string
	yes      *bool
	edit     func(*platformflags.Definition) error
	groups   bool
	editGrps func(platformflags.Groups) error
}

func cmdFlagsDispatch(args []string) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(osStderr, "gregalectl flags: missing subcommand; want list|show|inspect|account|enable|disable|allow|deny|revoke|rollout|default|set|schedule|groups|group-add|group-remove|history|rollback")
		return 2
	}
	handlers := map[string]func([]string) int{
		"list": cmdFlagsList, "show": cmdFlagsShow, "inspect": cmdFlagsInspect, "account": cmdFlagsAccount,
		"enable": cmdFlagsSwitch(true), "disable": cmdFlagsSwitch(false),
		"allow": cmdFlagsAccountRule("allow"), "deny": cmdFlagsAccountRule("deny"), "revoke": cmdFlagsAccountRule("revoke"),
		"rollout": cmdFlagsRollout, "default": cmdFlagsDefault, "set": cmdFlagsSet, "schedule": cmdFlagsSchedule,
		"groups": cmdFlagsGroups, "group-add": cmdFlagsGroupMember(true), "group-remove": cmdFlagsGroupMember(false),
		"history": cmdFlagsHistory, "rollback": cmdFlagsRollback,
	}
	handler, ok := handlers[args[0]]
	if !ok {
		_, _ = fmt.Fprintf(osStderr, "gregalectl flags: unknown subcommand %q\n", args[0])
		return 2
	}
	return handler(args[1:])
}

func cmdFlagsList(args []string) int {
	fs := flagsFlagSet("list")
	if err := fs.Parse(args); err != nil || !flagsNoPositional(fs, "list") {
		return 2
	}
	var response api.OperatorPlatformFlagList
	if code := flagsGet("list", "/v1/admin/flags", &response); code != 0 {
		return code
	}
	if jsonEnabled() {
		return emitOperatorJSON(response)
	}
	for _, item := range response.Items {
		printPlatformFlagSummary(item)
	}
	for _, name := range sortedGroupNames(response.Groups.Groups) {
		_, _ = fmt.Fprintf(osStdout, "group=%s accounts=%d used_by=%s\n", name, len(response.Groups.Groups[name]), strings.Join(response.Groups.References[name], ","))
	}
	return 0
}

func cmdFlagsShow(args []string) int {
	fs := flagsFlagSet("show")
	key := fs.String("key", "", "platform flag key")
	if err := fs.Parse(args); err != nil || !flagsNoPositional(fs, "show") {
		return 2
	}
	cleanKey, ok := flagsRequireKey("show", *key)
	if !ok {
		return 2
	}
	var item api.OperatorPlatformFlag
	if code := flagsGet("show", "/v1/admin/flags/"+url.PathEscape(cleanKey), &item); code != 0 {
		return code
	}
	if jsonEnabled() {
		return emitOperatorJSON(item)
	}
	printPlatformFlagSummary(item)
	definition, _ := json.Marshal(item.Definition)
	_, _ = fmt.Fprintf(osStdout, "label=%s\ncapability=%s\nadr=%s\nconsumers=%s\nattributes=%s\nlegacy_env=%s\ndefinition=%s\n",
		item.Label, item.Capability, item.ADR, strings.Join(item.Consumers, ","), strings.Join(item.Attributes, ","), strings.Join(item.LegacyEnv, ","), definition)
	for _, receipt := range item.Receipts {
		_, _ = fmt.Fprintf(osStdout, "receipt daemon=%s instance=%s version=%d status=%s reported_at=%s error=%q\n",
			receipt.Daemon, receipt.Instance, receipt.Version, receipt.Status, receipt.ReportedAt, receipt.Error)
	}
	return 0
}

func cmdFlagsInspect(args []string) int {
	fs := flagsFlagSet("inspect")
	key := fs.String("key", "", "platform flag key")
	accountID := fs.String("account-id", "", "account UUID")
	orgID := fs.String("org-id", "", "organization UUID the request would act under (optional)")
	at := fs.String("at", "", "decide as of this RFC 3339 time instead of now (optional)")
	if err := fs.Parse(args); err != nil || !flagsNoPositional(fs, "inspect") {
		return 2
	}
	cleanKey, ok := flagsRequireKey("inspect", *key)
	if !ok || !flagsRequireAccount("inspect", *accountID) {
		return 2
	}
	var decision api.OperatorPlatformFlagDecision
	path := "/v1/admin/flags/" + url.PathEscape(cleanKey) + "/decision?account_id=" + url.QueryEscape(strings.TrimSpace(*accountID))
	if org := strings.TrimSpace(*orgID); org != "" {
		path += "&org_id=" + url.QueryEscape(org)
	}
	if when := strings.TrimSpace(*at); when != "" {
		path += "&at=" + url.QueryEscape(when)
	}
	if code := flagsGet("inspect", path, &decision); code != 0 {
		return code
	}
	if jsonEnabled() {
		return emitOperatorJSON(decision)
	}
	printPlatformFlagDecision(decision)
	return 0
}

func cmdFlagsAccount(args []string) int {
	fs := flagsFlagSet("account")
	accountID := fs.String("account-id", "", "account UUID")
	if err := fs.Parse(args); err != nil || !flagsNoPositional(fs, "account") || !flagsRequireAccount("account", *accountID) {
		return 2
	}
	var response api.OperatorAccountPlatformFlags
	if code := flagsGet("account", "/v1/admin/accounts/"+url.PathEscape(strings.TrimSpace(*accountID))+"/flags", &response); code != 0 {
		return code
	}
	if jsonEnabled() {
		return emitOperatorJSON(response)
	}
	for _, decision := range response.Items {
		printPlatformFlagDecision(decision)
	}
	return 0
}

// cmdFlagsSwitch turns the kill switch on or off.
func cmdFlagsSwitch(enabled bool) func([]string) int {
	name := map[bool]string{true: "enable", false: "disable"}[enabled]
	return func(args []string) int {
		fs := flagsFlagSet(name)
		key, mutation := flagsMutationFlags(fs)
		if err := fs.Parse(args); err != nil || !flagsNoPositional(fs, name) {
			return 2
		}
		mutation.name = name
		mutation.edit = func(def *platformflags.Definition) error {
			def.Enabled = enabled
			return nil
		}
		return runFlagsMutation(*key, mutation)
	}
}

// cmdFlagsAccountRule maintains the explicit account lists. A deny rule runs
// first and an allow rule second, so an explicit decision always beats a
// group, plan or rollout rule; revoke removes the account from both.
func cmdFlagsAccountRule(name string) func([]string) int {
	return func(args []string) int {
		fs := flagsFlagSet(name)
		key, mutation := flagsMutationFlags(fs)
		accountID := fs.String("account-id", "", "account UUID")
		orgID := fs.String("org-id", "", "organization UUID: everyone acting under the company")
		if err := fs.Parse(args); err != nil || !flagsNoPositional(fs, name) || !flagsRequireSubject(name, *accountID, *orgID) {
			return 2
		}
		orgs := strings.TrimSpace(*orgID) != ""
		id, allowID, denyID := strings.ToLower(strings.TrimSpace(*accountID)), flagsAllowRuleID, flagsDenyRuleID
		if orgs {
			id, allowID, denyID = strings.ToLower(strings.TrimSpace(*orgID)), flagsOrgAllowRuleID, flagsOrgDenyRuleID
		}
		mutation.name = name
		mutation.edit = func(def *platformflags.Definition) error {
			removeSubjectFromRule(def, allowID, id, orgs)
			removeSubjectFromRule(def, denyID, id, orgs)
			switch name {
			case "allow":
				addSubjectToRule(def, allowID, id, orgs, true)
			case "deny":
				addSubjectToRule(def, denyID, id, orgs, false)
			}
			return nil
		}
		return runFlagsMutation(*key, mutation)
	}
}

// cmdFlagsSchedule sets or clears the window of one existing rule (ADR
// 20261021080640). "none" clears a boundary; an omitted flag keeps it.
func cmdFlagsSchedule(args []string) int {
	fs := flagsFlagSet("schedule")
	key, mutation := flagsMutationFlags(fs)
	ruleID := fs.String("rule-id", "", "rule to schedule (see flags show)")
	startsAt := fs.String("starts-at", "", "RFC 3339 time the rule starts applying, or none")
	endsAt := fs.String("ends-at", "", "RFC 3339 time the rule stops applying, or none")
	if err := fs.Parse(args); err != nil || !flagsNoPositional(fs, "schedule") {
		return 2
	}
	id := strings.TrimSpace(*ruleID)
	if id == "" || (strings.TrimSpace(*startsAt) == "" && strings.TrimSpace(*endsAt) == "") {
		_, _ = fmt.Fprintln(osStderr, "gregalectl flags schedule: --rule-id and at least one of --starts-at or --ends-at required")
		return 2
	}
	starts, keepStarts, err := parseScheduleBoundary(*startsAt)
	if err != nil {
		_, _ = fmt.Fprintf(osStderr, "gregalectl flags schedule: --starts-at: %v\n", err)
		return 2
	}
	ends, keepEnds, err := parseScheduleBoundary(*endsAt)
	if err != nil {
		_, _ = fmt.Fprintf(osStderr, "gregalectl flags schedule: --ends-at: %v\n", err)
		return 2
	}
	mutation.name = "schedule"
	mutation.edit = func(def *platformflags.Definition) error {
		for i := range def.Rules {
			if def.Rules[i].ID != id {
				continue
			}
			if !keepStarts {
				def.Rules[i].StartsAt = starts
			}
			if !keepEnds {
				def.Rules[i].EndsAt = ends
			}
			return nil
		}
		return fmt.Errorf("flag has no rule %q", id)
	}
	return runFlagsMutation(*key, mutation)
}

// parseScheduleBoundary reads one --starts-at/--ends-at value: empty keeps
// the current boundary, "none" clears it, anything else must be RFC 3339.
func parseScheduleBoundary(raw string) (at *time.Time, keep bool, err error) {
	switch raw = strings.TrimSpace(raw); raw {
	case "":
		return nil, true, nil
	case "none":
		return nil, false, nil
	}
	parsed, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return nil, false, fmt.Errorf("%q is not an RFC 3339 time (for example 2026-11-02T09:00:00Z)", raw)
	}
	parsed = parsed.UTC()
	return &parsed, false, nil
}

func cmdFlagsRollout(args []string) int {
	fs := flagsFlagSet("rollout")
	key, mutation := flagsMutationFlags(fs)
	percent := fs.Float64("percent", -1, "share of eligible accounts, 0..100 (0 removes the rollout rule)")
	plans := fs.String("plans", "", "comma-separated plans the rollout is limited to")
	if err := fs.Parse(args); err != nil || !flagsNoPositional(fs, "rollout") {
		return 2
	}
	if *percent < 0 || *percent > 100 {
		_, _ = fmt.Fprintln(osStderr, "gregalectl flags rollout: --percent must be between 0 and 100")
		return 2
	}
	basisPoints := int(*percent*100 + 0.5)
	mutation.name = "rollout"
	mutation.edit = func(def *platformflags.Definition) error {
		def.Rules = slices.DeleteFunc(def.Rules, func(rule platformflags.Rule) bool { return rule.ID == flagsRolloutRuleID })
		if basisPoints == 0 {
			return nil
		}
		value := true
		rule := platformflags.Rule{ID: flagsRolloutRuleID, Rollout: &basisPoints, Value: &value}
		for _, plan := range strings.Split(*plans, ",") {
			if plan = strings.TrimSpace(plan); plan != "" {
				rule.Plans = append(rule.Plans, plan)
			}
		}
		def.Rules = append(def.Rules, rule)
		return nil
	}
	return runFlagsMutation(*key, mutation)
}

func cmdFlagsDefault(args []string) int {
	fs := flagsFlagSet("default")
	key, mutation := flagsMutationFlags(fs)
	value := fs.String("value", "", "default for accounts no rule matches: true or false")
	if err := fs.Parse(args); err != nil || !flagsNoPositional(fs, "default") {
		return 2
	}
	parsed, err := strconv.ParseBool(strings.TrimSpace(*value))
	if err != nil {
		_, _ = fmt.Fprintln(osStderr, "gregalectl flags default: --value must be true or false")
		return 2
	}
	mutation.name = "default"
	mutation.edit = func(def *platformflags.Definition) error {
		def.Default = parsed
		return nil
	}
	return runFlagsMutation(*key, mutation)
}

func cmdFlagsSet(args []string) int {
	fs := flagsFlagSet("set")
	key, mutation := flagsMutationFlags(fs)
	file := fs.String("file", "", "JSON definition file ({\"enabled\":..,\"default\":..,\"rules\":[..]}); - reads stdin")
	if err := fs.Parse(args); err != nil || !flagsNoPositional(fs, "set") {
		return 2
	}
	raw, err := readFlagsFile(*file)
	if err != nil {
		_, _ = fmt.Fprintln(osStderr, "gregalectl flags set:", err)
		return 2
	}
	replacement, err := platformflags.ParseDefinition(raw)
	if err != nil {
		_, _ = fmt.Fprintln(osStderr, "gregalectl flags set:", err)
		return 2
	}
	mutation.name = "set"
	mutation.edit = func(def *platformflags.Definition) error {
		*def = replacement
		return nil
	}
	return runFlagsMutation(*key, mutation)
}

func cmdFlagsGroups(args []string) int {
	fs := flagsFlagSet("groups")
	if err := fs.Parse(args); err != nil || !flagsNoPositional(fs, "groups") {
		return 2
	}
	var response api.OperatorPlatformFlagList
	if code := flagsGet("groups", "/v1/admin/flags", &response); code != 0 {
		return code
	}
	if jsonEnabled() {
		return emitOperatorJSON(response.Groups)
	}
	for _, name := range sortedGroupNames(response.Groups.Groups) {
		_, _ = fmt.Fprintf(osStdout, "group=%s accounts=%s used_by=%s\n", name,
			strings.Join(response.Groups.Groups[name], ","), strings.Join(response.Groups.References[name], ","))
	}
	return 0
}

// cmdFlagsGroupMember adds an account to a group (creating it) or removes it.
func cmdFlagsGroupMember(add bool) func([]string) int {
	name := map[bool]string{true: "group-add", false: "group-remove"}[add]
	return func(args []string) int {
		fs := flagsFlagSet(name)
		mutation := flagsMutationOptions(fs)
		group := fs.String("group", "", "group name")
		accountID := fs.String("account-id", "", "account UUID")
		if err := fs.Parse(args); err != nil || !flagsNoPositional(fs, name) || !flagsRequireAccount(name, *accountID) {
			return 2
		}
		groupName := strings.TrimSpace(*group)
		if groupName == "" {
			_, _ = fmt.Fprintf(osStderr, "gregalectl flags %s: --group required\n", name)
			return 2
		}
		account := strings.ToLower(strings.TrimSpace(*accountID))
		mutation.name, mutation.groups = name, true
		mutation.editGrps = func(groups platformflags.Groups) error {
			members := slices.DeleteFunc(slices.Clone(groups[groupName]), func(id string) bool { return id == account })
			if add {
				members = append(members, account)
			}
			groups[groupName] = members
			return nil
		}
		return runFlagsMutation("", mutation)
	}
}

func cmdFlagsHistory(args []string) int {
	fs := flagsFlagSet("history")
	key := fs.String("key", "", "platform flag key, or groups")
	limit := fs.Int("limit", 50, "maximum revisions to return (1..200)")
	if err := fs.Parse(args); err != nil || !flagsNoPositional(fs, "history") {
		return 2
	}
	configKey, ok := flagsConfigKey("history", *key)
	if !ok {
		return 2
	}
	if *limit < 1 || *limit > 200 {
		_, _ = fmt.Fprintln(osStderr, "gregalectl flags history: --limit must be between 1 and 200")
		return 2
	}
	var response operatorConfigHistoryResponse
	if code := flagsGet("history", "/v1/admin/config/"+url.PathEscape(configKey)+"/revisions?limit="+strconv.Itoa(*limit), &response); code != 0 {
		return code
	}
	if jsonEnabled() {
		return emitOperatorJSON(response)
	}
	for _, revision := range response.Items {
		_, _ = fmt.Fprintf(osStdout, "version=%d actor=%s reason=%q created_at=%s new=%s\n",
			revision.Version, revision.ActorID, revision.Reason, revision.CreatedAt, displayConfigJSON(revision.NewValue))
	}
	return 0
}

func cmdFlagsRollback(args []string) int {
	fs := flagsFlagSet("rollback")
	key, mutation := flagsMutationFlags(fs)
	version := fs.Int64("version", 0, "historical version to restore")
	if err := fs.Parse(args); err != nil || !flagsNoPositional(fs, "rollback") {
		return 2
	}
	configKey, ok := flagsConfigKey("rollback", *key)
	if !ok {
		return 2
	}
	if *version < 1 {
		_, _ = fmt.Fprintln(osStderr, "gregalectl flags rollback: --version must be positive")
		return 2
	}
	reason, traceID, ok := flagsMutationPreflight("rollback", mutation)
	if !ok {
		return 2
	}
	sess, current, code := flagsCurrentVersion("rollback", configKey)
	if code != 0 {
		return code
	}
	request := api.RollbackOperatorRuntimeConfigRequest{Version: *version, Reason: reason, ExpectedVersion: &current}
	return flagsWrite(&sess, http.MethodPost, "/v1/admin/config/"+url.PathEscape(configKey)+"/rollback", request, traceID, "rollback")
}

// runFlagsMutation reads the current value, applies the edit, validates the
// result locally and writes it with the version it read.
func runFlagsMutation(key string, mutation flagsMutation) int {
	reason, traceID, ok := flagsMutationPreflight(mutation.name, mutation)
	if !ok {
		return 2
	}
	sess, err := loadOperatorSession()
	if err != nil {
		_, _ = fmt.Fprintf(osStderr, "gregalectl flags %s: %v\n", mutation.name, err)
		return 1
	}
	var list api.OperatorPlatformFlagList
	if err := newOperatorHTTPClient(&sess).doJSON(context.Background(), http.MethodGet, "/v1/admin/flags", nil, &list, false, nil); err != nil {
		_, _ = fmt.Fprintf(osStderr, "gregalectl flags %s: %v\n", mutation.name, err)
		return 1
	}
	configKey, value, expected, err := editedFlagsValue(list, key, mutation)
	if err != nil {
		_, _ = fmt.Fprintf(osStderr, "gregalectl flags %s: %v\n", mutation.name, err)
		return 2
	}
	request := operatorConfigPatchRequest{Value: value, Reason: reason, ExpectedVersion: &expected}
	return flagsWrite(&sess, http.MethodPatch, "/v1/admin/config/"+url.PathEscape(configKey), request, traceID, mutation.name)
}

func editedFlagsValue(list api.OperatorPlatformFlagList, key string, mutation flagsMutation) (string, json.RawMessage, int64, error) {
	if mutation.groups {
		groups := platformflags.Groups{}
		for name, members := range list.Groups.Groups {
			groups[name] = slices.Clone(members)
		}
		if err := mutation.editGrps(groups); err != nil {
			return "", nil, 0, err
		}
		groups = platformflags.NormalizeGroups(groups)
		if err := platformflags.ValidateGroups(groups); err != nil {
			return "", nil, 0, err
		}
		return platformflags.GroupsConfigKey, groups.Marshal(), list.Groups.Version, nil
	}
	cleanKey := strings.TrimSpace(key)
	for _, item := range list.Items {
		if item.Key != cleanKey {
			continue
		}
		def := fromAPIDefinition(item.Definition)
		if err := mutation.edit(&def); err != nil {
			return "", nil, 0, err
		}
		def = platformflags.NormalizeDefinition(def)
		groups := platformflags.Groups(list.Groups.Groups)
		if groups == nil {
			groups = platformflags.Groups{}
		}
		if err := platformflags.ValidateDefinition(def, groups); err != nil {
			return "", nil, 0, err
		}
		expected := item.Version
		if item.Source != "operator" {
			// No durable row yet: the first write must create it.
			expected = 0
		}
		return item.ConfigKey, def.Marshal(), expected, nil
	}
	return "", nil, 0, fmt.Errorf("unknown platform flag %q", cleanKey)
}

func fromAPIDefinition(in api.PlatformFlagDefinition) platformflags.Definition {
	out := platformflags.Definition{Enabled: in.Enabled, Default: in.Default}
	for _, rule := range in.Rules {
		out.Rules = append(out.Rules, platformflags.Rule{
			ID: rule.ID, Accounts: slices.Clone(rule.Accounts), Orgs: slices.Clone(rule.Orgs), Group: rule.Group,
			Plans: slices.Clone(rule.Plans), Rollout: rule.Rollout, StartsAt: rule.StartsAt, EndsAt: rule.EndsAt, Value: rule.Value,
		})
	}
	return out
}

// ruleTargets is the rule's organization list when orgs, else its accounts.
func ruleTargets(rule *platformflags.Rule, orgs bool) *[]string {
	if orgs {
		return &rule.Orgs
	}
	return &rule.Accounts
}

func addSubjectToRule(def *platformflags.Definition, ruleID, id string, orgs, value bool) {
	for i := range def.Rules {
		if def.Rules[i].ID == ruleID {
			targets := ruleTargets(&def.Rules[i], orgs)
			*targets = append(*targets, id)
			return
		}
	}
	rule := platformflags.Rule{ID: ruleID, Value: &value}
	*ruleTargets(&rule, orgs) = []string{id}
	def.Rules = slices.Insert(def.Rules, explicitRulePosition(def.Rules, ruleID), rule)
}

// explicitRulePosition is just after the explicit rules that come before
// ruleID in flagsExplicitRuleOrder.
func explicitRulePosition(rules []platformflags.Rule, ruleID string) int {
	rank := slices.Index(flagsExplicitRuleOrder, ruleID)
	position := 0
	for position < len(rules) {
		existing := slices.Index(flagsExplicitRuleOrder, rules[position].ID)
		if existing < 0 || existing > rank {
			break
		}
		position++
	}
	return position
}

func removeSubjectFromRule(def *platformflags.Definition, ruleID, id string, orgs bool) {
	for i := range def.Rules {
		if def.Rules[i].ID != ruleID {
			continue
		}
		targets := ruleTargets(&def.Rules[i], orgs)
		*targets = slices.DeleteFunc(*targets, func(v string) bool { return v == id })
		rule := def.Rules[i]
		if len(rule.Accounts) == 0 && len(rule.Orgs) == 0 && rule.Group == "" && len(rule.Plans) == 0 && rule.Rollout == nil {
			def.Rules = slices.Delete(def.Rules, i, i+1)
		}
		return
	}
}

func flagsWrite(sess *operatorSession, method, path string, input any, traceID, action string) int {
	headers := make(http.Header)
	headers.Set(operatorTraceIDHeader, traceID)
	var response api.OperatorRuntimeConfig
	if err := newOperatorHTTPClient(sess).doJSONWithHeaders(context.Background(), method, path, input, &response, true, nil, headers); err != nil {
		_, _ = fmt.Fprintf(osStderr, "gregalectl flags %s: %v\n", action, err)
		return 1
	}
	if jsonEnabled() {
		return emitOperatorJSON(operatorConfigMutationOutput{OperatorRuntimeConfig: response, TraceID: traceID})
	}
	_, _ = fmt.Fprintf(osStdout, "config_key=%s version=%d status=%s value=%s\ntrace_id=%s\n",
		response.Key, response.Version, response.Status, displayConfigJSON(response.EffectiveValue), traceID)
	return 0
}

func flagsCurrentVersion(action, configKey string) (operatorSession, int64, int) {
	sess, catalog, code := loadOperatorConfigCatalog(action)
	if code != 0 {
		return operatorSession{}, 0, code
	}
	entry, found := findOperatorConfig(catalog.Items, configKey)
	if !found {
		_, _ = fmt.Fprintf(osStderr, "gregalectl flags %s: unknown configuration key %q\n", action, configKey)
		return operatorSession{}, 0, 2
	}
	return sess, entry.Version, 0
}

func flagsGet(action, path string, out any) int {
	sess, err := loadOperatorSession()
	if err != nil {
		_, _ = fmt.Fprintf(osStderr, "gregalectl flags %s: %v\n", action, err)
		return 1
	}
	if err := newOperatorHTTPClient(&sess).doJSON(context.Background(), http.MethodGet, path, nil, out, false, nil); err != nil {
		_, _ = fmt.Fprintf(osStderr, "gregalectl flags %s: %v\n", action, err)
		return 1
	}
	return 0
}

func flagsFlagSet(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(osStderr)
	return fs
}

func flagsMutationFlags(fs *flag.FlagSet) (*string, flagsMutation) {
	key := fs.String("key", "", "platform flag key")
	return key, flagsMutationOptions(fs)
}

// flagsMutationOptions registers the confirmation flags every write takes.
func flagsMutationOptions(fs *flag.FlagSet) flagsMutation {
	return flagsMutation{
		reason:  fs.String("reason", "", "audit reason (3..500 characters)"),
		traceID: fs.String("trace-id", "", "OTel 32-char-hex trace id (auto-generated when empty)"),
		yes:     fs.Bool("yes", false, "acknowledge the platform flag change"),
	}
}

func flagsMutationPreflight(action string, mutation flagsMutation) (string, string, bool) {
	reason, ok := validateConfigMutation(action, *mutation.reason, *mutation.yes)
	if !ok {
		return "", "", false
	}
	traceID, ok := configMutationTraceID(action, *mutation.traceID)
	return reason, traceID, ok
}

func flagsNoPositional(fs *flag.FlagSet, action string) bool {
	if fs.NArg() != 0 {
		_, _ = fmt.Fprintf(osStderr, "gregalectl flags %s: positional arguments are not accepted\n", action)
		return false
	}
	return true
}

func flagsRequireKey(action, raw string) (string, bool) {
	key := strings.TrimSpace(raw)
	if _, ok := platformflags.Lookup(key); !ok {
		_, _ = fmt.Fprintf(osStderr, "gregalectl flags %s: --key must name a platform flag (%s)\n", action, strings.Join(platformFlagKeys(), ", "))
		return "", false
	}
	return key, true
}

// flagsConfigKey maps a flag key, or "groups", to its runtime-config key.
func flagsConfigKey(action, raw string) (string, bool) {
	if strings.TrimSpace(raw) == "groups" {
		return platformflags.GroupsConfigKey, true
	}
	key, ok := flagsRequireKey(action, raw)
	return platformflags.ConfigKey(key), ok
}

// flagsRequireSubject wants exactly one of --account-id and --org-id.
func flagsRequireSubject(action, account, org string) bool {
	if (strings.TrimSpace(account) == "") == (strings.TrimSpace(org) == "") {
		_, _ = fmt.Fprintf(osStderr, "gregalectl flags %s: exactly one of --account-id or --org-id required\n", action)
		return false
	}
	return true
}

func flagsRequireAccount(action, raw string) bool {
	if strings.TrimSpace(raw) == "" {
		_, _ = fmt.Fprintf(osStderr, "gregalectl flags %s: --account-id required\n", action)
		return false
	}
	return true
}

func platformFlagKeys() []string {
	var keys []string
	for _, spec := range platformflags.Catalog() {
		keys = append(keys, spec.Key)
	}
	return keys
}

func readFlagsFile(path string) (json.RawMessage, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, fmt.Errorf("--file required")
	}
	var data []byte
	var err error
	if path == "-" {
		data, err = io.ReadAll(io.LimitReader(os.Stdin, api.PlatformFlagsMaxDefinitionBytes+1))
	} else {
		data, err = os.ReadFile(path)
	}
	if err != nil {
		return nil, err
	}
	if len(data) > api.PlatformFlagsMaxDefinitionBytes {
		return nil, fmt.Errorf("definition exceeds %d bytes", api.PlatformFlagsMaxDefinitionBytes)
	}
	return json.RawMessage(data), nil
}

func sortedGroupNames(groups map[string][]string) []string {
	names := make([]string, 0, len(groups))
	for name := range groups {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

func printPlatformFlagSummary(item api.OperatorPlatformFlag) {
	_, _ = fmt.Fprintf(osStdout, "key=%s enabled=%t default=%t rules=%d version=%d source=%s status=%s converged=%s pending=%s failed=%s%s\n",
		item.Key, item.Definition.Enabled, item.Definition.Default, len(item.Definition.Rules), item.Version, item.Source, item.Status,
		strings.Join(item.Convergence.Converged, ","), strings.Join(item.Convergence.Pending, ","), strings.Join(item.Convergence.Failed, ","),
		flagsRetireBy(item.RetireBy, time.Now()))
}

// flagsRetireBy renders a preview flag's retire-by date, marking it overdue
// once the date has passed (ADR 20261021080640).
func flagsRetireBy(retireBy string, now time.Time) string {
	if retireBy == "" {
		return ""
	}
	out := " retire_by=" + retireBy
	if at, err := time.Parse(time.DateOnly, retireBy); err == nil && !now.Before(at) {
		out += " overdue=true"
	}
	return out
}

func printPlatformFlagDecision(d api.OperatorPlatformFlagDecision) {
	entitled := "n/a"
	if d.Entitled != nil {
		entitled = strconv.FormatBool(*d.Entitled)
	}
	org, scheduled := "", ""
	if d.OrgID != "" {
		org = " org=" + d.OrgID
	}
	if d.ScheduledRuleID != "" {
		scheduled = " scheduled_rule=" + d.ScheduledRuleID + " schedule_state=" + d.ScheduleState
	}
	if d.At != "" {
		scheduled += " at=" + d.At
	}
	_, _ = fmt.Fprintf(osStdout, "flag=%s account=%s%s plan=%s enabled=%t reason=%s rule=%s entitled=%s available=%t version=%d%s\n",
		d.Flag, d.AccountID, org, d.Plan, d.Enabled, d.Reason, d.RuleID, entitled, d.Available, d.Version, scheduled)
}
