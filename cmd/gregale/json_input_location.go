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

// jsonFlagSyntaxError is called only after validation failed. Decode into a
// RawMessage to locate the syntax error without converting numbers or values.
// Neither the payload nor the parser's value-bearing message enters the error.
func jsonFlagSyntaxError(flagName, source string, data []byte) error {
	position := len(data)
	var raw json.RawMessage
	var syntax *json.SyntaxError
	if err := json.Unmarshal(data, &raw); errors.As(err, &syntax) && syntax.Error() != "unexpected end of JSON input" {
		// SyntaxError.Offset is one-based. For an unexpected EOF it points
		// at the final byte; show the position where more input is needed.
		position = int(min(max(syntax.Offset-1, 0), int64(len(data))))
	}
	prefix := data[:position]
	line := bytes.Count(prefix, []byte{'\n'}) + 1
	column := utf8.RuneCount(prefix[bytes.LastIndexByte(prefix, '\n')+1:]) + 1
	if !utf8.ValidString(source) || strings.IndexFunc(source, func(r rune) bool { return !unicode.IsPrint(r) }) >= 0 {
		source = strconv.Quote(source)
	}
	return fmt.Errorf("%s:%d:%d: %s must be valid JSON (inline, @file, or - for stdin)", source, line, column, flagName)
}
