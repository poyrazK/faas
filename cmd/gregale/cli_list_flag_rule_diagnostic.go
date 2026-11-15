package main

import (
	"bytes"
	"errors"
	"strings"
)

func listFlagRuleDiagnosticForError(err error) *cliFlagRuleDiagnostic {
	var violation *listFlagRuleError
	if !errors.As(err, &violation) {
		return nil
	}
	rule := violation.rule
	rule.When = append([]cliFlagRuleCondition{}, rule.When...)
	rule.Requires = append([]cliFlagRuleCondition{}, rule.Requires...)
	return &cliFlagRuleDiagnostic{Kind: "invalid_flag_combination", Rule: rule,
		Missing: append([]cliFlagRuleCondition{}, violation.missing...), HelpArgv: listFlagRuleHelpArgv(violation.command)}
}

func listFlagRuleHelpArgv(command string) []string {
	argv := []string{"gregale", "--json", "help"}
	commands := offlineCapabilityView().discoverableCommands(visibleCliCommands())
	if document, ok := jsonHelpLookup(cliTreeManifest(commands), strings.Fields(command)); ok {
		return append(argv, strings.Fields(strings.TrimPrefix(document.Command, "gregale "))...)
	}
	return argv
}

func printListFlagRuleUsage(violation *listFlagRuleError) {
	if !jsonOutput || jsonUsageHelp {
		PrintUsage(osStderr, violation.Error(), "cli")
		return
	}
	var original bytes.Buffer
	PrintUsage(&original, violation.Error(), "cli")
	if original.Len() != 0 {
		_ = writeListFlagRuleProblem(osStderr, original.Bytes(), listFlagRuleDiagnosticForError(violation))
	}
}

func jsonHelpFlagRules(node cliSub) []cliFlagRule {
	switch node.output {
	case cliOutputBuildList, cliOutputInvocationList:
		flags := make([]string, 0, len(node.Flags))
		for _, flag := range node.Flags {
			flags = append(flags, flag.Name)
		}
		return visibleListFlagRules(flags)
	default:
		return nil
	}
}
