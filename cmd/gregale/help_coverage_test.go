package main_test

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
)

// Every subcommand a usage message names must answer --help from the help
// manifest, so it is also in completion, man pages and the CLI reference.
// The parser fallback ("Flags for gregale ...") only covers gaps; it is not
// coverage.
func TestEveryUsageSubcommandHasManifestHelp(t *testing.T) {
	enableEveryCLIFeature(t)
	t.Setenv("FAAS_API", "http://127.0.0.1:9")
	t.Setenv("FAAS_TOKEN", "")
	oldOut, oldErr := *main.OsStdout, *main.OsStderr
	t.Cleanup(func() { *main.OsStdout, *main.OsStderr = oldOut, oldErr })
	verbs := usageSubcommands(t)
	if len(verbs) < 500 {
		t.Fatalf("found only %d usage subcommands; the scan is broken", len(verbs))
	}
	for _, args := range verbs {
		var out, errOut bytes.Buffer
		*main.OsStdout, *main.OsStderr = &out, &errOut
		help := append(append([]string{}, args...), "--help")
		code := main.Run(help)
		if code != 0 || !strings.Contains(out.String(), "Usage:") || errOut.Len() != 0 {
			t.Errorf("gregale %s: exit %d, stdout %.120q, stderr %.120q", strings.Join(help, " "), code, out.String(), errOut.String())
		}
	}
}

var usageVerbList = regexp.MustCompile(`usage: gregale ((?:[a-z][a-z0-9-]* )+)<([a-z0-9-]+(?:\|[a-z0-9-]+)+)>`)

// usageSubcommands returns each command path plus verb that a usage literal
// lists as a choice, such as "realtime <list|get|...>".
func usageSubcommands(t *testing.T) [][]string {
	t.Helper()
	fset := token.NewFileSet()
	packages, err := parser.ParseDir(fset, ".", func(info os.FileInfo) bool {
		return !strings.HasSuffix(info.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, file := range packages["main"].Files {
		ast.Inspect(file, func(node ast.Node) bool {
			literal, ok := node.(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING {
				return true
			}
			value, err := strconv.Unquote(literal.Value)
			if err != nil {
				return true
			}
			for _, match := range usageVerbList.FindAllStringSubmatch(value, -1) {
				for _, verb := range strings.Split(match[2], "|") {
					seen[strings.TrimSpace(match[1])+" "+verb] = true
				}
			}
			return true
		})
	}
	paths := make([]string, 0, len(seen))
	for path := range seen {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	verbs := make([][]string, len(paths))
	for i, path := range paths {
		verbs[i] = strings.Fields(path)
	}
	return verbs
}
