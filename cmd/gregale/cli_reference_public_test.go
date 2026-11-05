package main_test

import (
	"bytes"
	"regexp"
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

// CLI audit 2026-11-04: the public reference documented every released
// command, and for a real Scale account 856 of its 1,494 paths answered
// "unknown command" because their capability was preview or internal. The
// reference now lists GA and beta commands only.
func TestPublicReferenceListsOnlyGAAndBetaCommands(t *testing.T) {
	var buf bytes.Buffer
	main.RenderMarkdownReference(&buf, main.CliTreeManifest(main.PublicReferenceCommands()))
	ref := buf.String()
	for _, heading := range []string{"## deploy\n", "## logs\n", "### domain list\n"} {
		if !strings.Contains(ref, heading) {
			t.Errorf("public reference lost the GA heading %q", strings.TrimSpace(heading))
		}
	}
	// events, mcp-hosting and automations are preview or internal.
	for _, heading := range []string{"## event\n", "## mcp\n", "## automation\n", "### release mirror\n"} {
		if strings.Contains(ref, heading) {
			t.Errorf("public reference documents the preview or internal heading %q", strings.TrimSpace(heading))
		}
	}
}

// A path that exists only through its flag folds (`edge cache set --method
// GET`) documents that flag as required, answers --help, and without the
// flag names it instead of failing with today's verbs.
func TestFoldOnlyPathsRequireTheirFlag(t *testing.T) {
	enableEveryCLIFeature(t)
	t.Setenv("FAAS_CAPABILITY_FLAGS_RELEASE_ALL", "1")
	var buf bytes.Buffer
	main.RenderMarkdownReference(&buf, main.CliTreeManifest(main.VisibleCliCommands()))
	if !strings.Contains(buf.String(), "`gregale edge cache set --method <GET|HEAD> ") {
		t.Errorf("reference does not mark edge cache set --method as required")
	}
	cases := []struct{ path, flag string }{
		{"edge cache set", "--method"},
		{"alert incident update", "--note"},
		{"api route health check", "--review"},
		{"debug issue update", "--issues"},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			main.ResetJSONOut(t)
			var stdout, stderr bytes.Buffer
			oldOut, oldErr := *main.OsStdout, *main.OsStderr
			*main.OsStdout, *main.OsStderr = &stdout, &stderr
			t.Cleanup(func() { *main.OsStdout, *main.OsStderr = oldOut, oldErr })

			if code := main.Run(append(strings.Fields(tc.path), "--help")); code != 0 {
				t.Fatalf("%s --help exited %d: %s", tc.path, code, stderr.String())
			}
			if want := regexp.MustCompile(`gregale ` + tc.path + ` ` + tc.flag + `\b`); !want.MatchString(stdout.String()) {
				t.Errorf("%s --help does not show %s as required:\n%s", tc.path, tc.flag, stdout.String())
			}

			stdout.Reset()
			stderr.Reset()
			if code := main.Run(append(strings.Fields(tc.path), "x")); code == 0 {
				t.Fatalf("%s without %s exited 0", tc.path, tc.flag)
			}
			if !strings.Contains(stderr.String(), "gregale "+tc.path+" needs "+tc.flag) {
				t.Errorf("%s without %s does not name the flag:\n%s", tc.path, tc.flag, stderr.String())
			}
		})
	}
}
