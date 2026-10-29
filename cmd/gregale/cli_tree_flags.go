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
		for _, synonym := range cliTreeFlagSynonyms {
			if synonym.Canonical != name {
				continue
			}
			match, count := "", 0
			for _, old := range synonym.Old {
				flag, ok := defined[old]
				if ok && mentionsAny(strings.ToLower(flag.Short), synonym.Mentions) {
					match, count = old, count+1
				}
			}
			if count != 1 {
				continue
			}
			out[i] = "--" + match
			if hasValue {
				out[i] += "=" + value
			}
		}
	}
	return out
}

func mentionsAny(text string, words []string) bool {
	for _, word := range words {
		if strings.Contains(text, word) {
			return true
		}
	}
	return false
}
