package main

import (
	"strings"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
)

// appArgumentEnabled is a test seam: the coverage test turns it off to prove
// each affected command rejected a bare argument before this rule existed.
var appArgumentEnabled = true

// appArgumentAsFlag lets a command that names its app only with --app also
// take the app as its first argument, so `gregale alerts list my-api` runs as
// `gregale alerts list --app my-api`. Slug-first commands such as logs and ps
// already accept --app (mergeAppFlag); this is the other direction.
//
// It applies only to a manifest leaf that declares --app and no positionals,
// and only to a slug-shaped token right after the verb, where it cannot be a
// flag value. Passing the same app both ways is allowed; two different apps
// is a usage error.
func appArgumentAsFlag(args []string) ([]string, error) {
	for _, flag := range []string{"app", "org"} {
		rewritten, err := targetArgumentAsFlag(args, flag)
		if err != nil || !sameArgs(rewritten, args) {
			return rewritten, err
		}
	}
	return args, nil
}

// targetArgumentAsFlag is appArgumentAsFlag for one target flag: --app, or
// --org under orgs (`gregale org keys list my-org`).
func targetArgumentAsFlag(args []string, flag string) ([]string, error) {
	at, ok := appArgumentPosition(args, flag)
	if !ok {
		return args, nil
	}
	app, rest := args[at], args[at+1:]
	for i := range rest {
		if rest[i] == "--" {
			break
		}
		value, isApp := targetFlagValue(rest, i, flag)
		if !isApp {
			continue
		}
		if value != app {
			return nil, errAppFlagConflict
		}
		return append(append([]string{}, args[:at]...), rest...), nil
	}
	return append(append(append([]string{}, args[:at]...), "--"+flag, app), rest...), nil
}

// appArgumentPosition reports where a bare app argument would sit for args,
// and whether the addressed command takes one.
func appArgumentPosition(args []string, target string) (int, bool) {
	if !appArgumentEnabled || len(args) < 2 {
		return 0, false
	}
	command, ok := lookupCliCommand(args[0])
	if !ok || command.SubcommandsAfterPositionals {
		return 0, false
	}
	flags, positionals, children := command.Flags, command.Positionals, command.Subcommands
	at := 1
	for at < len(args) && len(children) > 0 {
		sub, ok := findCliSubcommand(children, args[at])
		if !ok || sub.SubcommandsAfterPositionals {
			return 0, false
		}
		flags, positionals, children = sub.Flags, sub.Positionals, sub.Subcommands
		at++
	}
	if target == "org" && command.Name != "orgs" {
		return 0, false
	}
	if at >= len(args) || !takesAppArgument(flags, positionals, children, target) {
		return 0, false
	}
	if token := args[at]; strings.HasPrefix(token, "-") || !api.ValidAppSlug(token) || isUUIDArgument(token) && namesTarget(args[at+1:], target) {
		return 0, false
	}
	return at, true
}

// namesTarget reports whether rest already passes --<target>. A UUID before
// it is then the command's id, not a second app: `release traffic promote
// <id> --app web` became `--app <id> --app web` and was refused as two apps
// (prod hunt r36). Without --app a UUID still names the app by its ID.
func namesTarget(rest []string, target string) bool {
	for i := range rest {
		if rest[i] == "--" {
			return false
		}
		if _, ok := targetFlagValue(rest, i, target); ok {
			return true
		}
	}
	return false
}

// takesAppArgument reports whether a leaf names its app only with --app.
func takesAppArgument(flags []cliFlag, positionals []string, children []cliSub, target string) bool {
	if len(positionals) > 0 || len(children) > 0 {
		return false
	}
	for _, flag := range flags {
		if flag.Name == target && !flag.Bool {
			return true
		}
	}
	return false
}

