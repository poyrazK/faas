package main

import (
	"errors"
	"fmt"
	"strings"
)

// DocCommandCheck is what CheckDocCommand found for one command line a
// customer page writes (cli_docs_commands_test.go).
type DocCommandCheck struct {
	// Problem says why the command line does not name a command.
	Problem string
	// Args is the line as today's command: what a probe runs.
	Args []string
	// Path is today's command path, without positionals.
	Path []string
	// Unlisted are the long flags the manifest does not list for the command.
	Unlisted []string
}

// CheckDocCommand resolves a documented command line (the words after
// `gregale`, then its flags and their values) through the command tree to
// today's command, and lists the flags its manifest entry leaves out.
func CheckDocCommand(args []string) DocCommandCheck {
	if len(args) == 0 || args[0] == "help" {
		return DocCommandCheck{}
	}
	words := leadingTreeWords(args)
	if _, ok := cliTreeHelpTopic(words); ok && len(words) == len(args) {
		return DocCommandCheck{} // a noun or subnoun: `gregale release`
	}
	rewritten, err := rewriteCliTree(args)
	if errors.Is(err, errAppTarget) && args[0] == appSlugFallback {
		// A slug-first command written for a linked checkout.
		rewritten, err = rewriteCliTree(append([]string{appSlugFallback, "docs-app"}, args[1:]...))
	}
	if errors.As(err, new(errFoldFlagRequired)) {
		return DocCommandCheck{} // a path named in prose, without the flag a run needs
	}
	if err != nil {
		return DocCommandCheck{Problem: err.Error(), Args: args}
	}
	rewritten = normalizeTreeFlags(rewritten) // --env, --release: the tree's spellings
	check := DocCommandCheck{Args: rewritten, Path: docCommandPath(rewritten)}
	if problem := docCommandPathProblem(rewritten); problem != "" {
		check.Problem = problem
		return check
	}
	listed := map[string]bool{}
	for _, f := range manifestLeafFlags(rewritten) {
		listed[f.Name] = true
		if f.ShortName != "" {
			listed[f.ShortName] = true
		}
	}
	for _, arg := range rewritten[1:] {
		name, ok := strings.CutPrefix(arg, "--")
		if !ok || name == "" {
			continue
		}
		name, _, _ = strings.Cut(name, "=")
		if !listed[name] && !docGlobalFlags[name] && !strings.HasPrefix(name, "-") {
			check.Unlisted = append(check.Unlisted, name)
		}
	}
	return check
}

// docCommandPathProblem reports a command or verb the manifest does not
// have. A word in a verb's place that is not one of the verbs is an unknown
// verb unless that level also takes positionals (`org members ORG_ID`).
func docCommandPathProblem(args []string) string {
	command, ok := lookupCliCommand(args[0])
	if !ok {
		return fmt.Sprintf("unknown command %q", args[0])
	}
	subs, afterPositionals, positionals := command.Subcommands, command.SubcommandsAfterPositionals, len(command.Positionals) > 0
	path := []string{args[0]}
	for _, word := range args[1:] {
		if strings.HasPrefix(word, "-") || len(subs) == 0 {
			return ""
		}
		if afterPositionals {
			afterPositionals = false
			continue
		}
		sub, ok := findCliSubcommand(subs, word)
		if !ok {
			if positionals {
				return ""
			}
			return fmt.Sprintf("%s has no verb %q", strings.Join(path, " "), word)
		}
		path = append(path, word)
		subs, afterPositionals, positionals = sub.Subcommands, sub.SubcommandsAfterPositionals, len(sub.Positionals) > 0
	}
	return ""
}

// docCommandPath is the command and verbs args name in the manifest.
func docCommandPath(args []string) []string {
	path := args[:1]
	for _, word := range args[1:] {
		if strings.HasPrefix(word, "-") {
			break
		}
		next := append(append([]string{}, path...), word)
		if _, ok := lookupCliPath(next); !ok {
			continue // a positional (`app <slug> scale`)
		}
		path = next
	}
	return path
}

// docGlobalFlags are accepted with any command (run's global options).
var docGlobalFlags = map[string]bool{
	"json": true, "profile": true, "org": true, "agent-reason": true,
	"http-retries": true, "http-timeout": true, "http-debug": true,
	"non-interactive": true, "wait-for-approval": true, "help": true, "all": true,
}
