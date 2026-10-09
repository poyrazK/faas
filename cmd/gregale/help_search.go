package main

import (
	"fmt"
	"sort"
	"strings"
	"unicode"
)

const helpSearchLimit = 8
const helpSearchUsage = `usage: gregale help --search "task or keywords" [--all] [--json]`

type helpSearchResult struct {
	Command     string   `json:"command"`
	Description string   `json:"description"`
	Usage       string   `json:"usage"`
	Examples    []string `json:"examples"`
	DocsURL     string   `json:"docs_url"`
	keywords    []string
	flags       []cliFlag
	score       int
	matched     int
}

func hasHelpSearchFlag(args []string) bool {
	for _, arg := range args {
		if arg == "--search" || strings.HasPrefix(arg, "--search=") {
			return true
		}
	}
	return false
}

func cmdHelpSearch(args []string) int {
	query, all, err := parseHelpSearch(args)
	if err != nil {
		return printErr("Invalid help search", err)
	}
	commands := customerCliCommands()
	if all {
		commands = cliCommands
	}
	results := searchHelpCommands(query, commands)
	if jsonOutput {
		return jsonOut(writeJSON(struct {
			Query   string             `json:"query"`
			Results []helpSearchResult `json:"results"`
		}{query, results}))
	}
	if len(results) == 0 {
		_, _ = fmt.Fprintf(osStdout, "No commands match %q. Try fewer keywords or use gregale help --all.\n", query)
		return 0
	}
	_, _ = fmt.Fprintf(osStdout, "Commands matching %q:\n", query)
	for _, result := range results {
		_, _ = fmt.Fprintf(osStdout, "\n%s\n  %s\n  Usage: %s\n", result.Command, result.Description, result.Usage)
		for _, example := range result.Examples[:min(2, len(result.Examples))] {
			_, _ = fmt.Fprintf(osStdout, "  Example: %s\n", example)
		}
		_, _ = fmt.Fprintf(osStdout, "  Docs: %s\n", result.DocsURL)
	}
	return 0
}

func parseHelpSearch(args []string) (string, bool, error) {
	query, all, seen := "", false, false
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--all":
			if all {
				return "", false, fmt.Errorf("--all was supplied twice; %s", helpSearchUsage)
			}
			all = true
		case args[i] == "--search" || strings.HasPrefix(args[i], "--search="):
			if seen {
				return "", false, fmt.Errorf("--search was supplied twice; %s", helpSearchUsage)
			}
			seen = true
			if args[i] == "--search" {
				if i+1 == len(args) || strings.HasPrefix(args[i+1], "--") {
					return "", false, fmt.Errorf("--search requires a quoted task or keywords; %s", helpSearchUsage)
				}
				i++
				query = args[i]
			} else {
				query = strings.TrimPrefix(args[i], "--search=")
			}
		default:
			return "", false, fmt.Errorf("unexpected argument %q; quote multi-word queries; %s", args[i], helpSearchUsage)
		}
	}
	query = strings.TrimSpace(query)
	if !seen || len(query) > 256 || len(helpSearchTokens(query)) == 0 {
		return "", false, fmt.Errorf("search requires 1..256 bytes containing task keywords; %s", helpSearchUsage)
	}
	return query, all, nil
}

