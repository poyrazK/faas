package main

// Short flag commands (ADR 20261114150000 §1). Each reads the latest
// configuration, changes one thing, and publishes it with the version it
// read. A concurrent publish answers 409; the change is re-applied to the
// fresh configuration, at most flagsEditAttempts times.

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/flags"
)

const (
	flagsEditAttempts     = 3
	flagsEveryonePercent  = "everyone-percent"
	flagsTypeVariant      = "variant"
	flagsUsageEditVerbs   = "list|stale|create|set|on|off|kill|unkill|percent|rule-add|rule-rm|rm|edge-set|edge-rm|edge-list"
	flagsEditConflictCode = "flags_version_conflict"
)

// flagsEditVerbs are the short verbs; cmdFlags hands them to cmdFlagsEdit.
var flagsEditVerbs = map[string]string{
	"list":     "--project",
	"stale":    "--project",
	"create":   "a flag KEY",
	"set":      "a flag KEY and at least one of --default, --description, --owner, --expires",
	"on":       "a flag KEY",
	"off":      "a flag KEY",
	"kill":     "a flag KEY",
	"unkill":   "a flag KEY",
	"percent":  "a flag KEY and a percentage 0..100",
	"rule-add": "a flag KEY and --id",
	"rule-rm":  "a flag KEY and --id",
	"rm":       "a flag KEY",
	// ADR-20261124212701: edge routing.
	"edge-set":  "a flag KEY, --path, --variant and --rewrite",
	"edge-rm":   "a flag KEY (and --path for one route)",
	"edge-list": "--project",
}

// flagsSnapshot is the part of GET .../flags the edit verbs use.
type flagsSnapshot struct {
	Version int64 `json:"version"`
	flags.Config
}

// flagsEdit changes cfg in place and returns a one-line summary.
type flagsEdit func(cfg *flags.Config) (string, error)

// repeatedFlag collects a repeatable string option.
type repeatedFlag []string

func (r *repeatedFlag) String() string     { return strings.Join(*r, ",") }
func (r *repeatedFlag) Set(v string) error { *r = append(*r, v); return nil }

