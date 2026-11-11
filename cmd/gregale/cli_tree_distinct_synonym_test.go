package main_test

import (
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

// Hunt 32 H32-72: help folded a verb spelled like a synonym into the
// standard verb beside it, so `flag --help` hid `inspect` (a command of its
// own, not `get`). True aliases (`rm` beside `delete`) still fold.
func TestTreeHelpListsDistinctSynonymCommands(t *testing.T) {
	for _, tt := range []struct {
		words []string
		want  string
	}{
		{[]string{"flag", "--help"}, "Explain a customer's decision"},
		{[]string{"dashboard", "--help"}, "Show every widget's current value"},
	} {
		out, errOut, restore := main.SwapIO(t)
		main.Run(tt.words)
		got := out.String() + errOut()
		restore()
		if !strings.Contains(got, tt.want) {
			t.Fatalf("gregale %s does not list %q:\n%s", strings.Join(tt.words, " "), tt.want, got)
		}
	}
	out, errOut, restore := main.SwapIO(t)
	main.Run([]string{"app", "tcp", "--help"})
	got := out.String() + errOut()
	restore()
	if strings.Contains(got, "Alias for rm") {
		t.Fatalf("an alias was listed beside its command:\n%s", got)
	}
}
