// adr: 20261112223000

package main_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/whycopy"
)

// fixSurfaces are the sources whose text tells a reader what to run after a
// failure: error constructors, the explanation catalogs, plan hints, and
// source-inference warnings and remedies.
var fixSurfaces = []string{
	"../../pkg/api/errors.go",
	"../../pkg/whycopy",
	"../../pkg/presetwhy",
	"../../pkg/featurehints",
	"../../pkg/preflight",
	"../../pkg/frameworkprofile",
}

var (
	// backtickCommand is a command line a fix tells the reader to run.
	backtickCommand = regexp.MustCompile("`(gregale [^`\n]+)`")
	// formatVerb and concatenation stand in for an app slug or a value.
	formatVerb    = regexp.MustCompile(`%[-+# 0-9.]*[sdvq]`)
	concatenation = regexp.MustCompile(`"\s*\+[^+]+\+\s*"`)
)

// fixCommandLines returns the command lines in one Go source file, with
// format verbs and string concatenations replaced by a placeholder value.
func fixCommandLines(t *testing.T, path string) map[int][]string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	out := map[int][]string{}
	for i, line := range strings.Split(string(body), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "//") {
			continue
		}
		for _, m := range backtickCommand.FindAllStringSubmatch(line, -1) {
			command := concatenation.ReplaceAllString(m[1], "my-app")
			command = formatVerb.ReplaceAllString(command, "my-app")
			out[i+1] = append(out[i+1], command)
		}
	}
	return out
}

// TestEveryFixCommandExists holds ADR-20261112223000 §2: every command a
// failure tells the reader to run exists, with the flags it names. An agent
// that runs a fix and gets "unknown command" is worse off than one that got
// no fix (the 2026-11-13 eval found `gregale certs` and an operator-only
// `rollouts recover` in customer fixes).
func TestEveryFixCommandExists(t *testing.T) {
	checked := 0
	for _, surface := range fixSurfaces {
		err := filepath.WalkDir(surface, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return err
			}
			for line, commands := range fixCommandLines(t, path) {
				for _, command := range commands {
					checked++
					if err := main.ResolveHintCommand(command); err != nil {
						t.Errorf("%s:%d: %v", path, line, err)
					}
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if checked < 20 {
		t.Fatalf("checked %d fix commands; the surfaces moved?", checked)
	}
}

// TestEveryFailureCodeHasAFix: every catalog row says what to do.
func TestEveryFailureCodeHasAFix(t *testing.T) {
	for _, code := range whycopy.Codes() {
		row, _ := whycopy.Lookup(code)
		if strings.TrimSpace(row.Fix) == "" && row.Observed == nil {
			t.Errorf("%s: no fix", code)
		}
	}
}
