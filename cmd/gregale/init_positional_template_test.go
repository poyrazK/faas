package main_test

import (
	"strings"
	"testing"
)

// prod hunt 2026-11-21: `gregale init function-python313 .` printed only the
// usage. A template named as a word gets the flag form back.
func TestInitPositionalTemplateNamesTheFlagForm(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"init", "function-python313", "."}, "did you mean: gregale init --template function-python313 --path ."},
		{[]string{"init", "hello-go"}, "did you mean: gregale init --template hello-go --path ./hello-go"},
		{[]string{"init", "no-such-template"}, ""},
	} {
		code, stdout, stderr := runCLI(t, tc.args...)
		if code == 0 {
			t.Fatalf("%v: exit 0", tc.args)
		}
		out := stdout + stderr
		if tc.want == "" {
			if strings.Contains(out, "did you mean") {
				t.Errorf("%v: suggested a command for an unknown template:\n%s", tc.args, out)
			}
			continue
		}
		if !strings.Contains(out, tc.want) {
			t.Errorf("%v: want %q in:\n%s", tc.args, tc.want, out)
		}
	}
}
