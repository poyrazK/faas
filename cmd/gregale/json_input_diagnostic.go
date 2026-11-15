package main

import (
	"errors"
	"unicode/utf8"
)

// A sensitive or unrepresentable filename is omitted instead of advertising
// a redacted string as an actionable filesystem path.
type cliInputLocation struct {
	Source string `json:"source"`
	Path   string `json:"path,omitempty"`
	Line   int    `json:"line"`
	Column int    `json:"column"`
}

func jsonInputDiagnostic(err error) *cliFlagDiagnostic {
	var input *jsonInputSyntaxError
	if !errors.As(err, &input) {
		return nil
	}
	location := &cliInputLocation{Source: input.source.kind, Line: input.line, Column: input.column}
	if input.source.kind == "file" && utf8.ValidString(input.source.path) && redactErrorText(input.source.path) == input.source.path {
		location.Path = input.source.path
	}
	advice := &cliFlagDiagnostic{
		Kind: "invalid_json", Location: location,
		HelpArgv: flagDiagnosticHelpArgv(invokedCommandPath, ""),
	}
	if utf8.ValidString(input.flag) && redactErrorText(input.flag) == input.flag {
		advice.Flag = input.flag
	}
	return advice
}
