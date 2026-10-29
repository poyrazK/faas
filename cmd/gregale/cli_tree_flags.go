package main

// ADR-20261026145832 step 4: one flag per concept. A canonical spelling
// (--env, --release, --cursor, --since, --until, --follow) runs today's flag
// on any command that has an equivalent and lacks the canonical one. The
// equivalent must be the only candidate, and its help must name the concept,
// so `--from` meaning a source environment is never read as a time.

import "strings"

var cliTreeFlagSynonyms = []struct {
	Canonical string
	Old       []string
	Mentions  []string
}{
	{"env", []string{"environment", "scope", "stage"}, []string{"environment", "env "}},
	{"release", []string{"deployment", "deployment-id"}, []string{"deployment", "release", "revision"}},
	{"cursor", []string{"after", "before"}, []string{"cursor"}},
	{"since", []string{"from", "start"}, []string{"rfc3339", "timestamp", "utc day", "window start", "date"}},
	{"until", []string{"to", "end"}, []string{"rfc3339", "timestamp", "utc day", "window end", "date"}},
	{"follow", []string{"watch", "tail"}, []string{"stream", "watch", "poll", "follow"}},
}

// manifestLeafFlags returns the flags of the manifest command or verb args
// name, skipping the data word of a slug-first family.
func manifestLeafFlags(args []string) []cliFlag {
	command, ok := lookupCliCommand(args[0])
	if !ok {
		return nil
	}
	flags, subs, afterPositionals := command.Flags, command.Subcommands, command.SubcommandsAfterPositionals
	i := 1
	for i < len(args) && !strings.HasPrefix(args[i], "-") {
		if afterPositionals {
			afterPositionals = false
			i++
			continue
		}
		sub, ok := findCliSubcommand(subs, args[i])
		if !ok {
			break
		}
		flags, subs, afterPositionals = sub.Flags, sub.Subcommands, sub.SubcommandsAfterPositionals
		i++
	}
	return flags
}

// normalizeTreeFlags rewrites canonical flag spellings to the equivalent
// flag of the command args name.
func normalizeTreeFlags(args []string) []string {
	if len(args) == 0 || args[0] == "help" {
		return args
	}
	flags := manifestLeafFlags(args)
	if len(flags) == 0 {
		return args
	}
	defined := make(map[string]cliFlag, len(flags))
	for _, flag := range flags {
		defined[flag.Name] = flag
	}
	out := append([]string{}, args...)
	for i, arg := range out {
		if arg == "--" {
			break
		}
		if !strings.HasPrefix(arg, "--") {
			continue
		}
		name, value, hasValue := strings.Cut(strings.TrimPrefix(arg, "--"), "=")
		if _, ok := defined[name]; ok {
			continue
		}
		if match, ok := cliTreeFlagEquivalent(defined, name); ok {
			out[i] = "--" + match
			if hasValue {
				out[i] += "=" + value
			}
		}
	}
	return out
}

// cliTreeFlagEquivalent returns the flag a canonical spelling runs among
// defined: the only one of its old spellings whose help names the concept,
// when defined lacks the canonical spelling itself.
func cliTreeFlagEquivalent(defined map[string]cliFlag, canonical string) (string, bool) {
	if _, ok := defined[canonical]; ok {
		return "", false
	}
	for _, synonym := range cliTreeFlagSynonyms {
		if synonym.Canonical != canonical {
			continue
		}
		match, count := "", 0
		for _, old := range synonym.Old {
			flag, ok := defined[old]
			if ok && mentionsAny(strings.ToLower(flag.Short), synonym.Mentions) {
				match, count = old, count+1
			}
		}
		return match, count == 1
	}
	return "", false
}

// cliTreeCanonicalFlags maps each of flags that a canonical spelling runs
// to that spelling (`scope` → `env` on `env set`).
func cliTreeCanonicalFlags(flags []cliFlag) map[string]string {
	defined := make(map[string]cliFlag, len(flags))
	for _, flag := range flags {
		defined[flag.Name] = flag
	}
	canonical := map[string]string{}
	for _, synonym := range cliTreeFlagSynonyms {
		if old, ok := cliTreeFlagEquivalent(defined, synonym.Canonical); ok {
			canonical[old] = synonym.Canonical
		}
	}
	return canonical
}

// cliTreeFlagsForDisplay spells flags as help, the reference and completion
// show them: a flag a canonical spelling runs takes that spelling and names
// its earlier one, which keeps working (ADR-20261026145832 step 4).
func cliTreeFlagsForDisplay(flags []cliFlag) []cliFlag {
	canonical := cliTreeCanonicalFlags(flags)
	if len(canonical) == 0 {
		return flags
	}
	shown := make([]cliFlag, len(flags))
	for i, flag := range flags {
		if name, ok := canonical[flag.Name]; ok {
			flag.Short = strings.TrimSpace(flag.Short + " (also --" + flag.Name + ")")
			flag.Name = name
		}
		shown[i] = flag
	}
	return shown
}

// cliTreeDisplayCommand spells the flags of command and its verbs for
// display (cliTreeFlagsForDisplay).
func cliTreeDisplayCommand(command cliCommand) cliCommand {
	command.Flags = cliTreeFlagsForDisplay(command.Flags)
	command.Subcommands = cliTreeDisplaySubs(command.Subcommands)
	return command
}

func cliTreeDisplaySubs(subs []cliSub) []cliSub {
	if len(subs) == 0 {
		return subs
	}
	shown := make([]cliSub, len(subs))
	for i, sub := range subs {
		sub.Flags = cliTreeFlagsForDisplay(sub.Flags)
		sub.Subcommands = cliTreeDisplaySubs(sub.Subcommands)
		shown[i] = sub
	}
	return shown
}

// cliTreeCanonicalFlag returns the canonical spelling of flag on the
// command args name, in today's or the tree's spelling.
func cliTreeCanonicalFlag(args []string, flag string) (string, bool) {
	if len(args) == 0 {
		return "", false
	}
	today, err := rewriteCliTree(args)
	if err != nil || len(today) == 0 {
		return "", false
	}
	name, ok := cliTreeCanonicalFlags(manifestLeafFlags(today))[flag]
	return name, ok
}

func mentionsAny(text string, words []string) bool {
	for _, word := range words {
		if strings.Contains(text, word) {
			return true
		}
	}
	return false
}
