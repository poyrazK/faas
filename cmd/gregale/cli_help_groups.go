package main

import (
	"fmt"
	"slices"
	"strings"
)

// cliTreeShortcutCommands are the top-level verbs of the daily loop
// (ADR-20261026145832 rule 5), in root help order.
var cliTreeShortcutCommands = []string{
	"init", "dev", "deploy", "logs", "status", "open", "rollback", "run", "invoke", "add",
	"link", "unlink", "login", "logout", "signup", "whoami", "completion", "version",
}

// cliTreeDailyCommands is the first screen of `gregale help`: guided
// onboarding, then the daily loop.
var cliTreeDailyCommands = []string{"start", "menu", "login", "init", "dev", "deploy", "app", "env", "logs", "status", "open"}

// writeCliTreeCommands lists `gregale help --all`: the shortcuts, each
// family's nouns, then customer commands the tree does not place yet.
// Nouns and verbs hidden for the account are left out.
func writeCliTreeCommands(b *strings.Builder, discovery cliTreeDiscovery) {
	b.WriteString("\nShortcuts:\n")
	for _, name := range cliTreeShortcutCommands {
		if command, ok := discovery.commands[name]; ok && command.Audience == cliAudienceCustomer {
			writeRootHelpEntry(b, name, command.Short)
		}
	}
	writeRootHelpEntry(b, "help", "Show command help")
	family := ""
	for _, noun := range cliTreeNouns {
		if len(discovery.entries([]string{noun.Name})) == 0 {
			continue
		}
		if noun.Family != family {
			family = noun.Family
			fmt.Fprintf(b, "\n%s:\n", family)
		}
		writeRootHelpEntry(b, noun.Name, noun.Short)
	}
	printed := false
	for _, command := range customerCliCommands() {
		if _, ok := discovery.commands[command.Name]; !ok || cliTreePlaces(command.Name) {
			continue
		}
		if !printed {
			b.WriteString("\nMore:\n")
			printed = true
		}
		writeRootHelpEntry(b, command.Name, command.Short)
	}
}

// writeCliTreeDailyCommands lists the first screen of `gregale help`.
func writeCliTreeDailyCommands(b *strings.Builder, discovery cliTreeDiscovery) {
	for _, name := range cliTreeDailyCommands {
		if noun, ok := lookupCliTreeNoun(name); ok {
			if len(discovery.entries([]string{name})) > 0 {
				writeRootHelpEntry(b, name, noun.Short)
			}
			continue
		}
		if command, ok := discovery.commands[name]; ok && command.Audience == cliAudienceCustomer {
			writeRootHelpEntry(b, name, command.Short)
		}
	}
	writeRootHelpEntry(b, "help", "Show command help")
}

// writeCliEarlierNames lists today's customer commands the tree renamed,
// each with the nouns that now hold it. They stay accepted.
func writeCliEarlierNames(b *strings.Builder, discovery cliTreeDiscovery) {
	printed := false
	for _, command := range customerCliCommands() {
		if _, ok := discovery.commands[command.Name]; !ok || cliTreeNamesCommand(command.Name) {
			continue
		}
		nouns := cliTreeNounsHolding(command.Name)
		if len(nouns) == 0 {
			continue
		}
		if !printed {
			b.WriteString("\nEarlier names (still accepted):\n")
			printed = true
		}
		writeRootHelpEntry(b, command.Name, "now gregale "+strings.Join(nouns, ", gregale "))
	}
}

// cliTreeNamesCommand reports a top-level name the tree keeps: a shortcut
// or a noun.
func cliTreeNamesCommand(name string) bool {
	_, noun := lookupCliTreeNoun(name)
	return noun || slices.Contains(cliTreeShortcutCommands, name)
}

// cliTreePlaces reports a top-level command the tree keeps or renames.
func cliTreePlaces(name string) bool {
	return cliTreeNamesCommand(name) || len(cliTreeNounsHolding(name)) > 0
}

// cliTreeNounsHolding lists the customer nouns whose paths run command.
func cliTreeNounsHolding(command string) []string {
	var nouns []string
	for _, alias := range cliTreeAliases {
		head := strings.Fields(alias.New)[0]
		if head == "admin" || strings.Fields(alias.Old)[0] != command || slices.Contains(nouns, head) {
			continue
		}
		nouns = append(nouns, head)
	}
	return nouns
}

// rootHelpFlags reports whether args only choose a root help view:
// --all lists every customer command and --operator adds operator commands.
func rootHelpFlags(args []string) (showAll, showOperator, ok bool) {
	for _, arg := range args {
		switch arg {
		case "--all":
			showAll = true
		case "--operator":
			showAll, showOperator = true, true
		case "--help", "-h":
		default:
			return false, false, false
		}
	}
	return showAll, showOperator, true
}

// cliRelatedCommands links command names that are easy to confuse. Command
// help, man pages and the reference list the rest of a command's set under
// "See also".
var cliRelatedCommands = [][]string{
	{"deploy", "deploys", "deployment", "deployments"},
	{"logs", "tail"},
	{"slo", "slos"},
	{"run", "runs"},
	{"crons", "triggers"},
	{"env", "secrets"},
	{"connect", "github"},
	{"plan", "billing", "usage", "invoices", "overage-cap"},
	{"preview", "previews"},
}

// cliSeeAlso returns the members of commands related to name, in set order.
func cliSeeAlso(name string, commands []cliCommand) []cliCommand {
	var related []cliCommand
	for _, set := range cliRelatedCommands {
		if !containsCompletionString(set, name) {
			continue
		}
		for _, other := range set {
			for _, command := range commands {
				if other != name && command.Name == other {
					related = append(related, command)
				}
			}
		}
	}
	return related
}
