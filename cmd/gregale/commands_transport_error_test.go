package main

import (
	"errors"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestTransportProblemTruncatesUTF8Safely(t *testing.T) {
	problem := transportProblem(errors.New(strings.Repeat("é", 119) + "€" + strings.Repeat("é", 20)))
	if !utf8.ValidString(problem.Detail) {
		t.Fatalf("transport error detail is not valid UTF-8: %q", problem.Detail)
	}
	if !strings.HasSuffix(problem.Detail, "...") {
		t.Fatalf("transport error detail was not truncated: %q", problem.Detail)
	}
}
