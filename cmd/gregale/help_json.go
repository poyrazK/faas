package main

import (
	"fmt"
	"slices"
	"strings"
)

// JSON help describes the installed manifest, not parser-inferred types or
// defaults. Version 1 keeps absent collections as arrays and lists immediate
// children; callers request a child's command to discover its full contract.
type jsonHelpDocument struct {
	SchemaVersion               int               `json:"schema_version"`
	Command                     string            `json:"command"`
	RequestedCommand            string            `json:"requested_command"`
	Description                 string            `json:"description"`
	DocsURL                     string            `json:"docs_url"`
	Aliases                     []string          `json:"aliases"`
	Positionals                 []string          `json:"positionals"`
	PositionalChoices           []string          `json:"positional_choices"`
	Flags                       []jsonHelpFlag    `json:"flags"`
	Examples                    []string          `json:"examples"`
	Notes                       []string          `json:"notes"`
	Subcommands                 []jsonHelpCommand `json:"subcommands"`
	SubcommandsAfterPositionals bool              `json:"subcommands_after_positionals"`
	FlagsAfterPositionals       bool              `json:"flags_after_positionals"`
}

type jsonHelpFlag struct {
	Name        string   `json:"name"`
	Spellings   []string `json:"spellings"`
	Description string   `json:"description"`
	TakesValue  bool     `json:"takes_value"`
	Value       string   `json:"value"`
	Required    bool     `json:"required"`
	Repeatable  bool     `json:"repeatable"`
	Choices     []string `json:"choices"`
	Group       string   `json:"group"`
}

type jsonHelpCommand struct {
	Command     string `json:"command"`
	Description string `json:"description"`
}

func runJSONHelp(args []string) int {
	words, operator, err := jsonHelpArguments(args)
	if err != nil {
		return printErr("Invalid help request", err)
	}
	// Read the existing account cache or tier defaults without refreshing it.
	// Discovery must work offline, including with broken saved configuration.
	commands := offlineCapabilityView().discoverableCommands(visibleCliCommands())
	manifest := cliTreeManifest(commands)
	if len(words) == 0 {
		root := jsonHelpRoot(manifest, commands, operator)
		return jsonOut(writeJSON(root))
	}
	canonical := strings.Fields(cliTreeSpellPath(strings.Join(manifestVerbNames(words), " ")))
	document, ok := jsonHelpLookup(manifest, canonical)
	if !ok {
		// Operator and compatibility topics remain directly inspectable, as
		// in human help, but are listed at root only with --operator.
		document, ok = jsonHelpLookup(commands, canonical)
	}
	if !ok {
		return printErr("Unknown help topic", fmt.Errorf("gregale: unknown help topic %q; run 'gregale --json help' for commands", strings.Join(words, " ")))
	}
	document.RequestedCommand = "gregale " + strings.Join(words, " ")
	document.Aliases = jsonHelpAliases(commands, document.Command)
	return jsonOut(writeJSON(document))
}

func jsonHelpArguments(args []string) ([]string, bool, error) {
	var words []string
	operator, literal := false, false
	for _, arg := range args {
		switch {
		case literal:
			words = append(words, arg)
		case arg == "--":
			literal = true
		case arg == "--operator":
			operator = true
		case arg == "--all", arg == "--help", arg == "-h":
			// Structured help always describes every visible local option.
		case strings.HasPrefix(arg, "-"):
			return nil, false, fmt.Errorf("gregale help: unknown option %q", arg)
		default:
			words = append(words, arg)
		}
	}
	if operator && len(words) > 0 {
		return nil, false, fmt.Errorf("gregale help: --operator selects the root command list; omit the help topic")
	}
	return words, operator, nil
}

func jsonHelpRoot(manifest, commands []cliCommand, operator bool) jsonHelpDocument {
	document := jsonHelpNode("gregale", "Deploy apps and functions that scale to zero", "cli", cliSub{})
	for _, command := range manifest {
		document.Subcommands = append(document.Subcommands, jsonHelpCommand{"gregale " + command.Name, command.Short})
	}
	if operator {
		for _, command := range commands {
			listed := slices.ContainsFunc(manifest, func(shown cliCommand) bool { return shown.Name == command.Name })
			if !listed && cliCommandAvailable(command.Name) {
				document.Subcommands = append(document.Subcommands, jsonHelpCommand{"gregale " + command.Name, command.Short})
			}
		}
	}
	return document
}

