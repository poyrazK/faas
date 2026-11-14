// adr: 20261112223000

package main

import (
	"fmt"
	"io"
	"slices"
	"strings"
)

// conceptHint is what a word an agent guesses at (`app x listener`, `app
// tcp`, `app network`) means on a command that spells it differently.
// Typo suggestions cannot find these: "listener" is not a misspelling of
// "tcp". The 2026-11-13 eval spent ten commands guessing them.
type conceptHint struct {
	// Sub names a subcommand the hint points at; Flag a flag. The hint is
	// shown only when the command has it, so it never names a missing one.
	Sub, Flag string
	// Then is what follows the verb or flag in the suggested command line.
	Then string
	// Why says what the command or flag does, in the words of the guess.
	Why string
}

// conceptHints maps a guessed word to the commands that do it.
var conceptHints = map[string][]conceptHint{
	"listener":  {{Sub: "tcp", Then: "add --name <name> --guest-port <port>", Why: "a public raw TCP listener; declare the port first with --ports"}, {Flag: "ports", Then: "<name>=tcp:<port>[:internal]", Why: "declare TCP or UDP ports; :internal keeps one private to your apps"}},
	"listeners": {{Sub: "tcp", Then: "list", Why: "list raw TCP listeners"}},
	"port":      {{Flag: "ports", Then: "<name>=tcp:<port>[:internal]", Why: "declare TCP or UDP ports; :internal keeps one private to your apps"}},
	"ports":     {{Flag: "ports", Then: "<name>=tcp:<port>[:internal]", Why: "declare TCP or UDP ports; :internal keeps one private to your apps"}},
	"expose":    {{Flag: "ports", Then: "<name>=tcp:<port>[:internal]", Why: "declare TCP or UDP ports"}},
	"private":   {{Flag: "visibility", Then: "internal", Why: "no public URL; apps in your account still reach it"}},
	"internal":  {{Flag: "visibility", Then: "internal", Why: "no public URL; apps in your account still reach it"}},
	"public":    {{Flag: "no-require-authn", Why: "serve the public URL without a Gregale API key"}, {Flag: "visibility", Then: "public", Why: "give the app a public URL"}},
	"auth":      {{Flag: "public-auth", Then: "open|bearer|basic|ip_allowlist|internal_only", Why: "who may call the public URL"}},
	"healthcheck": {{Sub: "health", Why: "check serving health and readiness"},
		{Flag: "health-path", Then: "<path>", Why: "the path health probes request"}},
	"probe":     {{Flag: "health-path", Then: "<path>", Why: "the path health probes request"}},
	"memory":    {{Flag: "ram", Then: "<MB>", Why: "memory per instance"}},
	"ram":       {{Sub: "scale", Then: "--ram <MB>", Why: "memory per instance"}},
	"redis":     {{Flag: "ports", Then: "redis=tcp:6379:internal", Why: "a Redis you run as an app: declare its port internal; apps reach it at $GREGALE_SERVICE_<APP>_HOST"}},
	"env":       {{Sub: "env", Then: "set KEY=<value>", Why: "set an environment variable"}},
	"secret":    {{Sub: "secrets", Then: "set KEY=<value>", Why: "set a secret"}},
	"forward":   {{Sub: "port-forward", Then: "[LOCAL:]HOST:PORT", Why: "reach a private port from your machine"}},
	"tunnel":    {{Sub: "port-forward", Then: "[LOCAL:]HOST:PORT", Why: "reach a private port from your machine"}},
	"ssh":       {{Sub: "exec", Then: "-- <command>", Why: "run a command against the live deployment"}},
	"shell":     {{Sub: "exec", Then: "-- <command>", Why: "run a command against the live deployment"}},
	"min":       {{Flag: "min", Then: "<N>", Why: "instances kept warm"}},
	"always-on": {{Flag: "min", Then: "1", Why: "keep one instance running"}},
}

// conceptSuggestions returns the command lines that do what got guessed at,
// limited to verbs and flags command declares.
func conceptSuggestions(path string, command cliCommand, got string) []string {
	hints := conceptHints[strings.ToLower(strings.TrimLeft(got, "-"))]
	if len(hints) == 0 {
		return nil
	}
	// Dispatchers pass a trimmed command; the manifest node has the flags.
	node := cliTreeNode{Subs: command.Subcommands, Flags: command.Flags, AfterPositionals: command.SubcommandsAfterPositionals}
	if full, ok := lookupCliPath(strings.Fields(path)); ok {
		node = full
	}
	prefix := "gregale " + cliTreeSpellPath(path)
	if node.AfterPositionals || command.hasSlugFirst() {
		prefix += " <app>"
	}
	var out []string
	for _, hint := range hints {
		var line string
		switch {
		case hint.Sub != "":
			if _, ok := findCliSubcommand(node.Subs, hint.Sub); !ok {
				continue
			}
			line = prefix + " " + hint.Sub
		case hint.Flag != "":
			if !slices.ContainsFunc(node.Flags, func(f cliFlag) bool { return f.Name == hint.Flag }) {
				continue
			}
			line = prefix + " --" + hint.Flag
		}
		if hint.Then != "" {
			line += " " + hint.Then
		}
		out = append(out, line+"   # "+hint.Why)
	}
	return out
}

// printConceptHints writes the suggestions under an unknown-subcommand error.
func printConceptHints(w io.Writer, path string, command cliCommand, got string) {
	lines := conceptSuggestions(path, command, got)
	if len(lines) == 0 {
		return
	}
	_, _ = fmt.Fprintf(w, "  %q is not a subcommand here. Commands that do it:\n", got)
	for _, line := range lines {
		_, _ = fmt.Fprintf(w, "    %s\n", line)
	}
}
