package main

import (
	"fmt"
	"strings"
)

// appWordIsCommand reports a top-level command typed as an `app` verb.
// `gregale app inspect shop` read "inspect" as the app slug and failed with
// "unexpected positional argument(s): shop", never naming the word it could
// not use (prod hunt r30); it now points at the command that exists.
func appWordIsCommand(word string, rest []string) bool {
	if len(rest) == 0 || word == "app" {
		return false
	}
	if _, ok := lookupCliCommand(word); !ok {
		return false
	}
	suggestion := "gregale " + word + " " + strings.Join(rest, " ")
	_ = printErr("Unknown app verb", fmt.Errorf("%q is not an app verb; it is its own command. Did you mean: %s", word, suggestion))
	return true
}
