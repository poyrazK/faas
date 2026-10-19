package main_test

import (
	"bytes"
	"github.com/onebox-faas/faas/cmd/gregale"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestCompactCommandHelp(t *testing.T) {
	for _, tc := range []struct {
		name, common, advanced string
	}{
		{"deploy", "--source <auto|head|worktree>", "--canary-preset"},
		{"app", "gregale app <slug> [command] [flags]", "--warm-snapshot"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			command, _ := main.LookupCliCommand(tc.name)
			var out bytes.Buffer
			main.PrintLocalCommandHelp(&out, command)
			help := out.String()
			for _, want := range []string{tc.common, "Examples:", "gregale " + tc.name + " --help --all", "Connection:", "RAM/CPU:"} {
				if !strings.Contains(help, want) {
					t.Errorf("missing %q:\n%s", want, help)
				}
			}
			if strings.Contains(help, tc.advanced) {
				t.Errorf("advanced flag leaked into compact help:\n%s", help)
			}
			if lines := strings.Count(help, "\n"); lines > 30 {
				t.Errorf("compact help is %d lines; maximum 30:\n%s", lines, help)
			}
			for _, line := range strings.Split(help, "\n") {
				if utf8.RuneCountInString(line) > 80 {
					t.Errorf("help wraps at 80 columns: %q", line)
				}
			}
		})
	}
}

func TestExpandedCommandHelpRetainsEveryOption(t *testing.T) {
	for _, name := range []string{"deploy", "app"} {
		t.Run(name, func(t *testing.T) {
			command, _ := main.LookupCliCommand(name)
			var out bytes.Buffer
			main.PrintLocalCommandHelpDetail(&out, command, true)
			help := out.String()
			for _, line := range strings.Split(help, "\n") {
				if utf8.RuneCountInString(line) > 80 {
					t.Errorf("expanded help wraps at 80 columns: %q", line)
				}
			}
			for _, flag := range command.Flags {
				// A flag's own entry appears exactly once, even when other
				// descriptions mention it as a prerequisite or exclusion.
				entry := "  " + main.MdFlagLabel(flag)
				found := 0
				for _, line := range strings.Split(help, "\n") {
					if line == entry || strings.HasPrefix(line, entry+" ") {
						found++
					}
				}
				if found != 1 {
					t.Errorf("%s appears %d times, want once", entry, found)
				}
				if flag.Group == "" || !strings.Contains(help, "\n"+flag.Group+":\n") {
					t.Errorf("--%s needs a visible purpose group", flag.Name)
				}
			}
			for _, sub := range command.Subcommands {
				if !strings.Contains(help, "\n  "+sub.Name+" ") {
					t.Errorf("expanded help omits %s", sub.Name)
				}
			}
			for _, want := range []string{"micro, small, medium, large, xlarge", "before the command", "after the command"} {
				if !strings.Contains(help, want) {
					t.Errorf("expanded help missing %q", want)
				}
			}
		})
	}
}

func TestExpandedHelpFlagOrder(t *testing.T) {
	main.SetupConnectionProfiles(t)
	oldOut := (*main.OsStdout)
	t.Cleanup(func() { (*main.OsStdout) = oldOut })
	for _, tc := range []struct {
		args []string
		all  bool
	}{
		{[]string{"deploy", "--help", "--all"}, true},
		{[]string{"deploy", "--all", "-h"}, true},
		{[]string{"help", "deploy", "--all"}, true},
		{[]string{"--profile", "missing", "deploy", "--help", "--all"}, true},
		{[]string{"--json", "deploy", "--help", "--all"}, true},
		{[]string{"deploy", "--name", "--all", "--help"}, false},
		{[]string{"deploy", "--name=--all", "--help"}, false},
		{[]string{"deploy", "--help", "--", "--all"}, false},
		{[]string{"app", "network", "--help", "--all"}, true},
		{[]string{"help", "app", "--all"}, true},
		{[]string{"app", "--all", "--help"}, true},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			var out bytes.Buffer
			(*main.OsStdout) = &out
			if code := main.Run(tc.args); code != 0 {
				t.Fatalf("run(%q) = %d", tc.args, code)
			}
			if expanded := strings.Contains(out.String(), "Access and security:") || strings.Contains(out.String(), "Rollout and health:"); expanded != tc.all {
				t.Errorf("expanded = %v, want %v:\n%s", expanded, tc.all, out.String())
			}
		})
	}
}

func TestLocalHelpExplainsFlagValues(t *testing.T) {
	var out bytes.Buffer
	main.PrintLocalHelpFlags(&out, []main.CliFlag{
		{Name: "mode", Value: "MODE", ClosedSet: []string{"fast", "slow"}, Short: "select mode"},
		{Name: "header", Value: "KEY=VALUE", Req: true, Repeatable: true, Short: "add a header"},
		{Name: "enabled", Bool: true, ClosedSet: []string{"true", "false"}, Short: "toggle feature"},
		{Name: "out", ShortName: "o", Value: "PATH", Short: "write output"},
	}, false)
	for _, want := range []string{"--mode <MODE>", "Values: fast, slow", "--header <KEY=VALUE>", "(required)", "(repeatable)", "--enabled[=true|false]", "--out|-o <PATH>"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("flag help missing %q:\n%s", want, out.String())
		}
	}
}
