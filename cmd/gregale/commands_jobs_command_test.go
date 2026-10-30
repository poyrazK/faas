package main_test

import (
	"slices"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

// `job create --command /bin/sh,-c,echo a, b` split the script at its comma.
// A JSON array is the exact argv and "\," is a comma inside an argument; a
// plain comma list keeps its meaning.
func TestParseJobCommand(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  []string
	}{
		{"/bin/sh,-c,echo hi", []string{"/bin/sh", "-c", "echo hi"}},
		{`/bin/sh,-c,echo a\, b`, []string{"/bin/sh", "-c", "echo a, b"}},
		{`["/bin/sh","-c","echo a, b"]`, []string{"/bin/sh", "-c", "echo a, b"}},
		{`printf,a\nb`, []string{"printf", `a\nb`}},
		{"[,-f,x,]", []string{"[", "-f", "x", "]"}},
	} {
		if got := main.ParseJobCommand(tc.value); !slices.Equal(got, tc.want) {
			t.Errorf("ParseJobCommand(%q) = %q, want %q", tc.value, got, tc.want)
		}
	}
}

// A command after a bare "--" is taken as the exact argv, so a shell script
// needs no comma escaping; the job's own flags stay before it.
func TestSplitJobArgv(t *testing.T) {
	own, argv := main.SplitJobArgv([]string{"nightly", "--image", "alpine", "--", "/bin/sh", "-c", "echo a, b --flag"})
	if !slices.Equal(own, []string{"nightly", "--image", "alpine"}) || !slices.Equal(argv, []string{"/bin/sh", "-c", "echo a, b --flag"}) {
		t.Fatalf("split = %q / %q", own, argv)
	}
	if own, argv := main.SplitJobArgv([]string{"nightly", "--image", "alpine"}); len(own) != 3 || argv != nil {
		t.Fatalf("split without -- = %q / %q, want no argv", own, argv)
	}
}
