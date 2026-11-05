package main

import "strings"

// routesAppFlagAsPositional lets the route commands that take APP as their
// first argument also take --app, like the rest of the CLI: `routes status
// --app h14-py` answered "unknown flag --app" (Hunt 14 H14-17). An --app equal
// to the positional APP is accepted once.
func routesAppFlagAsPositional(args []string) []string {
	out := make([]string, 0, len(args))
	app := ""
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			out = append(out, args[i:]...)
			i = len(args)
			continue
		case (a == "--app" || a == "-app") && i+1 < len(args):
			app = args[i+1]
			i++
			continue
		case strings.HasPrefix(a, "--app=") || strings.HasPrefix(a, "-app="):
			app = a[strings.IndexByte(a, '=')+1:]
			continue
		}
		out = append(out, a)
	}
	if app == "" {
		return args
	}
	if _, positional := splitArgsForFlags(out); len(positional) > 0 && positional[0] == app {
		return out
	}
	return append([]string{app}, out...)
}
