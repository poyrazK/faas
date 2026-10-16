package main

import (
	"fmt"
	"path"
	"strings"
)

func validateJobArtifactIncludes(patterns []string) error {
	return validateJobArtifactPatterns("--include", patterns)
}
func validateJobArtifactExcludes(patterns []string) error {
	return validateJobArtifactPatterns("--exclude", patterns)
}
func validateJobArtifactPatterns(flagName string, patterns []string) error {
	for _, pattern := range patterns {
		if pattern == "" || strings.ContainsAny(pattern, "/\x00") {
			return fmt.Errorf("%s requires a nonempty artifact-name glob without slashes or NUL characters", flagName)
		}
		if _, err := path.Match(pattern, ""); err != nil {
			return fmt.Errorf("%s requires a valid glob using *, ?, or character classes; quote patterns to prevent shell expansion", flagName)
		}
	}
	return nil
}

func jobArtifactIncluded(name string, patterns []string) bool {
	if len(patterns) == 0 {
		return true
	}
	for _, pattern := range patterns {
		if matched, _ := path.Match(pattern, name); matched {
			return true
		}
	}
	return false
}

func appendJobArtifactIncludes(command string, patterns []string) string {
	for _, pattern := range patterns {
		command += " --include " + recoveryArgument(pattern)
	}
	return command
}

func jobArtifactSelected(name string, includes, excludes []string) bool {
	if !jobArtifactIncluded(name, includes) {
		return false
	}
	for _, pattern := range excludes {
		if matched, _ := path.Match(pattern, name); matched {
			return false
		}
	}
	return true
}
func appendJobArtifactFilters(command string, includes, excludes []string) string {
	command = appendJobArtifactIncludes(command, includes)
	for _, pattern := range excludes {
		command += " --exclude " + recoveryArgument(pattern)
	}
	return command
}
func jobArtifactFilterDescription(excludes []string) string {
	if len(excludes) > 0 {
		return "artifact filters"
	}
	return "include patterns"
}
