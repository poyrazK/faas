package main

import (
	"errors"
	"strings"
)

const (
	cliUnknownCommand   = "unknown_command"
	cliUnknownHelpTopic = "unknown_help_topic"
)

// cliCommandError retains the existing human message separately from advice
// derived from registered commands. Raw words never enter the advice.
type cliCommandError struct {
	message string
	advice  *cliFlagDiagnostic
}

func (e *cliCommandError) Error() string { return e.message }

func commandDiagnosticError(message, kind string, words []string) error {
	if !jsonOutput {
		return errors.New(message)
	}
	return &cliCommandError{message: message, advice: describeCommandDiagnostic(kind, words)}
}

func commandErrorDiagnostic(err error) *cliFlagDiagnostic {
	var command *cliCommandError
	if errors.As(err, &command) {
		return command.advice
	}
	return nil
}

// JSON correction is an offline error path. Human suggestions retain their
// existing discovery behavior and wording.
func rootCommandSuggestion(name string) (string, bool) {
	if !jsonOutput {
		return suggestCommand(name)
	}
	commands := offlineCapabilityView().discoverableCommands(visibleCliCommands())
	return suggestCommandFrom(name, cliTreeManifest(commands), commands)
}

func describeCommandDiagnostic(kind string, words []string) *cliFlagDiagnostic {
	commands := offlineCapabilityView().discoverableCommands(visibleCliCommands())
	manifest := cliTreeManifest(commands)
	words = strings.Fields(cliTreeSpellPath(strings.Join(manifestVerbNames(words), " ")))
	return commandDiagnosticFrom(kind, words, manifest)
}

// Walk only command nodes. Never copy a supplied target, flag, value or suffix
// into a correction, and never resolve a tie by choosing an arbitrary command.
func commandDiagnosticFrom(kind string, words []string, manifest []cliCommand) *cliFlagDiagnostic {
	advice := &cliFlagDiagnostic{Kind: kind, HelpArgv: []string{"gregale", "--json", "help"}}
	if len(words) == 0 {
		return advice
	}
	var parent cliCommand
	for _, command := range manifest {
		if command.Name == words[0] {
			parent = command
			break
		}
	}
	if parent.Name == "" {
		if suggestion, ok := suggestCommandFrom(words[0], manifest, nil); ok {
			setCommandCorrection(advice, strings.Fields(suggestion), manifest)
		}
		return advice
	}
	path := []string{parent.Name}
	advice.HelpArgv = append(advice.HelpArgv, path...)
	for _, word := range words[1:] {
		sub, ok := findCliSubcommand(parent.Subcommands, word)
		if !ok {
			if suggestion, found := suggestSubcommand(word, parent); found {
				setCommandCorrection(advice, append(path, suggestion), manifest)
			}
			return advice
		}
		path = append(path, sub.Name)
		advice.HelpArgv = append([]string{"gregale", "--json", "help"}, path...)
		parent = cliCommand{Subcommands: sub.Subcommands}
	}
	return advice
}

func setCommandCorrection(advice *cliFlagDiagnostic, path []string, manifest []cliCommand) {
	document, ok := jsonHelpLookup(manifest, path)
	if !ok {
		return
	}
	canonical := strings.Fields(strings.TrimPrefix(document.Command, "gregale "))
	advice.HelpArgv = append([]string{"gregale", "--json", "help"}, canonical...)
	correction := append([]string{"gregale"}, canonical...)
	if advice.Kind == cliUnknownHelpTopic {
		correction = append([]string(nil), advice.HelpArgv...)
	}
	advice.SuggestedCommands = [][]string{correction}
}