// appFlagAsArgument is appArgumentAsFlag's other direction: a command that
// takes its app only as its first argument also takes --app, so
// `gregale bucket list --app my-api` runs as `gregale bucket list my-api`
// (CLI audit 2026-11-05: bucket list, github status and edge cors get refused
// --app while most commands take it).
//
// It applies only to a manifest leaf that declares no --app flag and whose
// first positional names the app (appPositional). The same app both ways is
// allowed; two different apps is a usage error.
func appFlagAsArgument(args []string) ([]string, error) {
	for _, flag := range []string{"app", "org"} {
		rewritten, err := targetFlagAsArgument(args, flag)
		if err != nil || !sameArgs(rewritten, args) {
			return rewritten, err
		}
	}
	return args, nil
}

// targetFlagAsArgument is appFlagAsArgument for one target flag.
func targetFlagAsArgument(args []string, flag string) ([]string, error) {
	at, ok := appPositionalPosition(args, flag)
	if !ok {
		return args, nil
	}
	rest := args[at:]
	for i := range rest {
		if rest[i] == "--" {
			break
		}
		value, isApp := targetFlagValue(rest, i, flag)
		if !isApp {
			continue
		}
		if value == "" || strings.HasPrefix(value, "-") {
			return args, nil // the flag parser reports the missing value
		}
		end := i + 2
		if strings.Contains(rest[i], "=") {
			end = i + 1
		}
		without := append(append([]string{}, rest[:i]...), rest[end:]...)
		if len(without) > 0 && !strings.HasPrefix(without[0], "-") {
			// A UUID is an id (an event, a rule), never an app slug:
			// `dlq get <event-id> --app web` names the app by flag and the
			// event positionally, so the app goes in front of it. It was
			// read as a second app (Hunt 32 H32-22).
			if without[0] != value && isUUIDArgument(without[0]) {
				return append(append(append([]string{}, args[:at]...), value), without...), nil
			}
			if without[0] != value {
				return nil, errAppFlagConflict
			}
			return append(append([]string{}, args[:at]...), without...), nil
		}
		return append(append(append([]string{}, args[:at]...), value), without...), nil
	}
	return args, nil
}

// appPositionalPosition reports where the app argument sits for args when the
// addressed leaf takes its app only as its first positional.
func appPositionalPosition(args []string, target string) (int, bool) {
	at, _, ok := appPositionalLeaf(args, target)
	return at, ok
}

// appPositionalLeaf is appPositionalPosition that also returns the addressed
// leaf, so a caller can read its flags and positionals.
func appPositionalLeaf(args []string, target string) (int, cliSub, bool) {
	if !appArgumentEnabled || len(args) < 2 {
		return 0, cliSub{}, false
	}
	command, ok := lookupCliCommand(args[0])
	if !ok || command.SubcommandsAfterPositionals {
		return 0, cliSub{}, false
	}
	leaf := cliSub{Flags: command.Flags, Positionals: command.Positionals, Subcommands: command.Subcommands}
	at := 1
	for at < len(args) && len(leaf.Subcommands) > 0 {
		sub, ok := findCliSubcommand(leaf.Subcommands, args[at])
		if !ok || sub.SubcommandsAfterPositionals {
			return 0, cliSub{}, false
		}
		leaf = sub
		at++
	}
	if len(leaf.Subcommands) > 0 || len(leaf.Positionals) == 0 || !targetPositional(command.Name, leaf.Positionals[0], target) {
		return 0, cliSub{}, false
	}
	for _, flag := range leaf.Flags {
		if flag.Name == target {
			return 0, cliSub{}, false
		}
	}
	return at, leaf, true
}

// linkedAppAsArgument fills a linked checkout's app into a command whose
// first positional is the app when that is the one positional missing: a
// command run in the app's checkout no longer needs the app spelled out, as
// `gregale logs` and `gregale rollback` already did. Prod hunts r34–r51 found
// command after command printing its usage there (queue, redis, realtime,
// api consumer); this rule covers every such leaf in the manifest.
//
// It is deliberately narrow, like withLinkedApp: the positionals given must
// all be ids (UUIDs, which no app slug is), so a lone word is never shifted
// behind a guessed app; every flag must be one the leaf declares, so a flag
// value is never mistaken for a positional; and an optional or variadic
// positional list is left alone.
func linkedAppAsArgument(args []string) []string {
	at, leaf, ok := appPositionalLeaf(args, "app")
	if !ok {
		return args
	}
	required := 0
	for _, p := range leaf.Positionals {
		if strings.Contains(p, "...") {
			return args
		}
		if !strings.HasPrefix(p, "[") {
			required++
		}
	}
	if required == 0 || strings.HasPrefix(leaf.Positionals[0], "[") {
		return args
	}
	given, ok := leafPositionalArguments(args[at:], leaf.Flags)
	if !ok || len(given) != required-1 {
		return args
	}
	for _, arg := range given {
		if !isUUIDArgument(arg) {
			return args
		}
	}
	app := linkedAppForMissingSlug()
	if app == "" {
		return args
	}
	return append(append(append([]string{}, args[:at]...), app), args[at:]...)
}

