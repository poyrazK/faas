package main

import (
	"runtime/debug"
	"strings"
	"testing"
)

func TestQualificationSourceComesFromBuiltExecutable(t *testing.T) {
	revision := strings.Repeat("a", 40)
	for _, tc := range []struct {
		name, revision, modified, vcs string
		wantClean                     bool
		wantRevision                  string
	}{
		{"clean", revision, "false", "git", true, revision},
		{"dirty", revision, "true", "git", false, revision},
		{"missing dirty marker", revision, "", "git", false, revision},
		{"unstamped", "", "false", "git", false, ""},
		{"malformed", "private-invalid-revision", "false", "git", false, ""},
		{"foreign VCS", revision, "false", "other", false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			info := &debug.BuildInfo{Settings: []debug.BuildSetting{{Key: "vcs", Value: tc.vcs}, {Key: "vcs.revision", Value: tc.revision}, {Key: "vcs.modified", Value: tc.modified}}}
			got, clean := qualificationBuildSource(info)
			if got != tc.wantRevision || clean != tc.wantClean {
				t.Fatalf("revision=%q clean=%v", got, clean)
			}
		})
	}
}
