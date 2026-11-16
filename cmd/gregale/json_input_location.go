package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// The source kind is explicit: a file named "stdin" is still a file.
type jsonInputSource struct {
	kind string
	path string
}

type jsonInputSyntaxError struct {
	flag         string
	source       jsonInputSource
	line, column int
}

func (e *jsonInputSyntaxError) Error() string {
	source := e.source.kind
	if e.source.kind == "file" {
		source = e.source.path
	}
	if !utf8.ValidString(source) || strings.IndexFunc(source, func(r rune) bool { return !unicode.IsPrint(r) }) >= 0 {
		source = strconv.Quote(source)
	}
	return fmt.Sprintf("%s:%d:%d: %s must be valid JSON (inline, @file, or - for stdin)", source, e.line, e.column, e.flag)
}

// Locate invalid JSON without retaining the body or the value-bearing parser
// error. Error() preserves the existing human diagnostic byte for byte.
func jsonFlagSyntaxError(flagName string, source jsonInputSource, data []byte) error {
	position := len(data)
	var raw json.RawMessage
	var syntax *json.SyntaxError
	if err := json.Unmarshal(data, &raw); errors.As(err, &syntax) && syntax.Error() != "unexpected end of JSON input" {
		// SyntaxError.Offset is one-based. An unexpected EOF instead points
		// at the final byte; report the position where more input is needed.
		position = int(min(max(syntax.Offset-1, 0), int64(len(data))))
	}
	prefix := data[:position]
	line := bytes.Count(prefix, []byte{'\n'}) + 1
	column := utf8.RuneCount(prefix[bytes.LastIndexByte(prefix, '\n')+1:]) + 1
	return &jsonInputSyntaxError{flag: flagName, source: source, line: line, column: column}
}
