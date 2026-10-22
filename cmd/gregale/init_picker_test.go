package main_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/cmd/gregale/templates"
)

func TestListedTemplatesHaveSummariesAndCategoryLabels(t *testing.T) {
	for _, name := range templates.ListedNames() {
		summary := templates.Summary(name)
		if summary == "" || utf8.RuneCountInString(summary) > 60 {
			t.Errorf("%s: summary must be 1..60 characters: %q", name, summary)
		}
		if templates.CategoryLabels[templates.CategoryFor(name)] == "" {
			t.Errorf("%s: category %q has no picker label", name, templates.CategoryFor(name))
		}
	}
}

func TestCmdInitAsksForTemplateAndDirectoryInATerminal(t *testing.T) {
	tty := true
	oldTTY, oldIn := *main.TestOnlyTTY, *main.OsStdin
	*main.TestOnlyTTY = &tty
	dest := filepath.Join(t.TempDir(), "starter")
	// Category 1 (HTTP app), template 3 (hello-go), then the directory.
	*main.OsStdin = strings.NewReader("1\n3\n" + dest + "\n")
	t.Cleanup(func() { *main.TestOnlyTTY, *main.OsStdin = oldTTY, oldIn })
	stdout, readStderr, restore := main.SwapIO(t)
	defer restore()
	if code := main.CmdInit(nil); code != 0 {
		t.Fatalf("init exit = %d; stderr %q", code, readStderr())
	}
	if _, err := os.Stat(filepath.Join(dest, "main.go")); err != nil {
		t.Fatalf("hello-go was not written to the chosen directory: %v", err)
	}
	prompts := readStderr()
	for _, want := range []string{"What do you want to build?", "HTTP app", "hello-go · " + templates.Summary("hello-go"), "Directory [./hello-go]"} {
		if !strings.Contains(prompts, want) {
			t.Errorf("prompts missing %q:\n%s", want, prompts)
		}
	}
	if strings.Contains(stdout.String(), "What do you want to build?") {
		t.Errorf("prompts leaked to stdout:\n%s", stdout.String())
	}
}

func TestCmdInitWithoutTemplateOutsideATerminalPointsAtList(t *testing.T) {
	tty := false
	oldTTY := *main.TestOnlyTTY
	*main.TestOnlyTTY = &tty
	t.Cleanup(func() { *main.TestOnlyTTY = oldTTY })
	_, readStderr, restore := main.SwapIO(t)
	defer restore()
	if code := main.CmdInit([]string{"--path", t.TempDir()}); code != 1 {
		t.Fatalf("init exit = %d, want 1", code)
	}
	if got := readStderr(); !strings.Contains(got, "--template is required; list templates with gregale init --list") {
		t.Errorf("stderr should point at --list: %q", got)
	}
}

func TestCmdInitListShowsSummaries(t *testing.T) {
	var buf strings.Builder
	if code := main.RunCmdInitList(&buf); code != 0 {
		t.Fatalf("list exit = %d", code)
	}
	if !strings.Contains(buf.String(), "hello-node") || !strings.Contains(buf.String(), templates.Summary("hello-node")) {
		t.Errorf("--list lacks summaries:\n%s", buf.String())
	}
	for _, line := range strings.Split(buf.String(), "\n") {
		if strings.HasSuffix(line, " ") {
			t.Errorf("trailing space in %q", line)
		}
	}
}
