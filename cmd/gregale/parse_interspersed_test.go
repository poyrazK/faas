package main

import (
	"flag"
	"io"
	"reflect"
	"strings"
	"testing"
)

func TestParseInterspersed(t *testing.T) {
	cases := []struct {
		name     string
		args     []string
		wantApp  string
		wantQuit bool
		wantPos  []string
	}{
		{"flags first", []string{"--app", "my-api", "--quiet", "id-1"}, "my-api", true, []string{"id-1"}},
		{"flags last", []string{"id-1", "--app", "my-api", "--quiet"}, "my-api", true, []string{"id-1"}},
		{"bool before positional keeps it", []string{"--quiet", "id-1", "id-2"}, "", true, []string{"id-1", "id-2"}},
		{"equals form", []string{"id-1", "--app=my-api"}, "my-api", false, []string{"id-1"}},
		{"value that looks like a flag", []string{"--app", "--dash", "id-1"}, "--dash", false, []string{"id-1"}},
		{"double dash ends flags", []string{"id-1", "--", "--quiet"}, "", false, []string{"id-1", "--quiet"}},
		{"single dash is positional", []string{"-", "--app", "x"}, "x", false, []string{"-"}},
		{"bool with explicit value", []string{"--quiet=false", "id-1"}, "", false, []string{"id-1"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fs := flag.NewFlagSet("t", flag.ContinueOnError)
			fs.SetOutput(io.Discard)
			app := fs.String("app", "", "")
			quiet := fs.Bool("quiet", false, "")
			if err := parseInterspersed(fs, tc.args); err != nil {
				t.Fatalf("parseInterspersed(%q) = %v", tc.args, err)
			}
			if *app != tc.wantApp || *quiet != tc.wantQuit || !reflect.DeepEqual(fs.Args(), tc.wantPos) {
				t.Fatalf("parseInterspersed(%q): app=%q quiet=%v args=%q, want app=%q quiet=%v args=%q",
					tc.args, *app, *quiet, fs.Args(), tc.wantApp, tc.wantQuit, tc.wantPos)
			}
		})
	}
}

func TestParseInterspersedRejectsUnknownFlag(t *testing.T) {
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	if err := parseInterspersed(fs, []string{"id-1", "--nope"}); err == nil {
		t.Fatal("unknown flag after a positional was accepted")
	}
}

// Prod hunt #3: a differential sweep ran every leaf with its flags before
// and after its positionals; these accepted only one order. Against a dead
// API, neither order may fail as a usage error.
func TestLeavesAcceptFlagsOnEitherSideOfPositionals(t *testing.T) {
	const id = "00000000-0000-4000-8000-000000000001"
	cases := []struct {
		name        string
		flags, args []string
	}{
		{"github bind", []string{"--repo", "acme/my-api", "--branch", "main"}, []string{"github", "bind", "my-api"}},
		{"github disconnect", []string{"--yes"}, []string{"github", "disconnect", "my-api"}},
		{"crons update", []string{"--schedule", "*/5 * * * *"}, []string{"crons", "update", id}},
		{"triggers delete", []string{"--quiet"}, []string{"triggers", "delete", id}},
		{"jobs add", []string{"--image", "ghcr.io/acme/job:1"}, []string{"jobs", "add", "nightly"}},
		{"jobs update", []string{"--ram", "256"}, []string{"jobs", "update", "nightly"}},
		{"jobs run", []string{"--tasks", "2"}, []string{"jobs", "run", "nightly"}},
		{"jobs occurrences", []string{"--limit", "5"}, []string{"jobs", "occurrences", "nightly"}},
		{"deploys show", []string{"--app", "my-api", "--status"}, []string{"deploys", "show", "v42"}},
		{"deploys status", []string{"--app", "my-api"}, []string{"deploys", "status", "v42"}},
		{"env create", []string{"--from", "production", "--protected"}, []string{"env", "create", "staging"}},
		{"queue setup", []string{"--force", "--max-attempts", "3"}, []string{"queue", "setup", "my-api"}},
		{"operations get", []string{"--self"}, []string{"operations", "get", id}},
		{"operations cancel", []string{"--self"}, []string{"operations", "cancel", id}},
		{"webhooks rotate-secret", []string{"--app", "my-api", "--secret", "s3cr3t-value"}, []string{"webhooks", "rotate-secret", "0123456789abcdef0123456789abcdef"}},
	}
	for _, tc := range cases {
		cmd := tc.args[:len(tc.args)-1]
		pos := tc.args[len(tc.args)-1]
		orders := map[string][]string{
			"flags first": append(append(append([]string{}, cmd...), tc.flags...), pos),
			"flags last":  append(append([]string{}, tc.args...), tc.flags...),
		}
		for order, argv := range orders {
			t.Run(tc.name+"/"+order, func(t *testing.T) {
				resetJSONOut(t)
				t.Setenv("FAAS_API", "http://127.0.0.1:9")
				t.Setenv("FAAS_TOKEN", "fp_live_test")
				t.Setenv("HOME", t.TempDir())
				_, readStderr, restore := swapIO(t)
				run(argv)
				stderr := readStderr()
				restore()
				if strings.Contains(stderr, "usage: gregale") || strings.Contains(stderr, "flag provided but not defined") {
					t.Fatalf("%q was rejected as a usage error:\n%s", argv, stderr)
				}
			})
		}
	}
}
