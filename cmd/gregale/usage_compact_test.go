package main_test

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/onebox-faas/faas/cmd/gregale"
)

func TestCompactUsageShortensLongSynopses(t *testing.T) {
	flags := strings.Repeat(" [--flag VALUE]", 12)
	for _, tc := range []struct {
		name, in, want string
	}{
		{"short synopsis unchanged", "usage: gregale logs [<slug>] [--follow]", "usage: gregale logs [<slug>] [--follow]"},
		{"slug then flags", "usage: gregale app <slug>" + flags, "usage: gregale app <slug> [flags]\n  All options: gregale app --help"},
		{"angle verb choice", "usage: gregale events <alpha|beta|gamma> [ID]" + flags, "usage: gregale events <command> [flags]\n  All options: gregale events --help"},
		{"bracket verb choice", "usage: gregale app <slug> [scale|costs" + flags + "]", "usage: gregale app <slug> [command] [flags]\n  All options: gregale app --help"},
		{"pointer stops at placeholder", "usage: gregale app <slug> scale" + flags, "usage: gregale app <slug> scale [flags]\n  All options: gregale app --help"},
		{"upper-case positional", "usage: gregale auth config NAME" + flags, "usage: gregale auth config NAME [flags]\n  All options: gregale auth config --help"},
		{"positional forms only", "usage: gregale bucket protection " + strings.Repeat("<status app bucket operation-id> | ", 6), "usage: gregale bucket protection ...\n  All options: gregale bucket protection --help"},
		{"trailing note kept", "usage: gregale workflow wait <run-id>" + flags + "; provide a UUID and a positive timeout", "usage: gregale workflow wait <run-id> [flags]\n  provide a UUID and a positive timeout\n  All options: gregale workflow wait --help"},
		{"bracketed semicolon is synopsis", "usage: gregale releases bind <project> [--file FILE; required except prepare]" + flags, "usage: gregale releases bind <project> [flags]\n  All options: gregale releases bind --help"},
		{"required flags kept", "usage: gregale job create <name> --image REF" + flags, "usage: gregale job create <name> --image REF [flags]\n  All options: gregale job create --help"},
		{"required value alternatives kept", "usage: gregale x recover <id> --action resume|resolve" + flags, "usage: gregale x recover <id> --action resume|resolve [flags]\n  All options: gregale x recover --help"},
		{"required flags dropped when too long", "usage: gregale x NAME " + strings.Repeat("--required-flag-name VALUE ", 8) + strings.TrimSpace(flags), "usage: gregale x NAME [flags]\n  All options: gregale x --help"},
		{"error detail kept", "usage: gregale init" + flags + "\n  error: --template is required", "usage: gregale init [flags]\n  All options: gregale init --help\n  error: --template is required"},
		{"not a usage line", strings.Repeat("x", 200), strings.Repeat("x", 200)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := main.CompactUsage(tc.in); got != tc.want {
				t.Fatalf("CompactUsage:\n got %q\nwant %q", got, tc.want)
			}
		})
	}
}

// Every usage literal long enough to be compacted must point at a --help
// command that runs as printed.
func TestLongUsagePointersRunHelp(t *testing.T) {
	enableEveryCLIFeature(t)
	pointer := regexp.MustCompile(`All options: gregale (.+) --help`)
	oldOut, oldErr := *main.OsStdout, *main.OsStderr
	t.Cleanup(func() { *main.OsStdout, *main.OsStderr = oldOut, oldErr })
	checked := map[string]bool{}
	for _, usage := range longUsageLiterals(t) {
		compact := main.CompactUsage(usage)
		first, _, _ := strings.Cut(compact, "\n")
		if utf8.RuneCountInString(first) > main.UsageSynopsisLimit {
			t.Errorf("synopsis still longer than %d characters: %q", main.UsageSynopsisLimit, first)
			continue
		}
		match := pointer.FindStringSubmatch(compact)
		if match == nil {
			t.Errorf("long usage has no --help pointer: %q", usage)
			continue
		}
		if checked[match[1]] {
			continue
		}
		checked[match[1]] = true
		var out, errOut bytes.Buffer
		*main.OsStdout, *main.OsStderr = &out, &errOut
		args := append(strings.Fields(match[1]), "--help")
		code := main.Run(args)
		help := strings.Contains(out.String(), "Usage:") || strings.Contains(out.String(), "Flags for gregale ")
		if code != 0 || !help || errOut.Len() != 0 {
			t.Errorf("gregale %s exit = %d; stdout %q stderr %q", strings.Join(args, " "), code, out.String(), errOut.String())
		}
	}
	if len(checked) == 0 {
		t.Fatal("found no long usage literals; the scan is broken")
	}
}

func longUsageLiterals(t *testing.T) []string {
	t.Helper()
	fset := token.NewFileSet()
	packages, err := parser.ParseDir(fset, ".", func(info os.FileInfo) bool {
		return !strings.HasSuffix(info.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	var usages []string
	for _, file := range packages["main"].Files {
		ast.Inspect(file, func(node ast.Node) bool {
			literal, ok := node.(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING {
				return true
			}
			value, err := strconv.Unquote(literal.Value)
			if err != nil || !strings.HasPrefix(value, "usage: gregale ") {
				return true
			}
			first, _, _ := strings.Cut(value, "\n")
			if utf8.RuneCountInString(first) > main.UsageSynopsisLimit {
				usages = append(usages, value)
			}
			return true
		})
	}
	return usages
}

// A verb the help manifest does not describe yet still answers --help from
// the flags its parser registers, instead of a flag error and exit 1.
func TestUnmanifestedVerbHelpPrintsParserFlags(t *testing.T) {
	oldOut, oldErr := *main.OsStdout, *main.OsStderr
	t.Cleanup(func() { *main.OsStdout, *main.OsStderr = oldOut, oldErr })
	// drain_status is an undocumented compatibility spelling the dispatcher
	// accepts; the help manifest lists only drain-status. If it gains an
	// alias, pick another spelling the manifest does not describe.
	for _, args := range [][]string{
		{"realtime", "drain_status", "--help"},
		{"realtime", "drain_status", "app", "endpoint", "-h"},
	} {
		var out, errOut bytes.Buffer
		*main.OsStdout, *main.OsStderr = &out, &errOut
		if code := main.Run(args); code != 0 {
			t.Errorf("%v exit = %d, want 0; stderr %q", args, code, errOut.String())
		}
		if errOut.Len() != 0 {
			t.Errorf("%v wrote to stderr: %q", args, errOut.String())
		}
		if !strings.Contains(out.String(), "Flags for gregale realtime drain_status") || !strings.Contains(out.String(), "\n  --wait") {
			t.Errorf("%v help lacks the parser's flags:\n%s", args, out.String())
		}
	}
	// Without --help the same verb still fails with its usage.
	var out, errOut bytes.Buffer
	*main.OsStdout, *main.OsStderr = &out, &errOut
	if code := main.Run([]string{"realtime", "drain_status"}); code != 1 || !strings.Contains(errOut.String(), "usage: gregale realtime drain-status") {
		t.Errorf("missing arguments: exit %d stderr %q", code, errOut.String())
	}
}
