package main

import (
	"path/filepath"
	"strings"
)

func completionJobDefaultPatterns(words []string, option string) []string {
	if len(words) > 0 && words[0] == "--" {
		words = words[1:]
	}
	prior := words[3 : len(words)-1]
	if len(prior) > 0 && prior[len(prior)-1] == option {
		prior = prior[:len(prior)-1]
	}
	selected := map[string]bool{}
	for i := 0; i < len(prior); i++ {
		name, value, inline := strings.Cut(prior[i], "=")
		switch name {
		case "--tasks", "--include", "--exclude", "--add-include", "--remove-include", "--add-exclude", "--remove-exclude":
			if !inline {
				i++
				if i >= len(prior) {
					return nil
				}
				value = prior[i]
			}
			if name == option {
				selected[value] = true
			}
		case "--results", "--wait-ready", "--all-artifacts", "--clear-tasks", "--dry-run":
		default:
			if strings.HasPrefix(name, "-") {
				return nil
			}
		}
	}
	path, err := jobDefaultsPath()
	if err != nil {
		return nil
	}
	parent, err := inspectArtifactOutputDirectory(filepath.Dir(path))
	if err != nil {
		return nil
	}
	defer parent.Close()
	if parent.root == nil {
		return nil
	}
	info, err := parent.root.Lstat(filepath.Base(path))
	if err != nil || !info.Mode().IsRegular() || info.Size() > 65536 {
		return nil
	}
	defaults, found, err := readJobDefaults()
	if err != nil || found != path {
		return nil
	}
	patterns := defaults.Include
	if option == "--remove-exclude" {
		patterns = defaults.Exclude
	}
	result := []string{}
	for _, pattern := range patterns {
		if !selected[pattern] && !strings.ContainsAny(pattern, "\r\n\t") {
			result = append(result, pattern)
			selected[pattern] = true
		}
	}
	return result
}
