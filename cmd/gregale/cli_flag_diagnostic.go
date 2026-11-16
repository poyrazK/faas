package main

import (
	"flag"
	"strings"
)

// cliFlagDiagnostic is local parser metadata, never an API/SDK wire contract.
// Option advice comes from registered flags and the visible manifest; input
// locations identify the source without retaining its body.
type cliFlagDiagnostic struct {
	Kind           string            `json:"kind"`
	Flag           string            `json:"flag,omitempty"`
	SuggestedFlags []string          `json:"suggested_flags,omitempty"`
	ExpectedType   string            `json:"expected_type,omitempty"`
	ValueMarker    string            `json:"value_marker,omitempty"`
	AllowedValues  []string          `json:"allowed_values,omitempty"`
	Placement      string            `json:"placement,omitempty"`
	HelpArgv       []string          `json:"help_argv"`
	Location       *cliInputLocation `json:"location,omitempty"`
}

func flagDiagnosticPath(name string) string {
	if invokedCommandPath != "" {
		return invokedCommandPath
	}
	if fields := strings.Fields(name); len(fields) > 0 {
		return fields[0]
	}
	return ""
}

// Both renderers use the same advice. Human wording stays in its original
// order; the structured advice never contains supplied values or raw argv.
func describeFlagDiagnostic(message string, fs *flag.FlagSet, path string) (string, cliFlagDiagnostic) {
	diagnostic := normalizeFlagDiagnostic(strings.TrimSpace(message))
	advice := cliFlagDiagnostic{Kind: "invalid_flag"}
	if strings.HasPrefix(diagnostic, "unknown flag --") {
		advice.Kind = "unknown_flag"
	}
	if suggestion := suggestUnknownFlag(diagnostic, fs); suggestion != "" {
		advice.SuggestedFlags = []string{"--" + suggestion}
		diagnostic += "\n  Did you mean --" + suggestion + "?"
	}
	value := flagValueGuidance(diagnostic, fs, path)
	if value.kind != "" {
		advice.Kind, advice.Flag = value.kind, "--"+value.name
		advice.ExpectedType, advice.ValueMarker = value.expectedType, value.marker
		advice.AllowedValues = append([]string(nil), value.allowed...)
	}
	if hint := value.humanHint(); hint != "" {
		diagnostic += "\n  " + hint
	}
	// Recognition uses the shared discovery registry, while the human hint
	// retains its existing examples and coverage.
	if name, ok := strings.CutPrefix(normalizeFlagDiagnostic(strings.TrimSpace(message)), "unknown flag --"); ok {
		name, _, _ = strings.Cut(name, "=")
		for _, global := range globalFlagHelpDefinitions() {
			if global.name == name && global.placement == globalFlagBeforeCommand {
				advice.Kind, advice.Flag, advice.Placement = "misplaced_global_flag", global.spellings[0], global.placement
				advice.SuggestedFlags = nil
				break
			}
		}
	}
	if hint := globalFlagPlacementHint(diagnostic, path); hint != "" {
		diagnostic += "\n  " + hint
	}
	if fs != nil {
		if hint := appSettingFlagHint(diagnostic, fs.Name()); hint != "" {
			diagnostic += "\n  " + hint
		}
	}
	return diagnostic, advice
}

// invokedFlagHelpPath retains only a registered command path from the typed
// tree, before dispatch can fold a verb into a legacy handler. It is scoped to
// run() and used only for JSON advice; human diagnostics keep their old path.
var invokedFlagHelpPath string

func flagDiagnosticInvocationPath(args []string) string {
	commands := offlineCapabilityView().discoverableCommands(visibleCliCommands())
	manifest := cliTreeManifest(commands)
	words := leadingTreeWords(args)
	for len(words) > 0 {
		if document, ok := jsonHelpLookup(manifest, words); ok {
			return strings.TrimPrefix(document.Command, "gregale ")
		}
		words = words[:len(words)-1]
	}
	return ""
}

func flagDiagnosticHelpArgv(path, name string) []string {
	argv := []string{"gregale", "--json", "help"}
	commands := offlineCapabilityView().discoverableCommands(visibleCliCommands())
	manifest := cliTreeManifest(commands)
	for _, candidate := range []string{invokedFlagHelpPath, path, flagSetCommandName(name)} {
		candidate = strings.TrimPrefix(spellUsageForTree("gregale "+candidate), "gregale ")
		words := strings.Fields(cliTreeSpellPath(candidate))
		document, ok := jsonHelpLookup(manifest, words)
		if !ok {
			document, ok = jsonHelpLookup(commands, words)
		}
		if ok {
			return append(argv, strings.Fields(strings.TrimPrefix(document.Command, "gregale "))...)
		}
	}
	return argv
}
