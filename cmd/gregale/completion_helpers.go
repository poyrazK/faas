package main

import (
	"strconv"
	"strings"
)

func projectCompletionCommand() cliCommand {
	command, _ := lookupCliCommand("projects")
	return command
}

func buildCompletionCommand() cliCommand {
	command, _ := lookupCliCommand("build")
	return command
}

func deploymentCompletionCommands() []cliCommand {
	deployment, _ := lookupCliCommand("deployment")
	deploys, _ := lookupCliCommand("deploys")
	return []cliCommand{deployment, deploys}
}

func projectCompletionPositions() []cliCompletionPosition {
	return projectCompletionCommand().expandedCompletionPositions()
}

func projectCompletionContexts() []cliCompletionPosition {
	seen := make(map[string]bool)
	var contexts []cliCompletionPosition
	for _, position := range projectCompletionPositions() {
		if position.Role != cliCompletionProjectSlug {
			continue
		}
		key := strings.Join(position.Path, "\x00") + "\x00" + strconv.Itoa(position.Position)
		if seen[key] {
			continue
		}
		seen[key] = true
		contexts = append(contexts, position)
	}
	return contexts
}

func buildCLICompletionPositions() []cliCompletionPosition {
	return []cliCompletionPosition{
		{Path: []string{"status"}, Position: 1, Role: cliCompletionBuildID},
		{Path: []string{"provenance"}, Position: 1, Role: cliCompletionBuildID},
		{Path: []string{"sbom"}, Position: 1, Role: cliCompletionBuildID},
	}
}

type cliAppSlugCompletionPosition struct {
	Command                 string
	Path                    []string
	PathOffsets             []int
	WordOffset              int
	RequiredPositionOffsets []int
}

func isAppSlugPositional(positional string) bool {
	placeholder := strings.TrimSpace(positional)
	for strings.HasPrefix(placeholder, "[") && strings.HasSuffix(placeholder, "]") {
		placeholder = strings.TrimSpace(placeholder[1 : len(placeholder)-1])
	}
	return placeholder == "<app>" || placeholder == "<slug>"
}

func appSlugCompletionPositions() []cliAppSlugCompletionPosition {
	var positions []cliAppSlugCompletionPosition
	for _, command := range customerCliCommands() {
		if len(command.Subcommands) == 0 || command.SubcommandsAfterPositionals {
			for i, positional := range command.Positionals {
				if isAppSlugPositional(positional) {
					positions = appendAppSlugCompletionPosition(positions, command, nil, nil, i+1, nil)
				}
			}
		}
		if len(command.Subcommands) == 0 {
			continue
		}

		var precedingPositions []int
		if command.SubcommandsAfterPositionals {
			for i := range command.Positionals {
				precedingPositions = append(precedingPositions, i+1)
			}
		}
		firstSubcommandOffset := command.completionSubcommandWord() - 1
		positions = collectAppSlugSubcommandPositions(command, command.Subcommands, nil, nil, firstSubcommandOffset, precedingPositions, positions)
	}
	return positions
}

func collectAppSlugSubcommandPositions(command cliCommand, subcommands []cliSub, path []string, pathOffsets []int, subcommandOffset int, precedingPositions []int, positions []cliAppSlugCompletionPosition) []cliAppSlugCompletionPosition {
	for _, sub := range subcommands {
		subPath := append(append([]string(nil), path...), sub.Name)
		subPathOffsets := append(append([]int(nil), pathOffsets...), subcommandOffset)
		if len(sub.Subcommands) == 0 || sub.SubcommandsAfterPositionals {
			for i, positional := range sub.Positionals {
				if !isAppSlugPositional(positional) {
					continue
				}
				required := append([]int(nil), precedingPositions...)
				for prior := 0; prior < i; prior++ {
					required = append(required, subcommandOffset+prior+1)
				}
				positions = appendAppSlugCompletionPosition(positions, command, subPath, subPathOffsets, subcommandOffset+i+1, required)
			}
		}
		if len(sub.Subcommands) == 0 {
			continue
		}
		childOffset := subcommandOffset + 1
		childPrecedingPositions := append([]int(nil), precedingPositions...)
		if sub.SubcommandsAfterPositionals {
			for i := range sub.Positionals {
				positionOffset := subcommandOffset + i + 1
				childPrecedingPositions = append(childPrecedingPositions, positionOffset)
			}
			childOffset += len(sub.Positionals)
		}
		positions = collectAppSlugSubcommandPositions(command, sub.Subcommands, subPath, subPathOffsets, childOffset, childPrecedingPositions, positions)
	}
	return positions
}

