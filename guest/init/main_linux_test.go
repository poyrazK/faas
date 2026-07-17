//go:build linux

package main

import (
	"encoding/json"
	"testing"
	"testing/fstest"

	"github.com/onebox-faas/faas/pkg/api"
)

// TestDecideMode_BuildBeatsApp covers the precedence rule: a build manifest
// wins when both are present (defensive — base images normally carry at
// most one, but a misconfig shouldn't be a silent regression).
func TestDecideMode_BuildBeatsApp(t *testing.T) {
	fsys := fstest.MapFS{
		"etc/faas/build.json": &fstest.MapFile{Data: mustMarshal(t, api.BuildManifest{BuildID: "b", Framework: api.FrameworkRailpackNode, TimeoutSec: 60})},
		"etc/faas/app.json":    &fstest.MapFile{Data: []byte(`{"kind":"app"}`)},
	}
	mode, m, err := decideMode(fsys)
	if err != nil {
		t.Fatalf("decideMode: %v", err)
	}
	if mode != modeBuild {
		t.Fatalf("mode = %v, want modeBuild (build should beat app)", mode)
	}
	if m.BuildID != "b" || m.Framework != api.FrameworkRailpackNode || m.TimeoutSec != 60 {
		t.Errorf("manifest round-trip mismatch: %+v", m)
	}
}

func TestDecideMode_AppOnly(t *testing.T) {
	fsys := fstest.MapFS{
		"etc/faas/app.json": &fstest.MapFile{Data: []byte(`{}`)},
	}
	mode, _, err := decideMode(fsys)
	if err != nil {
		t.Fatalf("decideMode: %v", err)
	}
	if mode != modeApp {
		t.Errorf("mode = %v, want modeApp", mode)
	}
}

func TestDecideMode_BuildOnly(t *testing.T) {
	fsys := fstest.MapFS{
		"etc/faas/build.json": &fstest.MapFile{Data: mustMarshal(t, api.BuildManifest{BuildID: "b"})},
	}
	mode, m, err := decideMode(fsys)
	if err != nil {
		t.Fatalf("decideMode: %v", err)
	}
	if mode != modeBuild {
		t.Errorf("mode = %v, want modeBuild", mode)
	}
	if m.BuildID != "b" {
		t.Errorf("BuildID = %q, want b", m.BuildID)
	}
}

func TestDecideMode_BadJSONFallsBackToApp(t *testing.T) {
	fsys := fstest.MapFS{
		"etc/faas/build.json": &fstest.MapFile{Data: []byte(`{not json`)},
	}
	mode, _, _ := decideMode(fsys)
	if mode != modeApp {
		t.Errorf("mode = %v, want modeApp (garbage build.json must not panic)", mode)
	}
}

// TestClassifyExitCodes is the canonical exit-code → FailureClass mapping.
// builderd's ProcessOne consumes these strings.
func TestClassifyExitCodes(t *testing.T) {
	cases := []struct {
		code int
		want string
	}{
		{0, ""},
		{1, "FailureUserError"},
		{124, "FailureTimeout"},
		{137, "FailureOOM"},
		{-1, "FailureUserError"},
		{42, "FailureUserError"},
	}
	for _, c := range cases {
		if got := classify(c.code); got != c.want {
			t.Errorf("classify(%d) = %q, want %q", c.code, got, c.want)
		}
	}
}

// TestTailOf covers the LogTailBytes clamp.
func TestTailOf(t *testing.T) {
	if got := tailOf([]byte("hello"), 100); got != "hello" {
		t.Errorf("tailOf short = %q", got)
	}
	if got := tailOf([]byte("0123456789abcdef"), 4); got != "cdef" {
		t.Errorf("tailOf(long, 4) = %q, want cdef", got)
	}
	if got := tailOf([]byte("hello"), 0); got != "hello" {
		t.Errorf("tailOf(long, 0) = %q, want full", got)
	}
}

// TestBuildDoneShape round-trips a representative BuildDone payload through
// JSON to verify the field names match what builderd consumes. The actual
// writeAndPoweroff path is covered by the metal-loop integration test
// (`make metal-lima`); here we just lock the wire shape.
func TestBuildDoneShape(t *testing.T) {
	done := api.BuildDone{
		SchemaVersion: 1,
		BuildID:       "b-shape",
		ExitCode:      137,
		OCIImagePath:  "/build/out/image.tar",
		LogTail:       "step 1: ..., step 2: ...",
		FailureClass:  "FailureOOM",
	}
	data, err := json.Marshal(done)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got api.BuildDone
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.BuildID != "b-shape" || got.ExitCode != 137 || got.FailureClass != "FailureOOM" || got.OCIImagePath != "/build/out/image.tar" {
		t.Errorf("round-trip mismatch: %+v", got)
	}
}

func mustMarshal(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
