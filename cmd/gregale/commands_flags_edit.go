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
	"math"
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
	flagsUsageEditVerbs   = "list|create|set|on|off|kill|unkill|percent|rule-add|rule-rm|rm"
	flagsEditConflictCode = "flags_version_conflict"
)

// flagsEditVerbs are the short verbs; cmdFlags hands them to cmdFlagsEdit.
var flagsEditVerbs = map[string]string{
	"list":     "--project",
	"create":   "a flag KEY",
	"set":      "a flag KEY and at least one of --default, --description",
	"on":       "a flag KEY",
	"off":      "a flag KEY",
	"kill":     "a flag KEY",
	"unkill":   "a flag KEY",
	"percent":  "a flag KEY and a percentage 0..100",
	"rule-add": "a flag KEY and --id",
	"rule-rm":  "a flag KEY and --id",
	"rm":       "a flag KEY",
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
	var customers, attrs repeatedFlag
	fs.Var(&customers, "customer", "platform customer UUID (repeatable)")
	fs.Var(&attrs, "attr", "attribute condition NAME=V1,V2, NAME!=V1,V2 or NAME$=SUFFIX (repeatable)")
	positionals, err := parseInterleaved(fs, args)
	if err != nil {
		return 1
	}
	want := 1
	switch verb {
	case "list":
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
	key := positionals[0]
	if !flags.ValidKey(key) {
		printCommandValidation(os.Stderr, "gregale flags %s: %q is not a valid flag key (lowercase letter, then up to 63 of a-z 0-9 _ -)\n", verb, key)
		return 1
	}
	var edit flagsEdit
	switch verb {
	case "create":
		edit = flagsCreate(key, *variants, *defaultValue, *description, fs)
	case "set":
		if !flagSetAny(fs, "default", "description") {
			PrintUsage(os.Stderr, "gregale flags set needs at least one of --default, --description", "flags")
			return 1
		}
		edit = flagsSet(key, *defaultValue, *description, fs)
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
			printCommandValidation(os.Stderr, "gregale flags percent: %v\n", err)
			return 1
		}
		edit = flagsPercent(key, bp)
	case "rule-add":
		rule, err := flagsRuleFromOptions(*ruleID, customers, *group, attrs, *percent, *value, fs)
		if err != nil {
			printCommandValidation(os.Stderr, "gregale flags rule-add: %v\n", err)
			return 1
		}
		edit = flagsRuleAdd(key, rule)
	case "rule-rm":
		if *ruleID == "" {
			PrintUsage(os.Stderr, "gregale flags rule-rm needs --id", "flags")
			return 1
		}
		edit = flagsRuleRemove(key, *ruleID)
	case "rm":
		edit = flagsRemove(key)
	}
	version, summary, err := runFlagsEdit(ctx, client, *project, *environment, edit)
	if err != nil {
		return printErr("Flags operation failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(map[string]any{"version": version, "summary": summary}))
	}
	fmt.Fprintf(osStdout, "%s (version %d)\n", summary, version)
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

func findFlag(cfg *flags.Config, key string) (*flags.Flag, error) {
	for i := range cfg.Flags {
		if cfg.Flags[i].Key == key {
			return &cfg.Flags[i], nil
		}
	}
	return nil, fmt.Errorf("flag %q does not exist; create it with gregale flags create %s", key, key)
}

func isVariantFlag(f *flags.Flag) bool { return f.Type == flagsTypeVariant }

// parseFlagValue turns a CLI value into the JSON value for flag f.
func parseFlagValue(f *flags.Flag, raw string) (any, error) {
	if isVariantFlag(f) {
		for _, v := range f.Variants {
			if v.Key == raw {
				return raw, nil
			}
		}
		return nil, fmt.Errorf("%q is not a variant of %s", raw, f.Key)
	}
	switch raw {
	case "true":
		return true, nil
	case "false":
		return false, nil
	}
	return nil, fmt.Errorf("boolean flag %s takes true or false, not %q", f.Key, raw)
}

func flagsCreate(key, variantList, defaultValue, description string, fs *flag.FlagSet) flagsEdit {
	return func(cfg *flags.Config) (string, error) {
		if _, err := findFlag(cfg, key); err == nil {
			return "", fmt.Errorf("flag %q already exists", key)
		}
		f := flags.Flag{Key: key, Description: description, Enabled: true, Default: false, Rules: []flags.Rule{}}
		if variantList != "" {
			keys := splitList(variantList)
			if len(keys) < 2 {
				return "", fmt.Errorf("--variants needs at least two keys")
			}
			f.Type = flagsTypeVariant
			share := 10000 / len(keys)
			for i, k := range keys {
				weight := share
				if i == 0 {
					weight += 10000 - share*len(keys)
				}
				f.Variants = append(f.Variants, flags.FlagVariant{Key: k, Weight: weight})
			}
			f.Default = keys[0]
		}
		if flagSetAny(fs, "default") {
			v, err := parseFlagValue(&f, defaultValue)
			if err != nil {
				return "", err
			}
			f.Default = v
		}
		cfg.Flags = append(cfg.Flags, f)
		return fmt.Sprintf("Created %s (default %v)", key, f.Default), nil
	}
}

func flagsSet(key, defaultValue, description string, fs *flag.FlagSet) flagsEdit {
	return func(cfg *flags.Config) (string, error) {
		f, err := findFlag(cfg, key)
		if err != nil {
			return "", err
		}
		var changed []string
		if flagSetAny(fs, "default") {
			v, err := parseFlagValue(f, defaultValue)
			if err != nil {
				return "", err
			}
			f.Default = v
			changed = append(changed, fmt.Sprintf("default %v", v))
		}
		if flagSetAny(fs, "description") {
			f.Description = description
			changed = append(changed, "description")
		}
		return fmt.Sprintf("Updated %s: %s", key, strings.Join(changed, ", ")), nil
	}
}

func flagsOnOff(key string, on bool) flagsEdit {
	return func(cfg *flags.Config) (string, error) {
		f, err := findFlag(cfg, key)
		if err != nil {
			return "", err
		}
		f.Enabled = on
		if on {
			return fmt.Sprintf("Enabled %s", key), nil
		}
		// A disabled flag serves its default; for a boolean that must be
		// false, or "off" would leave the behavior on (ADR-377).
		if !isVariantFlag(f) && f.Default == true {
			f.Default = false
			return fmt.Sprintf("Disabled %s and set its default to false", key), nil
		}
		return fmt.Sprintf("Disabled %s; it serves its default %v", key, f.Default), nil
	}
}

// flagsKill sets or clears a flag's kill (ADR 20261114150000 §3). A killed
// flag serves the application's fallback on the next request through the
// gateway, whatever its default or rules say.
func flagsKill(key string, killed bool) flagsEdit {
	return func(cfg *flags.Config) (string, error) {
		f, err := findFlag(cfg, key)
		if err != nil {
			return "", err
		}
		if f.Killed == killed {
			if killed {
				return fmt.Sprintf("%s is already killed", key), nil
			}
			return fmt.Sprintf("%s is not killed", key), nil
		}
		f.Killed = killed
		if killed {
			return fmt.Sprintf("Killed %s: requests now get the fallback value in your code", key), nil
		}
		return fmt.Sprintf("Revived %s: it serves its configuration again", key), nil
	}
}

// parseFlagsPercent accepts "12", "12.5" or "12.5%" and returns basis points.
func parseFlagsPercent(raw string) (int, error) {
	s := strings.TrimSuffix(strings.TrimSpace(raw), "%")
	p, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(p) || p < 0 || p > 100 {
		return 0, fmt.Errorf("percentage must be a number from 0 to 100, not %q", raw)
	}
	if i := strings.IndexByte(s, '.'); i >= 0 && len(s)-i-1 > 2 {
		return 0, fmt.Errorf("percentage takes at most two decimals, not %q", raw)
	}
	return int(math.Round(p * 100)), nil
}

func flagsPercent(key string, bp int) flagsEdit {
	return func(cfg *flags.Config) (string, error) {
		f, err := findFlag(cfg, key)
		if err != nil {
			return "", err
		}
		at := slices.IndexFunc(f.Rules, func(r flags.Rule) bool { return r.ID == flagsEveryonePercent })
		if at >= 0 && f.Rules[at].Progression != nil {
			return "", fmt.Errorf("rule %s has a progression plan; advance it with gregale flags promote", flagsEveryonePercent)
		}
		if bp == 0 {
			if at < 0 {
				return fmt.Sprintf("%s has no percentage rollout", key), nil
			}
			f.Rules = slices.Delete(f.Rules, at, at+1)
			return fmt.Sprintf("Removed the percentage rollout of %s", key), nil
		}
		rollout := bp
		rule := flags.Rule{ID: flagsEveryonePercent, Rollout: &rollout}
		if !isVariantFlag(f) {
			rule.Value = true
		}
		if at >= 0 {
			f.Rules = slices.Delete(f.Rules, at, at+1)
		}
		f.Rules = append(f.Rules, rule)
		return fmt.Sprintf("%s now serves %s to %s%% of customers", key, flagsRuleValueText(f, rule), formatBasisPoints(bp)), nil
	}
}

func flagsRuleValueText(f *flags.Flag, r flags.Rule) string {
	if r.Value == nil && isVariantFlag(f) {
		return "weighted variants"
	}
	return fmt.Sprint(r.Value)
}

func formatBasisPoints(bp int) string {
	return strings.TrimSuffix(strings.TrimRight(strconv.FormatFloat(float64(bp)/100, 'f', 2, 64), "0"), ".")
}

// flagsAttributeName expands a short attribute name: country and ua_family
// are the gateway's, any other bare name is the application's.
func flagsAttributeName(name string) string {
	switch {
	case strings.Contains(name, "."):
		return name
	case name == "country" || name == "ua_family":
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
	return flags.Condition{}, fmt.Errorf("--attr %q: use NAME=V1,V2 (in), NAME!=V1,V2 (not in) or NAME$=SUFFIX (ends with); NAME is country, ua_family or an app attribute", raw)
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
			return nil, fmt.Errorf("--attr %q: use NAME=VALUE; NAME is country, ua_family or an app attribute", item)
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

// flagsRuleAdd inserts rule before the everyone-percent rule, so a
// targeted rule wins over the catch-all rollout, or appends it.
func flagsRuleAdd(key string, rule flags.Rule) flagsEdit {
	return func(cfg *flags.Config) (string, error) {
		f, err := findFlag(cfg, key)
		if err != nil {
			return "", err
		}
		if slices.ContainsFunc(f.Rules, func(r flags.Rule) bool { return r.ID == rule.ID }) {
			return "", fmt.Errorf("flag %s already has a rule %q; remove it first with gregale flags rule-rm", key, rule.ID)
		}
		if raw, ok := rule.Value.(string); ok {
			v, err := parseFlagValue(f, raw)
			if err != nil {
				return "", err
			}
			rule.Value = v
		} else if !isVariantFlag(f) {
			rule.Value = true
		}
		at := slices.IndexFunc(f.Rules, func(r flags.Rule) bool { return r.ID == flagsEveryonePercent })
		if at < 0 {
			at = len(f.Rules)
		}
		f.Rules = slices.Insert(f.Rules, at, rule)
		return fmt.Sprintf("Added rule %s to %s at position %d", rule.ID, key, at+1), nil
	}
}

func flagsRuleRemove(key, id string) flagsEdit {
	return func(cfg *flags.Config) (string, error) {
		f, err := findFlag(cfg, key)
		if err != nil {
			return "", err
		}
		at := slices.IndexFunc(f.Rules, func(r flags.Rule) bool { return r.ID == id })
		if at < 0 {
			return "", fmt.Errorf("flag %s has no rule %q", key, id)
		}
		f.Rules = slices.Delete(f.Rules, at, at+1)
		return fmt.Sprintf("Removed rule %s from %s", id, key), nil
	}
}

func flagsRemove(key string) flagsEdit {
	return func(cfg *flags.Config) (string, error) {
		at := slices.IndexFunc(cfg.Flags, func(f flags.Flag) bool { return f.Key == key })
		if at < 0 {
			return "", fmt.Errorf("flag %q does not exist", key)
		}
		cfg.Flags = slices.Delete(cfg.Flags, at, at+1)
		return fmt.Sprintf("Removed %s", key), nil
	}
}

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
		fmt.Fprintf(osStdout, "No flags (version %d). Create one with gregale flags create KEY --project ...\n", snap.Version)
		return 0
	}
	tw := tabwriter.NewWriter(osStdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "KEY\tTYPE\tSTATE\tDEFAULT\tRULES")
	for _, f := range snap.Flags {
		typ := f.Type
		if typ == "" {
			typ = "boolean"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%v\t%d\n", f.Key, typ, flagState(f), f.Default, len(f.Rules))
	}
	if err := tw.Flush(); err != nil {
		return 1
	}
	fmt.Fprintf(osStdout, "version %d\n", snap.Version)
	return 0
}
