package main

import (
	"strings"
)

type packSelection struct {
	excluded bool
	reason   string
	file     string
	line     int
	ancestor string
}

// The boolean packer and its explanation use the same ordered decision.
// .git cannot be re-included; the last matching configurable rule wins.
func (r packRules) selection(rel string, isDir bool) packSelection {
	base := rel
	if i := strings.LastIndex(rel, "/"); i >= 0 {
		base = rel[i+1:]
	}
	if base == ".git" {
		return packSelection{excluded: true, reason: "VCS metadata is always excluded"}
	}
	result := packSelection{reason: "source file"}
	if isDir {
		switch {
		case r.dockerfile && buildOutputDirs[base]:
			result.reason = "Dockerfile build output"
		case base == "vendor" && r.goVendor != nil && r.goVendor(rel):
			result.reason = "Go module vendoring"
		case defaultExcludeDirs[base]:
			result = packSelection{excluded: true, reason: "default build-artifact exclusion"}
		default:
			result.reason = "source directory"
		}
	} else if defaultExcludeFiles[base] || strings.HasSuffix(base, ".pyc") {
		result = packSelection{excluded: true, reason: "default generated/metadata exclusion"}
	}
	for _, pattern := range r.patterns {
		if pattern.dirOnly && !isDir {
			continue
		}
		if matchGregaleignoreOne(rel, pattern) {
			reason := "ignore rule"
			if pattern.negate {
				reason = "negated ignore rule"
			}
			result = packSelection{excluded: !pattern.negate, reason: reason, file: pattern.source, line: pattern.line}
		}
	}
	return result
}

// Archives can omit directory headers, so also apply the directory-pruning
// rules to every ancestor, just as the worktree walker does.
func gitArchiveSelection(name string, isDir bool, rules packRules) packSelection {
	result := rules.selection(name, isDir)
	if result.excluded {
		return result
	}
	parts := strings.Split(strings.TrimSuffix(name, "/"), "/")
	for i := 1; i < len(parts); i++ {
		ancestor := strings.Join(parts[:i], "/")
		if decision := rules.selection(ancestor, true); decision.excluded {
			decision.ancestor = ancestor
			return decision
		}
	}
	return result
}