func jsonHelpLookup(commands []cliCommand, words []string) (jsonHelpDocument, bool) {
	if len(words) == 0 {
		return jsonHelpDocument{}, false
	}
	for _, command := range commands {
		if command.Name != words[0] || !cliCommandAvailable(command.Name) {
			continue
		}
		node := cliSub{Name: command.Name, Short: command.Short, Notes: command.HelpNotes,
			Positionals: command.Positionals, Flags: command.Flags, Examples: command.Examples,
			Subcommands: command.Subcommands, SubcommandsAfterPositionals: command.SubcommandsAfterPositionals}
		path := "gregale " + command.Name
		for _, word := range words[1:] {
			child, ok := findCliSubcommand(node.Subcommands, word)
			if !ok {
				return jsonHelpDocument{}, false
			}
			node, path = child, path+" "+child.Name
		}
		document := jsonHelpNode(path, node.Short, command.DocSlug, node)
		if len(words) == 1 {
			document.PositionalChoices = append([]string{}, command.ClosedSet...)
		}
		return document, true
	}
	return jsonHelpDocument{}, false
}

func jsonHelpNode(path, description, topic string, node cliSub) jsonHelpDocument {
	document := jsonHelpDocument{
		SchemaVersion: 1, Command: path, RequestedCommand: path, Description: description,
		DocsURL: docsURLForTopic(topic), Aliases: []string{},
		Positionals: append([]string{}, node.Positionals...), PositionalChoices: []string{},
		Flags: []jsonHelpFlag{}, Examples: append([]string{}, node.Examples...), Notes: append([]string{}, node.Notes...),
		Subcommands: []jsonHelpCommand{}, SubcommandsAfterPositionals: node.SubcommandsAfterPositionals,
		FlagsAfterPositionals: node.FlagsAfterPositionals,
	}
	for _, flag := range cliTreeFlagsForDisplay(node.Flags) {
		document.Flags = append(document.Flags, jsonHelpFlag{
			Name: flag.Name, Spellings: cliFlagSpellings(flag), Description: flag.Short,
			TakesValue: !flag.Bool && (flag.Value != "" || flag.Req || len(flag.ClosedSet) > 0),
			Value:      flag.Value, Required: flag.Req, Repeatable: flag.Repeatable,
			Choices: append([]string{}, flag.ClosedSet...), Group: flag.Group,
		})
	}
	for _, child := range node.Subcommands {
		document.Subcommands = append(document.Subcommands, jsonHelpCommand{path + " " + child.Name, child.Short})
	}
	return document
}

// Compatibility paths come from the same filtered manifest and canonical
// rewriter used by dispatch. Never advertise an unfiltered hidden alias.
func jsonHelpAliases(commands []cliCommand, canonical string) []string {
	aliases := []string{}
	path := strings.TrimPrefix(canonical, "gregale ")
	add := func(path string) {
		if "gregale "+path != canonical && "gregale "+cliTreeSpellPath(path) == canonical && !slices.Contains(aliases, "gregale "+path) {
			aliases = append(aliases, "gregale "+path)
		}
	}
	// Only the selected path and its table prefixes can name this command.
	// Walking every manifest leaf through the rewriter made leaf discovery
	// take seconds and repeated the same work for unrelated command families.
	if old, err := rewriteTreePath(strings.Fields(path)); err == nil {
		if _, visible := jsonHelpLookup(commands, old); visible {
			add(strings.Join(old, " "))
			jsonHelpSubAliases(commands, old, add)
		}
	}
	for _, alias := range cliTreeAliases {
		if path == alias.New {
			add(alias.Old)
		} else if rest, ok := strings.CutPrefix(path, alias.New+" "); ok {
			add(alias.Old + " " + rest)
		}
	}
	slices.Sort(aliases)
	return aliases
}

func jsonHelpSubAliases(commands []cliCommand, path []string, add func(string)) {
	for _, command := range commands {
		if command.Name != path[0] {
			continue
		}
		prefixes, subs := []string{command.Name}, command.Subcommands
		for _, word := range path[1:] {
			sub, ok := findCliSubcommand(subs, word)
			if !ok {
				return
			}
			var next []string
			for _, prefix := range prefixes {
				for _, name := range sub.completionSpellings() {
					next = append(next, prefix+" "+name)
				}
			}
			prefixes, subs = next, sub.Subcommands
		}
		for _, spelling := range prefixes {
			add(spelling)
		}
		return
	}
}
