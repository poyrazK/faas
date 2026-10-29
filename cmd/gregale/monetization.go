package main

// adr: 20261018090210 — API monetization is released by a runtime setting.
// Until it is, the CLI keeps its verbs and flags out of every customer
// surface: help, completions, man pages, the generated reference, command
// suggestions, dispatch and flag parsing. Setting
// FAAS_API_MONETIZATION_ENABLED in the shell, the variable that also
// bootstraps the setting in apid, shows them.

import (
	"slices"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

func monetizationCLI() bool { return api.APIMonetizationEnabled() }

// monetizationVerbs are, per command, the subcommands that belong to API
// monetization.
var monetizationVerbs = map[string][]string{
	"consumers": {"quote", "completeness", "rate-cards", "rate-card-create", "statements", "statement-draft",
		"statement-show", "statement-finalize", "statement-handoff", "statement-export", "plans", "plan-create",
		"plan-update", "set-plan", "plan-history", "usage-alerts", "credits", "credit-grant", "credit-void",
		"spend-cap", "spend-cap-set", "spend-cap-delete", "trial", "trial-set", "trial-end", "billing-policy",
		"billing-policy-set", "billing-policy-delete", "overdue-holds", "signup", "signup-set", "signup-delete", "signups"},
	"platform-tenants": {"rate-cards", "rate-card-create", "statements", "statement-draft", "statement-show",
		"statement-finalize", "statement-handoff", "statement-export", "plans", "plan-create", "plan-update",
		"set-plan", "plan-history", "trial", "trial-set", "trial-end", "billing-policy", "billing-policy-set",
		"billing-policy-delete", "overdue-holds", "credits", "credit-grant", "credit-void", "spend-cap", "spend-cap-set",
		"spend-cap-delete"},
	"account": {"billing-connection", "billing-run", "invoices"},
}

// monetizationFlags are monetization flags of released verbs, keyed by
// "command verb".
var monetizationFlags = map[string][]string{"consumers create": {"trial-days"}}

// hiddenMonetizationFlagText replaces released flag descriptions that name
// monetization while it is hidden, keyed by "command verb --flag".
var hiddenMonetizationFlagText = map[string]string{
	"consumers key-create --scopes": "comma-separated scopes: read, write, admin (default write)",
}

// hiddenMonetizationVerb reports whether verb is a monetization subcommand of
// command while monetization is hidden.
func hiddenMonetizationVerb(command, verb string) bool {
	return !monetizationCLI() && slices.Contains(monetizationVerbs[command], verb)
}

// hiddenMonetizationFlag reports whether flag is a monetization flag of a
// released verb while monetization is hidden.
func hiddenMonetizationFlag(command, verb, flag string) bool {
	return !monetizationCLI() && slices.Contains(monetizationFlags[command+" "+verb], flag)
}

// releasedVerbFlags drops hidden monetization flags from a verb's allowed
// flag names.
func releasedVerbFlags(command, verb string, flags []string) []string {
	return slices.DeleteFunc(slices.Clone(flags), func(flag string) bool { return hiddenMonetizationFlag(command, verb, flag) })
}

// releasedUsage drops hidden monetization verbs from the first <a|b> or
// {a|b} group of a usage line.
func releasedUsage(command, usage string) string {
	if monetizationCLI() {
		return usage
	}
	open := strings.IndexAny(usage, "<{")
	if open < 0 {
		return usage
	}
	end := strings.IndexAny(usage[open:], ">}")
	if end < 0 {
		return usage
	}
	verbs := slices.DeleteFunc(strings.Split(usage[open+1:open+end], "|"), func(verb string) bool {
		return slices.Contains(monetizationVerbs[command], verb)
	})
	return usage[:open+1] + strings.Join(verbs, "|") + usage[open+end:]
}

// withoutMonetization is command as the CLI shows it while monetization is
// hidden. It never modifies the shared manifest.
func withoutMonetization(command cliCommand) cliCommand {
	hidden := monetizationVerbs[command.Name]
	command.Examples = slices.DeleteFunc(slices.Clone(command.Examples), func(example string) bool {
		// The verb follows today's command (`gregale consumers <verb>`) or
		// its tree path (`gregale api consumer <verb>`).
		fields := strings.Fields(example)
		return len(fields) > 2 && slices.ContainsFunc(fields[2:min(len(fields), 4)], func(word string) bool { return slices.Contains(hidden, word) })
	})
	if command.Subcommands == nil {
		return command
	}
	subs := make([]cliSub, 0, len(command.Subcommands))
	for _, sub := range command.Subcommands {
		if slices.Contains(hidden, sub.Name) {
			continue
		}
		flags := make([]cliFlag, 0, len(sub.Flags))
		for _, f := range sub.Flags {
			if slices.Contains(monetizationFlags[command.Name+" "+sub.Name], f.Name) {
				continue
			}
			if text, ok := hiddenMonetizationFlagText[command.Name+" "+sub.Name+" --"+f.Name]; ok {
				f.Short = text
			}
			flags = append(flags, f)
		}
		if sub.Flags != nil {
			sub.Flags = flags
		}
		subs = append(subs, sub)
	}
	command.Subcommands = subs
	return command
}