// Search only uses bundled metadata. No credentials, API calls, shell execution,
// or model service are involved. Coverage sorts before score so matching every
// task word beats a command with one heavily weighted name match.
func searchHelpCommands(query string, commands []cliCommand) []helpSearchResult {
	terms := helpSearchTokens(query)
	results := make([]helpSearchResult, 0)
	for _, candidate := range helpSearchIndex(commands) {
		name := helpSearchTokens(candidate.Command)
		keywords := helpSearchTokens(strings.Join(candidate.keywords, " "))
		description := helpSearchTokens(candidate.Description)
		var details strings.Builder
		for _, flag := range candidate.flags {
			fmt.Fprintf(&details, " %s %s", flag.Name, flag.Short)
		}
		details.WriteString(" " + strings.Join(candidate.Examples, " "))
		detailTokens := helpSearchTokens(details.String())
		for _, term := range terms {
			weight := 0
			switch {
			case containsHelpToken(name, term):
				weight = 8
			case containsHelpToken(keywords, term):
				weight = 6
			case containsHelpToken(description, term):
				weight = 4
			case containsHelpToken(detailTokens, term):
				weight = 1
			}
			if weight > 0 {
				candidate.matched++
				candidate.score += weight
			}
		}
		// Require a majority of the meaningful words to avoid flooding a task
		// search with matches for just "app" or "configuration".
		if candidate.matched > 0 && candidate.matched*2 >= len(terms)+1 {
			results = append(results, candidate)
		}
	}
	sort.Slice(results, func(i, j int) bool {
		if results[i].matched != results[j].matched {
			return results[i].matched > results[j].matched
		}
		if results[i].score != results[j].score {
			return results[i].score > results[j].score
		}
		return results[i].Command < results[j].Command
	})
	return results[:min(helpSearchLimit, len(results))]
}

func containsHelpToken(tokens []string, term string) bool {
	for _, token := range tokens {
		if token == term {
			return true
		}
	}
	return false
}

func helpSearchTokens(text string) []string {
	words := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	result := make([]string, 0, len(words))
	for _, word := range words {
		switch word {
		case "a", "an", "the", "to", "for", "of", "in", "on", "and", "or", "with", "after", "how", "do", "i", "my", "gregale":
			continue
		case "costs", "spending", "expense", "expenses":
			word = "cost"
		case "environments":
			word = "environment"
		case "secrets":
			word = "secret"
		case "changing", "changed", "changes":
			word = "change"
		case "comparing", "comparison":
			word = "compare"
		case "restarting":
			word = "restart"
		}
		if !containsHelpToken(result, word) {
			result = append(result, word)
		}
	}
	return result
}

func helpSearchIndex(commands []cliCommand) []helpSearchResult {
	var results []helpSearchResult
	for _, command := range commands {
		usage := "gregale " + command.Name
		if len(command.Subcommands) > 0 && !command.SubcommandsAfterPositionals {
			usage += " <" + command.subcommandChoice() + ">"
		}
		usage = localHelpArguments(usage, command.Positionals, command.Flags, false)
		if len(command.Subcommands) > 0 && command.SubcommandsAfterPositionals {
			usage += " <" + command.subcommandChoice() + ">"
		}
		results = append(results, helpSearchResult{
			Command: "gregale " + command.Name, Description: command.Short, Usage: usage,
			Examples: append([]string{}, command.Examples...), DocsURL: docsURLForTopic(command.DocSlug),
			keywords: command.SearchTerms, flags: command.Flags,
		})
		var walk func([]string, []cliSub)
		walk = func(ancestors []string, choices []cliSub) {
			for _, sub := range choices {
				names := append(append([]string{}, ancestors...), sub.Name)
				path := localHelpSubcommandPath(command, names)
				var required []cliFlag
				for _, flag := range sub.Flags {
					if flag.Req {
						required = append(required, flag)
					}
				}
				usage := mdSubSynopsis(command, names, sub.Positionals, required)
				if len(required) < len(sub.Flags) {
					usage += " [flags]"
				}
				// Restart's tracking verb is optional, unlike normal verb families.
				switch command.Name + " " + strings.Join(names, " ") {
				case "app restart":
					usage = strings.TrimPrefix(appRestartUsage, "usage: ")
				}
				results = append(results, helpSearchResult{
					Command: path, Description: sub.Short, Usage: usage,
					Examples: append([]string{}, sub.Examples...), DocsURL: docsURLForTopic(command.DocSlug),
					keywords: append(append([]string{}, sub.SearchTerms...), sub.Aliases...), flags: sub.Flags,
				})
				walk(names, sub.Subcommands)
			}
		}
		walk(nil, command.Subcommands)
	}
	return results
}
