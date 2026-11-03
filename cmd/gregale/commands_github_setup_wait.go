package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// githubSetupCheckWaitFor confirms that a workflow of the repository at root
// is named waitFor and runs on pushes to every deploy branch
// (ADR-20261101160300, amendment 2). GitHub never starts a workflow_run for a
// workflow that does not exist or does not run, so a typo or a CI that runs
// only on pull requests would stop production deploys without an error.
// generated, the workflow setup writes, is not a candidate.
func githubSetupCheckWaitFor(root, generated, waitFor string, branches []string) error {
	dir := filepath.Join(root, ".github", "workflows")
	entries, err := os.ReadDir(dir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read %s: %w", dir, err)
	}
	var names []string
	for _, entry := range entries {
		ext := filepath.Ext(entry.Name())
		rel := filepath.ToSlash(filepath.Join(".github", "workflows", entry.Name()))
		if entry.IsDir() || (ext != ".yml" && ext != ".yaml") || rel == filepath.ToSlash(generated) {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return fmt.Errorf("read %s: %w", rel, err)
		}
		var workflow struct {
			Name string    `yaml:"name"`
			On   yaml.Node `yaml:"on"`
		}
		if yaml.Unmarshal(data, &workflow) != nil {
			continue
		}
		// GitHub names a workflow without a name: by its path.
		name := strings.TrimSpace(workflow.Name)
		if name == "" {
			name = rel
		}
		names = append(names, name)
		if name != waitFor {
			continue
		}
		for _, branch := range branches {
			if !githubWorkflowRunsOnPush(&workflow.On, branch) {
				return fmt.Errorf("workflow %q (%s) does not run on pushes to %s, so GitHub would never report a run to deploy; add the branch to its on.push trigger", waitFor, rel, branch)
			}
		}
		return nil
	}
	sort.Strings(names)
	found := "none"
	if len(names) > 0 {
		found = strings.Join(names, ", ")
	}
	return fmt.Errorf("no workflow in .github/workflows is named %q (found: %s); pass the name: field of your CI workflow", waitFor, found)
}

// githubWorkflowRunsOnPush reports whether a workflow's on: trigger starts it
// for a push to branch.
func githubWorkflowRunsOnPush(on *yaml.Node, branch string) bool {
	switch on.Kind {
	case yaml.ScalarNode:
		return on.Value == "push"
	case yaml.SequenceNode:
		for _, event := range on.Content {
			if event.Kind == yaml.ScalarNode && event.Value == "push" {
				return true
			}
		}
		return false
	case yaml.MappingNode:
		for i := 0; i+1 < len(on.Content); i += 2 {
			if on.Content[i].Value == "push" {
				return githubPushFilterMatches(on.Content[i+1], branch)
			}
		}
	}
	return false
}

// githubPushFilterMatches applies a push trigger's branch filters. A push
// filter naming only tags runs for no branch.
func githubPushFilterMatches(push *yaml.Node, branch string) bool {
	if push.Kind != yaml.MappingNode {
		return true
	}
	filters := map[string][]string{}
	for i := 0; i+1 < len(push.Content); i += 2 {
		var values []string
		if push.Content[i+1].Decode(&values) == nil {
			filters[push.Content[i].Value] = values
		}
	}
	if ignore, ok := filters["branches-ignore"]; ok {
		return !githubFilterMatches(ignore, branch)
	}
	if include, ok := filters["branches"]; ok {
		return githubFilterMatches(include, branch)
	}
	_, tags := filters["tags"]
	_, tagsIgnore := filters["tags-ignore"]
	return !tags && !tagsIgnore
}

// githubFilterMatches evaluates GitHub's filter patterns in order: a later
// pattern overrides an earlier one, and a leading ! excludes.
func githubFilterMatches(patterns []string, branch string) bool {
	matched := false
	for _, pattern := range patterns {
		negate := strings.HasPrefix(pattern, "!")
		if githubFilterPattern(strings.TrimPrefix(pattern, "!")).MatchString(branch) {
			matched = !negate
		}
	}
	return matched
}

// githubFilterPattern compiles a filter pattern: ** matches any characters,
// * any but /, and ?, + and [] act as in a regular expression.
func githubFilterPattern(pattern string) *regexp.Regexp {
	var re strings.Builder
	re.WriteString("^")
	for i := 0; i < len(pattern); i++ {
		switch c := pattern[i]; c {
		case '*':
			if i+1 < len(pattern) && pattern[i+1] == '*' {
				re.WriteString(".*")
				i++
			} else {
				re.WriteString("[^/]*")
			}
		case '?', '+', '[', ']':
			re.WriteByte(c)
		default:
			re.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	re.WriteString("$")
	compiled, err := regexp.Compile(re.String())
	if err != nil {
		return regexp.MustCompile("^" + regexp.QuoteMeta(pattern) + "$")
	}
	return compiled
}
