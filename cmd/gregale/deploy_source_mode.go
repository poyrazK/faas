package main

import "fmt"

type deploySourceMode string

const (
	deploySourceAuto     deploySourceMode = "auto"
	deploySourceHEAD     deploySourceMode = "head"
	deploySourceWorktree deploySourceMode = "worktree"
)

// resolveDeploySourceMode preserves the historical default while letting
// scripts pin the exact local source policy. --worktree remains an alias for
// existing callers; mixing the two spellings is rejected as ambiguous.
func resolveDeploySourceMode(value string, explicit, worktree, nonLocal, createOnly bool) (deploySourceMode, error) {
	if !explicit {
		if worktree {
			return deploySourceWorktree, nil
		}
		return deploySourceAuto, nil
	}
	if worktree {
		return "", fmt.Errorf("--source and --worktree cannot be combined")
	}
	if nonLocal {
		return "", fmt.Errorf("--source applies only to local directories; remove the image, archive, repository, template, or --github selector")
	}
	if createOnly {
		return "", fmt.Errorf("--source cannot be combined with --create-only because no source is uploaded")
	}
	switch deploySourceMode(value) {
	case deploySourceAuto, deploySourceHEAD, deploySourceWorktree:
		return deploySourceMode(value), nil
	default:
		return "", fmt.Errorf("invalid --source %q; use auto, head, or worktree", value)
	}
}
