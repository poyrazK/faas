package main_test

import (
	"bytes"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/onebox-faas/faas/cmd/gregale"
)

// enableEveryCLIFeature shows dark-launched commands and verbs, so checks
// over the manifest also cover what those features add.
func enableEveryCLIFeature(t *testing.T) {
	t.Helper()
	t.Setenv("FAAS_APPLICATION_AUTH_ENABLED", "1")
	t.Setenv("FAAS_API_MONETIZATION_ENABLED", "1")
	t.Setenv("FAAS_FRONTEND_HOSTING_ENABLED", "1")
	t.Setenv("FAAS_FULL_ENVIRONMENT_CLONES_ENABLED", "1")
}

func TestCliHelpGroupsCoverCustomerCommands(t *testing.T) {
	enableEveryCLIFeature(t)
	customer := map[string]bool{}
	for _, command := range main.CustomerCliCommands() {
		customer[command.Name] = true
		if group := main.CliHelpGroup(command); group == "" || group == "Other" {
			t.Errorf("customer command %q has no help group; add it to cliHelpGroups", command.Name)
		}
	}
	seen := map[string]string{}
	for _, group := range *main.CliHelpGroups {
		for _, name := range group.Commands {
			if previous, ok := seen[name]; ok {
				t.Errorf("%q is listed in both %q and %q", name, previous, group.Title)
			}
			seen[name] = group.Title
			if !customer[name] {
				t.Errorf("help group %q lists %q, which is not a customer command", group.Title, name)
			}
		}
	}
	for _, command := range main.AdvancedCliCommands() {
		if group := main.CliHelpGroup(command); group != "" {
			t.Errorf("operator command %q is in customer group %q", command.Name, group)
		}
	}
}

// Summaries are the scan line in root help: plain language that fits beside
// the name. Usage syntax belongs in the usage line, flags and examples.
func TestCliCommandSummariesArePlain(t *testing.T) {
	enableEveryCLIFeature(t)
	for _, command := range main.VisibleCliCommands() {
		if n := utf8.RuneCountInString(command.Short); n == 0 || n > 70 {
			t.Errorf("%s: summary is %d characters; keep it to 70: %q", command.Name, n, command.Short)
		}
		for _, syntax := range []string{"|", "--", "gregale ", "<", "ADR-", "slug defaults"} {
			if strings.Contains(command.Short, syntax) {
				t.Errorf("%s: summary contains %q; move usage details to flags, notes or examples: %q", command.Name, syntax, command.Short)
			}
		}
	}
}

// Subcommand summaries follow the same rules; detail goes in Notes.
func TestCliSubcommandSummariesArePlain(t *testing.T) {
	enableEveryCLIFeature(t)
	var walk func(path string, subs []main.CliSub)
	walk = func(path string, subs []main.CliSub) {
		for _, sub := range subs {
			subPath := path + " " + sub.Name
			if n := utf8.RuneCountInString(sub.Short); n == 0 || n > 70 {
				t.Errorf("%s: summary is %d characters; keep it to 70 and move detail to Notes: %q", subPath, n, sub.Short)
			}
			for _, syntax := range []string{"|", "--", "gregale ", "<", "ADR-", "slug defaults"} {
				if strings.Contains(sub.Short, syntax) {
					t.Errorf("%s: summary contains %q; move usage details to flags, notes or examples: %q", subPath, syntax, sub.Short)
				}
			}
			walk(subPath, sub.Subcommands)
		}
	}
	for _, command := range main.VisibleCliCommands() {
		walk(command.Name, command.Subcommands)
	}
}

