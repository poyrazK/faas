// adr: 20261112223000

package main_test

import (
	"regexp"
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

// The 2026-11-13 eval's agent guessed `app x listener`, `app tcp` and
// `app network` before finding --ports; each answer listed subcommands only.
func TestGuessedConceptsNameTheRealCommand(t *testing.T) {
	cases := []struct {
		args []string
		want []string
	}{
		{[]string{"app", "x", "listener", "--help"}, []string{"gregale app <app> tcp add --name <name> --guest-port <port>", "gregale app <app> --ports <name>=tcp:<port>[:internal]"}},
		{[]string{"app", "x", "redis"}, []string{"gregale app <app> --ports redis=tcp:6379:internal"}},
		{[]string{"app", "x", "private"}, []string{"gregale app <app> --visibility internal"}},
	}
	for _, tc := range cases {
		main.ResetJSONOut(t)
		_, stderr, restore := main.SwapIO(t)
		code := main.Run(tc.args)
		restore()
		if code == 0 {
			t.Errorf("%v: exit 0, want an error", tc.args)
		}
		for _, want := range tc.want {
			if !strings.Contains(stderr(), want) {
				t.Errorf("%v: stderr lacks %q:\n%s", tc.args, want, stderr())
			}
		}
	}
}

var conceptLine = regexp.MustCompile(`(?m)^\s+(gregale [^#\n]+?)\s+#`)

// Every suggestion a guessed word can print is a real command and flag.
func TestEveryConceptHintIsARealCommand(t *testing.T) {
	for _, word := range main.ConceptWords() {
		main.ResetJSONOut(t)
		_, stderr, restore := main.SwapIO(t)
		_ = main.Run([]string{"app", "x", word})
		restore()
		for _, m := range conceptLine.FindAllStringSubmatch(stderr(), -1) {
			line := strings.NewReplacer("[:internal]", "", "|", "-", "[LOCAL:]", "").Replace(m[1])
			if err := main.ResolveHintCommand(line); err != nil {
				t.Errorf("%s: %v", word, err)
			}
		}
	}
}

// `--help --all` on a tree-only app verb printed today's `app` help, which
// read `listener create` as positionals.
func TestAppListenerHelpAllPrintsTheVerbsHelp(t *testing.T) {
	main.ResetJSONOut(t)
	stdout, stderr, restore := main.SwapIO(t)
	code := main.Run([]string{"app", "listener", "create", "--help", "--all"})
	restore()
	if code != 0 || !strings.Contains(stdout.String(), "Create a TCP listener") {
		t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr())
	}
}
