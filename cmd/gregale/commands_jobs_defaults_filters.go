package main

import (
	"errors"
	"flag"
	"fmt"
	"slices"
)

type jobDefaultsFilterEdits struct {
	addInclude, removeInclude, addExclude, removeExclude jobsMultiFlag
}

func (e *jobDefaultsFilterEdits) register(fs *flag.FlagSet) {
	fs.Var(&e.addInclude, "add-include", "append an include glob without replacing other filters (repeatable)")
	fs.Var(&e.removeInclude, "remove-include", "remove an exact saved include glob (repeatable)")
	fs.Var(&e.addExclude, "add-exclude", "append an exclude glob without replacing other filters (repeatable)")
	fs.Var(&e.removeExclude, "remove-exclude", "remove an exact saved exclude glob (repeatable)")
}

func (e jobDefaultsFilterEdits) active() bool {
	return len(e.addInclude)+len(e.removeInclude)+len(e.addExclude)+len(e.removeExclude) > 0
}

func (e jobDefaultsFilterEdits) apply(d localJobDefaults) (localJobDefaults, error) {
	var err error
	d.Include, err = editJobDefaultFilterList(d.Include, e.addInclude, e.removeInclude)
	if err != nil {
		return d, fmt.Errorf("include filters: %w", err)
	}
	d.Exclude, err = editJobDefaultFilterList(d.Exclude, e.addExclude, e.removeExclude)
	if err != nil {
		return d, fmt.Errorf("exclude filters: %w", err)
	}
	return d, nil
}

func editJobDefaultFilterList(existing, additions, removals []string) ([]string, error) {
	if err := validateJobArtifactPatterns("incremental filter edit", append(slices.Clone(additions), removals...)); err != nil {
		return nil, err
	}
	remove := map[string]bool{}
	for _, pattern := range removals {
		if !slices.Contains(existing, pattern) {
			return nil, fmt.Errorf("saved pattern %s does not exist; no filters changed", recoveryArgument(pattern))
		}
		if slices.Contains(additions, pattern) {
			return nil, errors.New("the same pattern cannot be added and removed in one edit")
		}
		remove[pattern] = true
	}
	result := make([]string, 0, len(existing)+len(additions))
	seen := map[string]bool{}
	for _, pattern := range existing {
		if !remove[pattern] {
			result = append(result, pattern)
			seen[pattern] = true
		}
	}
	for _, pattern := range additions {
		if !seen[pattern] {
			result = append(result, pattern)
			seen[pattern] = true
		}
	}
	return result, nil
}
