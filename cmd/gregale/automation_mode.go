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

// approvalWaitEnabled makes a request that needs a person's approval wait for
// the decision and retry once (ADR-20261104140058 §6). Set by the
// --wait-for-approval prefix or GREGALE_WAIT_FOR_APPROVAL=1.
var approvalWaitEnabled bool

// projectOrganization sends project commands to a company's projects. Set by
// the --org prefix (`gregale --org acme deploy --project`); a command's own
// --org option keeps its meaning (apps/projects review 2026-11-10, P3).
var projectOrganization string

func extractAutomationFlag(args []string) ([]string, error) {
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--org" || strings.HasPrefix(arg, "--org=") {
			value := strings.TrimPrefix(arg, "--org=")
			if arg == "--org" {
				rest := args[i+1:]
				if len(rest) == 0 {
					return args, errors.New("--org requires an organization slug")
				}
				value = rest[0]
				i++
			}
			if strings.TrimSpace(value) == "" || strings.HasPrefix(value, "-") {
				return args, errors.New("--org requires an organization slug")
			}
			projectOrganization = value
			continue
		}
		handled, err := extractBoolPrefixFlag(arg, "non-interactive", &nonInteractive)
		if !handled && err == nil {
			handled, err = extractBoolPrefixFlag(arg, "wait-for-approval", &approvalWaitEnabled)
		}
		if err != nil {
			return args, err
		}
		if handled {
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

// extractBoolPrefixFlag sets *dst from --name or --name=true|false.
func extractBoolPrefixFlag(arg, name string, dst *bool) (bool, error) {
	flag := "--" + name
	if arg != flag && !strings.HasPrefix(arg, flag+"=") {
		return false, nil
	}
	enabled := true
	if arg != flag {
		value := strings.TrimPrefix(arg, flag+"=")
		if value != "true" && value != "false" {
			return true, errors.New(flag + " accepts true or false")
		}
		enabled, _ = strconv.ParseBool(value)
	}
	*dst = enabled
	return true, nil
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
