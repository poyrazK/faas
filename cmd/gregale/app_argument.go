package main

import (
	"strings"

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
	at, ok := appArgumentPosition(args)
	if !ok {
		return args, nil
	}
	app, rest := args[at], args[at+1:]
	for i := range rest {
		if rest[i] == "--" {
			break
		}
		value, isApp := appFlagValue(rest, i)
		if !isApp {
			continue
		}
		if value != app {
			return nil, errAppFlagConflict
		}
		return append(append([]string{}, args[:at]...), rest...), nil
	}
	return append(append(append([]string{}, args[:at]...), "--app", app), rest...), nil
}

// appArgumentPosition reports where a bare app argument would sit for args,
// and whether the addressed command takes one.
func appArgumentPosition(args []string) (int, bool) {
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
	if at >= len(args) || !takesAppArgument(flags, positionals, children) {
		return 0, false
	}
	if token := args[at]; strings.HasPrefix(token, "-") || !api.ValidAppSlug(token) {
		return 0, false
	}
	return at, true
}

// takesAppArgument reports whether a leaf names its app only with --app.
func takesAppArgument(flags []cliFlag, positionals []string, children []cliSub) bool {
	if len(positionals) > 0 || len(children) > 0 {
		return false
	}
	for _, flag := range flags {
		if flag.Name == "app" && !flag.Bool {
			return true
		}
	}
	return false
}

// appFlagValue reads an --app flag at rest[i] in any spelling the flag
// package accepts.
func appFlagValue(rest []string, i int) (string, bool) {
	arg := rest[i]
	name, value, hasValue := strings.Cut(strings.TrimLeft(arg, "-"), "=")
	if !strings.HasPrefix(arg, "-") || name != "app" {
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