func appendAppSlugCompletionPosition(positions []cliAppSlugCompletionPosition, command cliCommand, path []string, pathOffsets []int, wordOffset int, requiredPositionOffsets []int) []cliAppSlugCompletionPosition {
	for _, pathVariant := range command.completionPathVariants(path) {
		position := cliAppSlugCompletionPosition{
			Command:                 command.Name,
			Path:                    pathVariant,
			PathOffsets:             append([]int(nil), pathOffsets...),
			WordOffset:              wordOffset,
			RequiredPositionOffsets: append([]int(nil), requiredPositionOffsets...),
		}
		if !hasAppSlugCompletionPosition(positions, position) {
			positions = append(positions, position)
		}
	}
	return positions
}

func hasAppSlugCompletionPosition(positions []cliAppSlugCompletionPosition, candidate cliAppSlugCompletionPosition) bool {
	for _, position := range positions {
		if position.Command != candidate.Command || position.WordOffset != candidate.WordOffset ||
			strings.Join(position.Path, "\x00") != strings.Join(candidate.Path, "\x00") ||
			!sameCompletionOffsets(position.PathOffsets, candidate.PathOffsets) ||
			!sameCompletionOffsets(position.RequiredPositionOffsets, candidate.RequiredPositionOffsets) {
			continue
		}
		return true
	}
	return false
}

func sameCompletionOffsets(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func customerCompletionFlags() []cliFlag {
	var flags []cliFlag
	var visit func(cliSub)
	visit = func(sub cliSub) {
		flags = append(flags, sub.Flags...)
		for _, child := range sub.Subcommands {
			visit(child)
		}
	}
	for _, command := range customerCliCommands() {
		flags = append(flags, command.Flags...)
		for _, sub := range command.Subcommands {
			visit(sub)
		}
	}
	return flags
}

func completionFlagSpellings(match func(cliFlag) bool) []string {
	seen := make(map[string]bool)
	var spellings []string
	for _, flag := range customerCompletionFlags() {
		if !match(flag) {
			continue
		}
		for _, spelling := range cliFlagSpellings(flag) {
			if !seen[spelling] {
				seen[spelling] = true
				spellings = append(spellings, spelling)
			}
		}
	}
	return spellings
}

// completionUnambiguousFlagSpellings returns value-taking option spellings
// only when every manifest use of a spelling accepts the same kind of value.
// A few common names such as --path, --source, and --profile mean either a
// local path or an API value depending on the command, so shell-wide handlers
// must leave those to command-aware renderers.
func completionUnambiguousFlagSpellings(match func(cliFlag) bool) []string {
	total := make(map[string]int)
	matched := make(map[string]int)
	for _, flag := range customerCompletionFlags() {
		for _, spelling := range cliFlagSpellings(flag) {
			total[spelling]++
			if match(flag) {
				matched[spelling]++
			}
		}
	}
	var spellings []string
	for _, flag := range customerCompletionFlags() {
		for _, spelling := range cliFlagSpellings(flag) {
			if matched[spelling] == total[spelling] && matched[spelling] > 0 {
				if !containsCompletionString(spellings, spelling) {
					spellings = append(spellings, spelling)
				}
			}
		}
	}
	return spellings
}

func completionClosedSetValues(name string) []string {
	seen := make(map[string]bool)
	var values []string
	for _, flag := range customerCompletionFlags() {
		if flag.Name != name {
			continue
		}
		for _, value := range flag.ClosedSet {
			if value != "" && !seen[value] {
				seen[value] = true
				values = append(values, value)
			}
		}
	}
	return values
}

func cliFlagUsesEnvironmentValues(flag cliFlag) bool {
	if flag.Name == "scope" {
		return true
	}
	if flag.Name != "environment" {
		return false
	}
	description := strings.ToLower(flag.Short)
	return strings.Contains(description, "project environment") ||
		strings.Contains(description, "registered environment") ||
		strings.Contains(description, "named environment") ||
		strings.Contains(description, "environment filter")
}

func cliFlagUsesFilePathValues(flag cliFlag) bool {
	if flag.Bool || len(flag.ClosedSet) > 0 {
		return false
	}
	name := strings.ToLower(flag.Name)
	if name == "file" || strings.HasSuffix(name, "-file") || name == "manifest" ||
		strings.HasSuffix(name, "-manifest") || strings.HasSuffix(name, "-manifest-path") ||
		name == "tarball" || name == "dockerfile" {
		return true
	}
	switch name {
	case "out", "output", "o", "f":
		return true
	}
	return flag.Value == "FILE" || flag.Value == "DIR"
}

func containsCompletionString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func cliFlagTakesValue(flag cliFlag) bool {
	return !flag.Bool && (flag.Value != "" || len(flag.ClosedSet) > 0)
}