func cmdFlagsEdit(verb string, args []string) int {
	fs := newFlagSet("flags-"+verb, flag.ContinueOnError)
	project := fs.String("project", "", "project slug")
	environment := fs.String("environment", "production", "named project environment")
	description := fs.String("description", "", "flag description")
	defaultValue := fs.String("default", "", "default value: true/false, or a variant key")
	variants := fs.String("variants", "", "comma-separated variant keys for a variant flag")
	ruleID := fs.String("id", "", "targeting rule ID")
	group := fs.String("group", "", "customer group")
	percent := fs.String("percent", "", "percentage of eligible customers, 0..100")
	value := fs.String("value", "", "rule value: true/false, or a variant key")
	owner := fs.String("owner", "", "who decides about the flag: a team or an email")
	expires := fs.String("expires", "", "expiry date YYYY-MM-DD, or 'never' to clear")
	all := fs.Bool("all", false, "stale: list every flag, not only those needing attention")
	edgePath := fs.String("path", "", "edge: the routed path, /page or /prefix/*")
	edgeVariant := fs.String("variant", "", "edge: the variant the route rewrites")
	edgeRewrite := fs.String("rewrite", "", "edge: the path the variant serves instead")
	var customers, attrs repeatedFlag
	fs.Var(&customers, "customer", "platform customer UUID (repeatable)")
	fs.Var(&attrs, "attr", "attribute condition NAME=V1,V2, NAME!=V1,V2 or NAME$=SUFFIX (repeatable)")
	positionals, err := parseInterleaved(fs, args)
	if err != nil {
		return 1
	}
	want := 1
	switch verb {
	case "list", "stale", "edge-list":
		want = 0
	case "percent":
		want = 2
	}
	if *project == "" || *environment == "" || len(positionals) != want {
		PrintUsage(os.Stderr, fmt.Sprintf("gregale %s needs --project and %s", cliTreeSpellPath("flags "+verb), flagsEditVerbs[verb]), "flags")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()
	if verb == "list" {
		snap, err := readFlagsSnapshot(ctx, client, *project, *environment)
		if err != nil {
			return printErr("Flags operation failed", err)
		}
		return printFlagsList(snap)
	}
	if verb == "edge-list" {
		snap, err := readFlagsSnapshot(ctx, client, *project, *environment)
		if err != nil {
			return printErr("Flags operation failed", err)
		}
		return printFlagsEdge(snap)
	}
	if verb == "stale" {
		raw, err := client.ProjectFlagLifecycle(ctx, *project, *environment)
		if err != nil {
			return printErr("Flags operation failed", err)
		}
		return printFlagsLifecycle(raw, *all)
	}
	key := positionals[0]
	if !flags.ValidKey(key) {
		printCommandValidation(os.Stderr, "gregale flags %s: %q is not a valid flag key (lowercase letter, then up to 63 of a-z 0-9 _ -)\n", verb, key)
		return 1
	}
	var edit flagsEdit
	switch verb {
	case "create":
		edit = withFlagsMeta(flagsCreate(key, *variants, *defaultValue, *description, fs), key, *owner, *expires, fs)
	case "set":
		if !flagSetAny(fs, "default", "description", "owner", "expires") {
			PrintUsage(os.Stderr, "gregale flag set needs at least one of --default, --description, --owner, --expires", "flags")
			return 1
		}
		edit = withFlagsMeta(flagsSet(key, *defaultValue, *description, fs), key, *owner, *expires, fs)
	case "on":
		edit = flagsOnOff(key, true)
	case "off":
		edit = flagsOnOff(key, false)
	case "kill":
		edit = flagsKill(key, true)
	case "unkill":
		edit = flagsKill(key, false)
	case "percent":
		bp, err := parseFlagsPercent(positionals[1])
		if err != nil {
			printCommandValidation(os.Stderr, "gregale flag rollout set: %v\n", err)
			return 1
		}
		edit = flagsPercent(key, bp)
	case "rule-add":
		rule, err := flagsRuleFromOptions(*ruleID, customers, *group, attrs, *percent, *value, fs)
		if err != nil {
			printCommandValidation(os.Stderr, "gregale flag rule create: %v\n", err)
			return 1
		}
		edit = flagsRuleAdd(key, rule)
	case "rule-rm":
		if *ruleID == "" {
			PrintUsage(os.Stderr, "gregale flag rule delete needs --id", "flags")
			return 1
		}
		edit = flagsRuleRemove(key, *ruleID)
	case "rm":
		edit = flagsRemove(key)
	case "edge-set":
		if *edgePath == "" || *edgeVariant == "" || *edgeRewrite == "" {
			PrintUsage(os.Stderr, "gregale flag edge set needs --path, --variant and --rewrite", "flags")
			return 1
		}
		edit = flagsEdgeSet(key, *edgePath, *edgeVariant, *edgeRewrite)
	case "edge-rm":
		edit = flagsEdgeRemove(key, *edgePath)
	}
	version, summary, err := runFlagsEdit(ctx, client, *project, *environment, edit)
	if err != nil {
		return printErr("Flags operation failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(map[string]any{"version": version, "summary": summary}))
	}
	_, _ = fmt.Fprintf(osStdout, "%s (version %d)\n", summary, version)
	return 0
}

// parseInterleaved parses options that may follow positionals.
func parseInterleaved(fs *flag.FlagSet, args []string) ([]string, error) {
	var positionals []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		args = fs.Args()
		if len(args) == 0 {
			return positionals, nil
		}
		positionals = append(positionals, args[0])
		args = args[1:]
	}
}

func flagSetAny(fs *flag.FlagSet, names ...string) bool {
	set := false
	fs.Visit(func(f *flag.Flag) {
		if slices.Contains(names, f.Name) {
			set = true
		}
	})
	return set
}

func readFlagsSnapshot(ctx context.Context, client *api.Client, project, environment string) (flagsSnapshot, error) {
	raw, err := client.ProjectFlags(ctx, project, environment)
	if err != nil {
		return flagsSnapshot{}, err
	}
	var snap flagsSnapshot
	if err := json.Unmarshal(raw, &snap); err != nil {
		return flagsSnapshot{}, fmt.Errorf("decode flag configuration: %w", err)
	}
	if snap.Flags == nil {
		snap.Flags = []flags.Flag{}
	}
	if snap.Groups == nil {
		snap.Groups = map[string][]string{}
	}
	return snap, nil
}

// runFlagsEdit applies edit to the latest configuration and publishes it,
// re-reading and re-applying on a version conflict.
func runFlagsEdit(ctx context.Context, client *api.Client, project, environment string, edit flagsEdit) (int64, string, error) {
	var lastErr error
	for range flagsEditAttempts {
		snap, err := readFlagsSnapshot(ctx, client, project, environment)
		if err != nil {
			return 0, "", err
		}
		summary, err := edit(&snap.Config)
		if err != nil {
			return 0, "", err
		}
		body, err := json.Marshal(map[string]any{"expected_version": snap.Version, "config": snap.Config})
		if err != nil {
			return 0, "", err
		}
		raw, err := client.PublishProjectFlags(ctx, project, environment, body)
		if err == nil {
			var published flagsSnapshot
			if err := json.Unmarshal(raw, &published); err != nil {
				return 0, "", fmt.Errorf("decode published configuration: %w", err)
			}
			return published.Version, summary, nil
		}
		if !isFlagsVersionConflict(err) {
			return 0, "", err
		}
		lastErr = err
	}
	return 0, "", fmt.Errorf("configuration kept changing; try again: %w", lastErr)
}

func isFlagsVersionConflict(err error) bool {
	var apiErr *api.APIError
	return errors.As(err, &apiErr) && apiErr.Problem.Code == flagsEditConflictCode
}

func flagsCreate(key, variantList, defaultValue, description string, fs *flag.FlagSet) flagsEdit {
	var variants []string
	if variantList != "" {
		variants = splitList(variantList)
		if len(variants) == 0 {
			variants = []string{""}
		}
	}
	return flagsEdit(flags.EditCreate(key, variants, optionalFlag(fs, "default", defaultValue), description))
}

// optionalFlag is value when the option was given on the command line.
func optionalFlag(fs *flag.FlagSet, name, value string) *string {
	if !flagSetAny(fs, name) {
		return nil
	}
	return &value
}

func flagsSet(key, defaultValue, description string, fs *flag.FlagSet) flagsEdit {
	return flagsEdit(flags.EditSet(key, optionalFlag(fs, "default", defaultValue), optionalFlag(fs, "description", description)))
}

func flagsOnOff(key string, on bool) flagsEdit { return flagsEdit(flags.EditOnOff(key, on)) }

// withFlagsMeta applies --owner and --expires after edit
// (ADR 20261114150000 §4).
func withFlagsMeta(edit flagsEdit, key, owner, expires string, fs *flag.FlagSet) flagsEdit {
	return flagsEdit(flags.EditMeta(flags.Edit(edit), key, optionalFlag(fs, "owner", owner), optionalFlag(fs, "expires", expires)))
}

// printFlagsLifecycle prints the lifecycle report, by default only the flags
// that need attention.
func printFlagsLifecycle(raw json.RawMessage, all bool) int {
	var report struct {
		ConfigVersion int64                 `json:"config_version"`
		Flags         []flags.FlagLifecycle `json:"flags"`
	}
	if err := json.Unmarshal(raw, &report); err != nil {
		return printErr("Flags operation failed", fmt.Errorf("decode lifecycle report: %w", err))
	}
	if jsonOutput {
		return jsonOut(writeJSON(report))
	}
	rows := report.Flags
	if !all {
		rows = slices.DeleteFunc(slices.Clone(rows), func(f flags.FlagLifecycle) bool { return len(f.Attention) == 0 })
	}
	if len(rows) == 0 {
		_, _ = fmt.Fprintf(osStdout, "No flag needs attention (version %d).\n", report.ConfigVersion)
		return 0
	}
	tw := tabwriter.NewWriter(osStdout, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "KEY\tOWNER\tEXPIRES\tLAST CHANGED\tREQUESTS (7D)\tATTENTION")
	for _, f := range rows {
		requests := "-"
		if f.EvaluatedRequests != nil {
			requests = strconv.FormatInt(*f.EvaluatedRequests, 10)
		}
		changed := f.LastChangedAt
		if len(changed) >= 10 {
			changed = changed[:10]
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s (v%d)\t%s\t%s\n", f.Key, dashIfEmpty(f.Owner), dashIfEmpty(f.ExpiresAt), changed, f.LastChangedVersion, requests, dashIfEmpty(strings.Join(f.Attention, ", ")))
	}
	if err := tw.Flush(); err != nil {
		return 1
	}
	return 0
}

// flagsKill sets or clears a flag's kill (ADR 20261114150000 §3).
func flagsKill(key string, killed bool) flagsEdit { return flagsEdit(flags.EditKill(key, killed)) }

// parseFlagsPercent accepts "12", "12.5" or "12.5%" and returns basis points.
func parseFlagsPercent(raw string) (int, error) { return flags.ParsePercent(raw) }

func flagsPercent(key string, bp int) flagsEdit { return flagsEdit(flags.EditPercent(key, bp)) }

// flagsAttributeName expands a short attribute name: country, ua_family and
// cohort are the gateway's, any other bare name is the application's.
func flagsAttributeName(name string) string {
	switch {
	case strings.Contains(name, "."):
		return name
	case name == "country" || name == "ua_family" || name == "cohort":
		return "gregale." + name
	default:
		return flags.AppAttributePrefix + name
	}
}

// parseFlagsCondition reads NAME=V1,V2 (in), NAME!=V1,V2 (not_in) or
// NAME$=SUFFIX,... (ends_with).
func parseFlagsCondition(raw string) (flags.Condition, error) {
	for _, form := range []struct{ sep, op string }{{"!=", flags.OpNotIn}, {"$=", flags.OpEndsWith}, {"=", flags.OpIn}} {
		name, values, ok := strings.Cut(raw, form.sep)
		if !ok {
			continue
		}
		c := flags.Condition{Attribute: flagsAttributeName(strings.TrimSpace(name)), Op: form.op, Values: splitList(values)}
		if !flags.ValidAttributeName(c.Attribute) || len(c.Values) == 0 {
			break
		}
		return c, nil
	}
	return flags.Condition{}, fmt.Errorf("--attr %q: use NAME=V1,V2 (in), NAME!=V1,V2 (not in) or NAME$=SUFFIX (ends with); NAME is country, ua_family, cohort or an app attribute", raw)
}

// parseFlagsInspectAttributes reads repeated NAME=VALUE options.
func parseFlagsInspectAttributes(raw []string) (map[string]string, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	out := map[string]string{}
	for _, item := range raw {
		name, value, ok := strings.Cut(item, "=")
		name = flagsAttributeName(strings.TrimSpace(name))
		if !ok || !flags.ValidAttributeName(name) || !flags.ValidAttributeValue(value) {
			return nil, fmt.Errorf("--attr %q: use NAME=VALUE; NAME is country, ua_family, cohort or an app attribute", item)
		}
		out[name] = value
	}
	return out, nil
}

// flagsRuleFromOptions builds a rule from rule-add options. The value is
// checked against the flag later, in flagsRuleAdd.
func flagsRuleFromOptions(id string, customers []string, group string, attrs []string, percent, value string, fs *flag.FlagSet) (flags.Rule, error) {
	if !flags.ValidKey(id) {
		return flags.Rule{}, fmt.Errorf("--id must be a rule ID (lowercase letter, then up to 63 of a-z 0-9 _ -)")
	}
	rule := flags.Rule{ID: id, Customers: []string(customers), Group: group}
	for _, raw := range attrs {
		c, err := parseFlagsCondition(raw)
		if err != nil {
			return flags.Rule{}, err
		}
		rule.Attributes = append(rule.Attributes, c)
	}
	if flagSetAny(fs, "percent") {
		bp, err := parseFlagsPercent(percent)
		if err != nil {
			return flags.Rule{}, err
		}
		rule.Rollout = &bp
	}
	if flagSetAny(fs, "value") {
		rule.Value = value
	}
	return rule, nil
}

// flagsRuleAdd inserts rule before the everyone-percent rule.
func flagsRuleAdd(key string, rule flags.Rule) flagsEdit {
	return flagsEdit(flags.EditRuleAdd(key, rule))
}

func flagsRuleRemove(key, id string) flagsEdit { return flagsEdit(flags.EditRuleRemove(key, id)) }

func flagsRemove(key string) flagsEdit { return flagsEdit(flags.EditRemove(key)) }

func flagState(f flags.Flag) string {
	if f.Killed {
		return "killed"
	}
	if !f.Enabled {
		return "off"
	}
	return "on"
}

func printFlagsList(snap flagsSnapshot) int {
	if jsonOutput {
		return jsonOut(writeJSON(snap))
	}
	if len(snap.Flags) == 0 {
		_, _ = fmt.Fprintf(osStdout, "No flags (version %d). Create one with gregale flag create KEY --project ...\n", snap.Version)
		return 0
	}
	tw := tabwriter.NewWriter(osStdout, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "KEY\tTYPE\tSTATE\tDEFAULT\tRULES\tOWNER\tEXPIRES")
	for _, f := range snap.Flags {
		typ := f.Type
		if typ == "" {
			typ = "boolean"
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%v\t%d\t%s\t%s\n", f.Key, typ, flagState(f), f.Default, len(f.Rules), dashIfEmpty(f.Owner), dashIfEmpty(f.ExpiresAt))
	}
	if err := tw.Flush(); err != nil {
		return 1
	}
	_, _ = fmt.Fprintf(osStdout, "version %d\n", snap.Version)
	return 0
}

// flagEdgeRow is one route action in `gregale flag edge list`.
type flagEdgeRow struct {
	Flag    string `json:"flag"`
	Path    string `json:"path"`
	Variant string `json:"variant"`
	Rewrite string `json:"rewrite"`
}

// printFlagsEdge lists every edge route action (ADR-20261124212701).
func printFlagsEdge(snap flagsSnapshot) int {
	rows := []flagEdgeRow{}
	for _, f := range snap.Flags {
		if f.Edge == nil {
			continue
		}
		for _, route := range f.Edge.Routes {
			variants := make([]string, 0, len(route.Variants))
			for v := range route.Variants {
				variants = append(variants, v)
			}
			slices.Sort(variants)
			for _, v := range variants {
				rows = append(rows, flagEdgeRow{Flag: f.Key, Path: route.Path, Variant: v, Rewrite: route.Variants[v].Rewrite})
			}
		}
	}
	if jsonOutput {
		return jsonOut(writeJSON(map[string]any{"version": snap.Version, "routes": rows}))
	}
	if len(rows) == 0 {
		_, _ = fmt.Fprintf(osStdout, "No edge routes (version %d). Add one with gregale flag edge set KEY --path /page --variant VARIANT --rewrite /page-b --project ...\n", snap.Version)
		return 0
	}
	tw := tabwriter.NewWriter(osStdout, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "FLAG\tPATH\tVARIANT\tSERVES")
	for _, r := range rows {
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", r.Flag, r.Path, r.Variant, r.Rewrite)
	}
	if err := tw.Flush(); err != nil {
		return 1
	}
	_, _ = fmt.Fprintf(osStdout, "version %d; other variants serve the path unchanged\n", snap.Version)
	return 0
}

func flagsEdgeSet(key, path, variant, rewrite string) flagsEdit {
	return flagsEdit(flags.EditEdgeSet(key, path, variant, rewrite))
}

func flagsEdgeRemove(key, path string) flagsEdit { return flagsEdit(flags.EditEdgeRemove(key, path)) }
