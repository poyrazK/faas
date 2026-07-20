package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestApplyJSONFlag_StripsFlagAndSetsOutput(t *testing.T) {
	resetJSONOutput()
	defer resetJSONOutput()
	got := applyJSONFlag([]string{"--json", "apps"})
	if got[0] != "apps" {
		t.Fatalf("expected --json stripped, got %v", got)
	}
	if !jsonOutput {
		t.Fatal("expected jsonOutput=true")
	}
}

func TestApplyJSONFlag_ShortFlag(t *testing.T) {
	resetJSONOutput()
	defer resetJSONOutput()
	got := applyJSONFlag([]string{"-j", "whoami"})
	if got[0] != "whoami" {
		t.Fatalf("expected -j stripped, got %v", got)
	}
	if !jsonOutput {
		t.Fatal("expected jsonOutput=true")
	}
}

func TestApplyJSONFlag_EqualsForm(t *testing.T) {
	resetJSONOutput()
	defer resetJSONOutput()
	got := applyJSONFlag([]string{"--json=true", "ps"})
	if got[0] != "ps" {
		t.Fatalf("expected --json=true stripped, got %v", got)
	}
	if !jsonOutput {
		t.Fatal("expected jsonOutput=true")
	}
}

func TestApplyJSONFlag_EqualsFalseOverridesEnv(t *testing.T) {
	resetJSONOutput()
	defer resetJSONOutput()
	t.Setenv("FAAS_JSON", "1")
	got := applyJSONFlag([]string{"--json=false", "usage"})
	if got[0] != "usage" {
		t.Fatalf("expected --json=false stripped, got %v", got)
	}
	if jsonOutput {
		t.Fatal("expected jsonOutput=false")
	}
}

func TestApplyJSONFlag_EnvFallback(t *testing.T) {
	resetJSONOutput()
	defer resetJSONOutput()
	t.Setenv("FAAS_JSON", "1")
	got := applyJSONFlag([]string{"status"})
	if len(got) != 1 || got[0] != "status" {
		t.Fatalf("expected args unchanged, got %v", got)
	}
	if !jsonOutput {
		t.Fatal("expected jsonOutput=true from env")
	}
}

func TestApplyJSONFlag_NoFlagNoOp(t *testing.T) {
	resetJSONOutput()
	defer resetJSONOutput()
	got := applyJSONFlag([]string{"status", "--foo"})
	if got[0] != "status" || got[1] != "--foo" {
		t.Fatalf("expected unchanged, got %v", got)
	}
	if jsonOutput {
		t.Fatal("expected jsonOutput=false")
	}
}

func TestApplyJSONFlag_Idempotent(t *testing.T) {
	resetJSONOutput()
	defer resetJSONOutput()
	first := applyJSONFlag([]string{"--json", "apps"})
	second := applyJSONFlag(first)
	if second[0] != "apps" {
		t.Fatalf("expected idempotent strip, got %v", second)
	}
}

func TestWriteJSON_IndentedScalar(t *testing.T) {
	resetJSONOutput()
	var buf bytes.Buffer
	prev := osStdout
	osStdout = &buf
	defer func() { osStdout = prev }()

	v := api.AccountResponse{Email: "a@b", Plan: "hobby", Status: "active"}
	if err := writeJSON(v); err != nil {
		t.Fatalf("writeJSON: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "\n  ") {
		t.Fatalf("expected indented output, got %q", out)
	}
	if !strings.HasSuffix(out, "\n") {
		t.Fatalf("expected trailing newline, got %q", out)
	}
	// Re-parse to confirm it's valid JSON.
	var roundtrip api.AccountResponse
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &roundtrip); err != nil {
		t.Fatalf("not valid JSON: %v\n%s", err, out)
	}
	if roundtrip.Email != "a@b" {
		t.Fatalf("roundtrip lost data: %+v", roundtrip)
	}
}

func TestWriteNDJSON_OneObjectPerLine(t *testing.T) {
	resetJSONOutput()
	var buf bytes.Buffer
	prev := osStdout
	osStdout = &buf
	defer func() { osStdout = prev }()

	items := []api.AppResponse{
		{Slug: "a", Status: "live"},
		{Slug: "b", Status: "parked"},
	}
	if err := writeNDJSON(items); err != nil {
		t.Fatalf("writeNDJSON: %v", err)
	}
	out := buf.String()
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d:\n%s", len(lines), out)
	}
	for i, l := range lines {
		var roundtrip api.AppResponse
		if err := json.Unmarshal([]byte(l), &roundtrip); err != nil {
			t.Fatalf("line %d not valid JSON: %v\n%s", i, err, l)
		}
	}
}

func TestWriteNDJSON_EmptySliceWritesNothing(t *testing.T) {
	resetJSONOutput()
	var buf bytes.Buffer
	prev := osStdout
	osStdout = &buf
	defer func() { osStdout = prev }()

	var items []api.AppResponse
	if err := writeNDJSON(items); err != nil {
		t.Fatalf("writeNDJSON: %v", err)
	}
	if buf.Len() != 0 {
		t.Fatalf("expected empty output, got %q", buf.String())
	}
}

func TestWriteJSONProblem_WritesOneJSONLineToStderr(t *testing.T) {
	resetJSONOutput()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	prev := os.Stderr
	os.Stderr = w
	defer func() { os.Stderr = prev }()

	p := api.Problem{Status: 404, Code: api.CodeNotFound, Title: "Not found", Detail: "app missing"}
	if err := writeJSONProblem(p); err != nil {
		t.Fatalf("writeJSONProblem: %v", err)
	}
	_ = w.Close()
	data, _ := io.ReadAll(r)
	out := string(data)
	if strings.Count(out, "\n") != 1 {
		t.Fatalf("expected single newline, got %q", out)
	}
	var roundtrip api.Problem
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &roundtrip); err != nil {
		t.Fatalf("not valid JSON: %v\n%s", err, out)
	}
	if roundtrip.Code != api.CodeNotFound {
		t.Fatalf("lost code: %+v", roundtrip)
	}
}

func TestJsonOut_NilIsZero(t *testing.T) {
	if got := jsonOut(nil); got != 0 {
		t.Fatalf("expected 0, got %d", got)
	}
}
