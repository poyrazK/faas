package main

import (
	"errors"
	"flag"
	"fmt"
)

// Append scalar overrides after the saved filters so the normal query parser
// validates the effective query and handles explicit false/empty values.
func logViewQueryArgs(fs *flag.FlagSet, view logView) ([]string, map[string]bool, error) {
	overrides := map[string]bool{}
	if logsFlagWasSet(fs, "clear-status") && fs.Lookup("clear-status").Value.(flag.Getter).Get().(bool) {
		if view.Source != logsSourceHTTP || view.LatestRequestError {
			return nil, nil, errors.New("--clear-status requires an ordinary HTTP view; runtime and latest-request-error views are not supported")
		}
		if logsFlagWasSet(fs, "status") {
			return nil, nil, errors.New("--clear-status cannot be combined with --status; choose removing the saved filter or an explicit HTTP status")
		}
		view.Status = 0
		overrides["status"], overrides["clear-status"] = true, true
	}
	for _, filter := range []struct {
		name   string
		value  *string
		source string
	}{
		{"grep", &view.Grep, logsSourceRuntime},
		{"level", &view.Level, logsSourceRuntime},
		{"route", &view.Route, logsSourceHTTP},
	} {
		option := "clear-" + filter.name
		if !logsFlagWasSet(fs, option) || !fs.Lookup(option).Value.(flag.Getter).Get().(bool) {
			continue
		}
		if view.Source != filter.source {
			return nil, nil, fmt.Errorf("--%s requires a %s view", option, filter.source)
		}
		if logsFlagWasSet(fs, filter.name) {
			return nil, nil, fmt.Errorf("--%s cannot be combined with --%s; choose clearing or setting the filter", option, filter.name)
		}
		*filter.value = ""
		overrides[filter.name], overrides[option] = true, true
	}
	args := view.args()
	var invalid bool
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "view", "saved-view", "app", "dry-run", "clear-status", "clear-grep", "clear-level", "clear-route":
			return
		case "since":
		case "route":
			invalid = invalid || view.Source != logsSourceHTTP
		case "status", "limit":
			invalid = invalid || view.Source != logsSourceHTTP || view.LatestRequestError
		case "grep", "level", "follow":
			invalid = invalid || view.Source != logsSourceRuntime || view.LatestRequestError
		default:
			invalid = true
			return
		}
		overrides[f.Name] = true
		args = append(args, "--"+f.Name+"="+f.Value.String())
	})
	if invalid {
		return nil, nil, errors.New("override only compatible saved-view filters: runtime supports --since/--grep/--level/--follow; HTTP supports --since/--route/--status/--limit; latest-request-error supports only --since/--route. App targeting and --dry-run are also supported; the view type cannot be changed")
	}
	return args, overrides, nil
}
