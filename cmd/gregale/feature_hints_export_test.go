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
	args, err := rewriteCliTree(words[1:])
	if err != nil {
		return fmt.Errorf("%q: %w", line, err)
	}
	var leading []string
	for _, word := range args {
		if strings.HasPrefix(word, "-") {
			break
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