// leafPositionalArguments returns the positional arguments in rest, skipping
// the leaf's flags and their values. ok is false when rest holds a flag the
// leaf does not declare, help, or a "--": then which tokens are positionals
// is not known.
func leafPositionalArguments(rest []string, flags []cliFlag) ([]string, bool) {
	var positional []string
	for i := 0; i < len(rest); i++ {
		arg := rest[i]
		if arg == "--" || arg == "-h" || arg == "--help" || arg == "-help" {
			return nil, false
		}
		if !strings.HasPrefix(arg, "-") || arg == "-" {
			positional = append(positional, arg)
			continue
		}
		name, _, hasValue := strings.Cut(strings.TrimLeft(arg, "-"), "=")
		flag, known := leafFlag(flags, name)
		if !known {
			return nil, false
		}
		if !hasValue && !cliFlagIsBool(flag) {
			i++
		}
	}
	return positional, true
}

// leafFlag finds a leaf flag by its name or short name.
func leafFlag(flags []cliFlag, name string) (cliFlag, bool) {
	for _, flag := range flags {
		if flag.Name == name || (flag.ShortName != "" && flag.ShortName == name) {
			return flag, true
		}
	}
	return cliFlag{}, false
}

// cliFlagIsBool reports a switch that takes no value (see cliFlag.Value).
func cliFlagIsBool(flag cliFlag) bool {
	return flag.Bool || (flag.Value == "" && !flag.Req && len(flag.ClosedSet) == 0)
}

// targetPositional reports whether a leaf's first positional names the
// target. For --app: `<app>` anywhere, and `<slug>` under commands whose slug
// is always an app (elsewhere it can name an org, a project or a preview).
// For --org: `<slug>` or `<org>` under orgs.
func targetPositional(command, placeholder, target string) bool {
	name := strings.Trim(placeholder, "[]")
	if target == "org" {
		return command == "orgs" && (name == "<slug>" || name == "<org>")
	}
	switch name {
	case "<app>":
		return true
	case "<slug>":
		return appSlugCommands[command]
	}
	return false
}

// appSlugCommands are today's commands whose `<slug>` positional is the app.
var appSlugCommands = map[string]bool{
	"github": true, "cors": true, "traffic": true, "debug": true, "rollback": true,
	"queue": true, "realtime": true, "dlq": true, "bucket": true, "logs": true,
	"consumers": true,
}

// appFlagValue reads an --app flag at rest[i] in any spelling the flag
// package accepts.
func appFlagValue(rest []string, i int) (string, bool) {
	return targetFlagValue(rest, i, "app")
}

// sameArgs reports whether two argument lists are equal.
func sameArgs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// targetFlagValue reads a --<target> flag at rest[i] in any spelling the
// flag package accepts.
func targetFlagValue(rest []string, i int, target string) (string, bool) {
	arg := rest[i]
	name, value, hasValue := strings.Cut(strings.TrimLeft(arg, "-"), "=")
	if !strings.HasPrefix(arg, "-") || name != target {
		return "", false
	}
	if hasValue {
		return value, true
	}
	if i+1 < len(rest) {
		return rest[i+1], true
	}
	return "", true
}

// isUUIDArgument reports whether arg is a UUID, which no app slug is.
func isUUIDArgument(arg string) bool {
	_, err := uuid.Parse(arg)
	return err == nil && len(arg) == 36
}
