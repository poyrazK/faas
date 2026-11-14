// adr: 20261112223000

package main

import (
	"fmt"
	"slices"
	"strings"
)

// ResolveHintCommand checks that a feature-hint command line names a real
// command and only flags that command declares (ADR-20261112223000 §1).
// Words in <angle brackets> are placeholders and positional values.
func ResolveHintCommand(line string) error {
	words := strings.Fields(line)
	if len(words) < 2 || words[0] != "gregale" {
		return fmt.Errorf("%q: must start with gregale and a command", line)
	}
	for i, word := range words {
		if word == "<app>" || word == "<slug>" {
			words[i] = sampleAppSlug // the tree's app verbs want a slug-shaped target
		}
	}
	args, err := rewriteCliTree(words[1:])
	if err != nil {
		return fmt.Errorf("%q: %w", line, err)
	}
	if at := slices.Index(args, "--"); at >= 0 {
		args = args[:at] // what follows -- is the command's payload
	}
	var leading []string
	for _, word := range args {
		if strings.HasPrefix(word, "-") {
			break
		}
		// A <placeholder> after a slug-first command stands for its app.
		if (strings.HasPrefix(word, "<") || word == sampleAppSlug) && len(leading) > 0 {
			if node, ok := lookupCliPath(leading); ok && node.AfterPositionals {
				continue
			}
		}
		leading = append(leading, word)
	}
	var node cliTreeNode
	found := false
	for n := len(leading); n > 0 && !found; n-- {
		node, found = lookupCliPath(leading[:n])
	}
	if !found {
		return fmt.Errorf("%q: no command %v", line, leading)
	}
	for _, word := range args {
		if !strings.HasPrefix(word, "--") {
			continue
		}
		name, _, _ := strings.Cut(strings.TrimPrefix(word, "--"), "=")
		if name == "help" || name == "all" {
			continue // every command takes them
		}
		if !slices.ContainsFunc(node.Flags, func(f cliFlag) bool { return f.Name == name }) {
			return fmt.Errorf("%q: the command declares no --%s", line, name)
		}
	}
	return nil
}

const sampleAppSlug = "sample-app"

// ConceptWords lists the guessed words concept hints answer.
func ConceptWords() []string {
	words := make([]string, 0, len(conceptHints))
	for word := range conceptHints {
		words = append(words, word)
	}
	slices.Sort(words)
	return words
}
