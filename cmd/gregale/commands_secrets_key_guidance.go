package main

import (
	"fmt"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

type secretKeyHint struct {
	Key        string
	Hidden     bool
	Suggestion string
}

func secretMetadataKeys(resp *api.AppSecretListResponse) []string {
	keys := []string{}
	if len(resp.SecretsByScope) > 0 {
		for _, rows := range resp.SecretsByScope {
			for _, row := range rows {
				if api.ValidateSecretKey(row.Key) == nil {
					keys = append(keys, row.Key)
				}
			}
		}
	} else {
		for _, row := range resp.Secrets {
			if api.ValidateSecretKey(row.Key) == nil {
				keys = append(keys, row.Key)
			}
		}
	}
	return sortedUniqueStrings(keys)
}

func secretKeyHints(requested, available, visible []string) []secretKeyHint {
	present, shown := map[string]bool{}, map[string]bool{}
	for _, key := range available {
		present[key] = true
	}
	for _, key := range visible {
		shown[key] = true
	}
	hints := []secretKeyHint{}
	for _, key := range sortedUniqueStrings(requested) {
		if shown[key] {
			continue
		}
		hint := secretKeyHint{Key: key, Hidden: present[key]}
		if !hint.Hidden && len(key) >= 4 {
			limit := 1
			if len(key) > 6 {
				limit = 2
			}
			best, tied := limit+1, false
			for _, candidate := range available {
				distance := editDistance(key, candidate)
				if distance < best {
					best, tied = distance, false
					hint.Suggestion = candidate
				} else if distance == best {
					tied = true
				}
			}
			if best > limit || tied {
				hint.Suggestion = ""
			}
		}
		hints = append(hints, hint)
	}
	return hints
}

func printSecretKeyHints(app, scope string, hints []secretKeyHint) {
	if len(hints) == 0 {
		return
	}
	_, _ = fmt.Fprintln(osStdout, "\nSelected key guidance (from fetched metadata):")
	for i, hint := range hints {
		if i == 5 {
			_, _ = fmt.Fprintf(osStdout, "  %d more selected keys did not match; review the unfiltered list.\n", len(hints)-i)
			break
		}
		target := hint.Key
		if hint.Hidden {
			_, _ = fmt.Fprintf(osStdout, "  %s is present in fetched metadata but excluded by the active class, age, or delivery filters.\n", hint.Key)
		} else {
			_, _ = fmt.Fprintf(osStdout, "  %s was not returned in the selected scope.\n", hint.Key)
			if hint.Suggestion == "" {
				continue
			}
			target = hint.Suggestion
			_, _ = fmt.Fprintf(osStdout, "  Did you mean %s? This is a suggestion only.\n", hint.Suggestion)
		}
		command := strings.Join([]string{recoveryCLI(), "secrets list --app", recoveryArgument(app), "--scope", recoveryArgument(scopeOrDefault(scope)), "--key", recoveryArgument(target)}, " ")
		_, _ = fmt.Fprintf(osStdout, "  Review without class, age, or delivery filters (POSIX shells):\n    %s\n", command)
	}
}
