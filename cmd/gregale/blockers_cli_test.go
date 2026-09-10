package main

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
)

func TestNormalizeAnalyticsArgsAcceptsSlugAndFlagsInEitherOrder(t *testing.T) {
	want := []string{"--since", "1h", "--until=2026-09-10T00:00:00Z", "--by", "status", "demo"}
	for _, input := range [][]string{
		{"demo", "--since", "1h", "--until=2026-09-10T00:00:00Z", "--by", "status"},
		{"--since", "1h", "--until=2026-09-10T00:00:00Z", "--by", "status", "demo"},
	} {
		if got := normalizeAnalyticsArgs(input); !reflect.DeepEqual(got, want) {
			t.Fatalf("normalizeAnalyticsArgs(%v) = %v, want %v", input, got, want)
		}
	}
}

func TestRunSubcommandHelpIsLocalAndValidationStillFails(t *testing.T) {
	oldOut := osStdout
	defer func() { osStdout = oldOut }()
	for _, args := range [][]string{
		{"app", "--help"},
		{"secrets", "--help"},
		{"env", "--help"},
		{"inspect", "--help"},
	} {
		var stdout bytes.Buffer
		osStdout = &stdout
		if code := run(args); code != 0 {
			t.Errorf("run(%v) = %d, want help success", args, code)
		}
		if output := stdout.String(); !strings.Contains(output, "Usage:") || !strings.Contains(output, "gregale "+args[0]) {
			t.Errorf("run(%v) help output = %q", args, output)
		}
	}
	if code := run([]string{"inspect", "INVALID"}); code == 0 {
		t.Fatal("invalid inspect slug returned success")
	}
	if code := run([]string{"analytics", "demo", "extra"}); code == 0 {
		t.Fatal("extra analytics positional returned success")
	}
}

func TestBuildDiffOptionsResolvesResourceProfile(t *testing.T) {
	opts := buildDiffOptions("demo", shapeApp, "", "", "", t.TempDir(), nil, nil, "micro")
	if opts.AppConfig.RAMMB == nil || *opts.AppConfig.RAMMB != 128 {
		t.Fatalf("micro RAM projection = %v, want 128", opts.AppConfig.RAMMB)
	}
	if opts.AppConfig.CPUMillicores == nil || *opts.AppConfig.CPUMillicores != 250 {
		t.Fatalf("micro CPU projection = %v, want 250", opts.AppConfig.CPUMillicores)
	}
}

func TestAppRAMRejectsNonPositiveValuesBeforeAuthentication(t *testing.T) {
	for _, args := range [][]string{{"demo", "--ram", "-1"}, {"demo", "--ram", "0"}} {
		if code := cmdApp(args); code == 0 {
			t.Fatalf("cmdApp(%v) returned success", args)
		}
	}
	for _, value := range []string{"-1", "0"} {
		if code := cmdAppScale("demo", []string{"--ram", value}); code == 0 {
			t.Fatalf("cmdAppScale(--ram %s) returned success", value)
		}
	}
}