// ADR numbers are for maintainers; customer help explains behavior instead.
func TestCliHelpTextOmitsADRReferences(t *testing.T) {
	enableEveryCLIFeature(t)
	var check func(path, text string)
	check = func(path, text string) {
		if strings.Contains(text, "ADR-") {
			t.Errorf("%s mentions an ADR: %q", path, text)
		}
	}
	var walk func(path string, subs []main.CliSub)
	checkFlags := func(path string, flags []main.CliFlag) {
		for _, flag := range flags {
			check(path+" --"+flag.Name, flag.Short)
		}
	}
	walk = func(path string, subs []main.CliSub) {
		for _, sub := range subs {
			subPath := path + " " + sub.Name
			check(subPath, sub.Short)
			checkFlags(subPath, sub.Flags)
			walk(subPath, sub.Subcommands)
		}
	}
	for _, command := range main.VisibleCliCommands() {
		check(command.Name, command.Short)
		for _, note := range command.HelpNotes {
			check(command.Name+" note", note)
		}
		checkFlags(command.Name, command.Flags)
		walk(command.Name, command.Subcommands)
	}
}

func TestCliRelatedCommandsCrossReference(t *testing.T) {
	enableEveryCLIFeature(t)
	customer := main.CustomerCliCommands()
	for _, set := range *main.CliRelatedCommands {
		if len(set) < 2 {
			t.Errorf("related set %v needs at least two commands", set)
		}
		for _, name := range set {
			command, ok := main.LookupCliCommand(name)
			if !ok || command.Audience != main.CliAudienceCustomer {
				t.Errorf("related set %v names %q, which is not a customer command", set, name)
				continue
			}
			if got := len(main.CliSeeAlso(name, customer)); got != len(set)-1 {
				t.Errorf("%s: see also lists %d commands, want %d", name, got, len(set)-1)
			}
		}
	}
	oldOut := (*main.OsStdout)
	t.Cleanup(func() { (*main.OsStdout) = oldOut })
	for _, tc := range []struct{ args []string }{
		{[]string{"slo", "--help"}},
		{[]string{"help", "slos"}},
	} {
		var out bytes.Buffer
		(*main.OsStdout) = &out
		if code := main.Run(tc.args); code != 0 {
			t.Fatalf("%v exit = %d", tc.args, code)
		}
		other := "gregale slos"
		if tc.args[1] == "slos" {
			other = "gregale slo"
		}
		if !strings.Contains(out.String(), "See also: "+other+"\n") {
			t.Errorf("%v help does not point at %q:\n%s", tc.args, other, out.String())
		}
	}
	var man bytes.Buffer
	(*main.OsStdout) = &man
	if code := main.Run([]string{"man", "deploys"}); code != 0 {
		t.Fatalf("man deploys exit = %d", code)
	}
	if !strings.Contains(man.String(), ".BR gregale-deploy (1),\n") || !strings.Contains(man.String(), ".BR gregale-deployments (1)\n") {
		t.Errorf("man page SEE ALSO omits related commands:\n%s", man.String())
	}
	var reference bytes.Buffer
	main.RenderMarkdownReference(&reference, customer)
	if !strings.Contains(reference.String(), "See also: [`slos`](#slos).") {
		t.Error("Markdown reference does not cross-reference slo and slos")
	}
}

// Compact help lists only Common verbs; a command with none must not print
// an empty "Common commands:" heading.
func TestCompactHelpOmitsEmptyCommonCommands(t *testing.T) {
	enableEveryCLIFeature(t)
	for _, command := range main.CustomerCliCommands() {
		if !command.CompactHelp || len(command.Subcommands) == 0 {
			continue
		}
		var out bytes.Buffer
		main.PrintLocalCommandHelp(&out, command)
		if strings.Contains(out.String(), "Common commands:\n\n") || strings.HasSuffix(out.String(), "Common commands:\n") {
			t.Errorf("%s compact help prints an empty Common commands section:\n%s", command.Name, out.String())
		}
	}
}

// Detail moved out of a subcommand summary stays visible in its help.
func TestSubcommandNotesAppearInHelp(t *testing.T) {
	var out bytes.Buffer
	oldOut := *main.OsStdout
	*main.OsStdout = &out
	t.Cleanup(func() { *main.OsStdout = oldOut })
	if code := main.Run([]string{"domains", "verify", "--help"}); code != 0 {
		t.Fatalf("domains verify --help = %d", code)
	}
	if !strings.Contains(out.String(), "Check DNS and certificate verification status") || !strings.Contains(out.String(), "Exits nonzero while pending.") {
		t.Fatalf("help lacks the summary or its note:\n%s", out.String())
	}
}
