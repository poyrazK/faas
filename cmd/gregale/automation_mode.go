package main

import (
	"errors"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/browser"
)

// Like --profile, this option is a prefix: command arguments and forwarded
// application arguments retain their meaning. run restores state on return.
var nonInteractive bool

func extractAutomationFlag(args []string) ([]string, error) {
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--non-interactive" || strings.HasPrefix(arg, "--non-interactive=") {
			enabled := true
			if arg != "--non-interactive" {
				value := strings.TrimPrefix(arg, "--non-interactive=")
				if value != "true" && value != "false" {
					return args, errors.New("--non-interactive accepts true or false")
				}
				enabled, _ = strconv.ParseBool(value)
			}
			nonInteractive = enabled
			continue
		}
		out = append(out, arg)
		if arg == "--profile" {
			if i+1 < len(args) {
				i++
				out = append(out, args[i])
			}
			continue
		}
		if strings.HasPrefix(arg, "--profile=") || arg == "--json" || arg == "-j" || strings.HasPrefix(arg, "--json=") {
			continue
		}
		return append(out, args[i+1:]...), nil
	}
	return out, nil
}

func requireAutomationConfirmation(approved bool, flag string) int {
	if nonInteractive && !approved {
		return printErr("Confirmation required", errors.New("non-interactive mode requires explicit approval with "+flag))
	}
	return 0
}

func openBrowser(target string) error {
	if nonInteractive {
		return errors.New("browser launch disabled by --non-interactive; open the displayed URL manually")
	}
	return browser.Open(target)
}
