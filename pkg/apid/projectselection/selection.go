// Package projectselection binds a protected project plan to its reviewed filters.
package projectselection

import (
	"slices"
	"strings"
)

// Filters keeps explicit and persisted exclusions separate so replay does not
// turn a persisted exclusion into an explicit --only/--exclude conflict.
type Filters struct {
	Only             []string `json:"only,omitempty"`
	Exclude          []string `json:"exclude,omitempty"`
	PersistedExclude []string `json:"persisted_exclude,omitempty"`
}

func New(only, exclude map[string]bool, persisted []string) *Filters {
	return &Filters{Only: keys(only), Exclude: keys(exclude), PersistedExclude: normalized(persisted)}
}

func Matches(reviewed, current *Filters) bool {
	return reviewed != nil && current != nil &&
		slices.Equal(reviewed.Only, current.Only) && slices.Equal(reviewed.Exclude, current.Exclude) &&
		slices.Equal(reviewed.PersistedExclude, current.PersistedExclude)
}

func keys(values map[string]bool) []string {
	var out []string
	for value, selected := range values {
		if selected {
			out = append(out, value)
		}
	}
	return normalized(out)
}

func normalized(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.ToLower(strings.TrimSpace(value)); value != "" {
			out = append(out, value)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}
