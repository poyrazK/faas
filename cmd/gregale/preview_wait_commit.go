package main

import (
	"fmt"
	"strings"
	"time"
)

// validPreviewWaitCommit accepts an empty --commit or a 7 to 40 character
// hexadecimal commit SHA.
func validPreviewWaitCommit(commit string) bool {
	if commit == "" {
		return true
	}
	if len(commit) < 7 || len(commit) > 40 {
		return false
	}
	for _, r := range strings.ToLower(commit) {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

// previewCommitMatches reports whether a preview's recorded commit is the one
// `preview wait --commit` asked for (ADR-20261105090226). Without --commit
// every commit matches.
func previewCommitMatches(recorded, want string) bool {
	if want == "" {
		return true
	}
	return recorded != "" && strings.HasPrefix(strings.ToLower(recorded), strings.ToLower(want))
}

// previewWaitResumeCommand repeats --commit so a resumed wait keeps waiting
// for the same head.
func previewWaitResumeCommand(slug, commit string, timeout time.Duration) string {
	if commit == "" {
		return fmt.Sprintf("gregale preview wait %s --timeout %d", slug, int(timeout/time.Second))
	}
	return fmt.Sprintf("gregale preview wait %s --commit %s --timeout %d", slug, commit, int(timeout/time.Second))
}
