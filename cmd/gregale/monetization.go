package main

// adr: 20261018090210 — API monetization is released by a runtime setting
// or, per account, by the api_monetization platform flag
// (ADR-20261020170649). Until it is, the CLI keeps its verbs and flags out of
// every customer surface: help, completions, man pages, the generated
// reference, command suggestions, dispatch and flag parsing. An account it is
// released to sees them once apid's capability view says so
// (api-monetization not hidden), as for the realtime preview; setting
// FAAS_API_MONETIZATION_ENABLED in the shell, the variable that also
// bootstraps the setting in apid, shows them everywhere, including the
// reference.

import (
	"slices"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// monetizationCapability is the capability apid hides from an account API
// monetization is not released to.
const monetizationCapability = "api-monetization"

// monetizationCLI is the static gate: the operator variable in the shell. The
// manifest the public reference and offline error paths read follows it
// alone, so they never depend on who is logged in or wait on the API.
func monetizationCLI() bool { return api.APIMonetizationEnabled() }

// monetizationInView reports whether a capability view shows API
// monetization: the static gate, or an account view apid confirmed (GET
// /v1/capabilities) that does not hide it. Logged out, with no confirmed
// view, or with FAAS_CAPABILITY_FLAGS_RELEASE_ALL (which releases capability
// flags, not this account rollout), it stays hidden.
func monetizationInView(view capabilityView) bool {
	return monetizationCLI() || !view.tier && !view.all && !view.hides(monetizationCapability)
}

// monetizationForAccount is monetizationInView for the logged-in account:
// running a monetization verb or flag follows the account's release instead
// of needing the operator variable in the seller's shell.
func monetizationForAccount() bool { return monetizationInView(discoveryCapabilityView()) }

// monetizationOffline is monetizationInView for usage and error text: the
// run's view or the cached one, never a new API read.
func monetizationOffline() bool { return monetizationInView(offlineCapabilityView()) }

// monetizationVerbs are, per command, the subcommands that belong to API
// monetization.
var monetizationVerbs = map[string][]string{
	"consumers": {"quote", "completeness", "rate-cards", "rate-card-create", "statements", "statement-draft",
		"statement-show", "statement-finalize", "statement-handoff", "statement-export", "plans", "plan-create",
		"plan-update", "set-plan", "plan-history", "usage-alerts", "credits", "credit-grant", "credit-void",
		"spend-cap", "spend-cap-set", "spend-cap-delete", "trial", "trial-set", "trial-end", "billing-policy",
		"billing-policy-set", "billing-policy-delete", "overdue-holds", "signup", "signup-set", "signup-delete", "signups",
		"identities", "identity-delete"},
	"platform-tenants": {"rate-cards", "rate-card-create", "statements", "statement-draft", "statement-show",
		"statement-finalize", "statement-handoff", "statement-export", "plans", "plan-create", "plan-update",
		"set-plan", "plan-history", "trial", "trial-set", "trial-end", "billing-policy", "billing-policy-set",
		"billing-policy-delete", "overdue-holds", "credits", "credit-grant", "credit-void", "spend-cap", "spend-cap-set",
		"spend-cap-delete"},
	"account": {"billing-connection", "billing-run", "invoices"},
	// Seller stats and the listing are API monetization surfaces
	// (ADR-20261126082046, ADR-20261126175800).
	"mcp": {"stats", "listing"},
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
	return slices.Contains(monetizationVerbs[command], verb) && !monetizationForAccount()
}

// hiddenMonetizationFlag reports whether flag is a monetization flag of a
// released verb while monetization is hidden.
func hiddenMonetizationFlag(command, verb, flag string) bool {
	return slices.Contains(monetizationFlags[command+" "+verb], flag) && !monetizationForAccount()
}

// releasedVerbFlags drops hidden monetization flags from a verb's allowed
// flag names.
func releasedVerbFlags(command, verb string, flags []string) []string {
	return slices.DeleteFunc(slices.Clone(flags), func(flag string) bool { return hiddenMonetizationFlag(command, verb, flag) })
}

// releasedUsage drops hidden monetization verbs from the first <a|b> or
// {a|b} group of a usage line.
func releasedUsage(command, usage string) string {
	if monetizationOffline() {
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
		// An example spells today's command (`gregale consumers
		// rate-card-create`) or its tree path (`gregale api consumer
		// rate-card create`); both run today's verb. The alias table is
		// read directly: the manifest lookups of rewriteCliTree come back
		// here.
		fields := strings.Fields(example)
		if len(fields) < 3 {
			return false
		}
		today := fields[1:]
		if alias, used, ok := matchCliTreeAlias(today); ok {
			today = append(strings.Fields(alias.Old), today[used:]...)
		}
		return len(today) > 1 && slices.Contains(hidden, today[1])
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
