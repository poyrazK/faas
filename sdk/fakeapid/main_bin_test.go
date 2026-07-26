//go:build smoke_bin

// This file is the canonical artifact-compatibility check. The
// un-tagged `go test ./...` (run by sdk-fakeapid CI) builds the
// fixture from source and spawns it. The smoke_bin-tagged variant
// spawns the pre-built ./bin/fakeapid binary instead, which is
// what PR 5 (Node SDK) and PR 6 (Python SDK) will use from their
// own CI to assert the binary Node/Python ship is the same binary
// the Go SDK smoke tests.
//
// Invoke from sdk/fakeapid/ as:
//
//	go test -tags smoke_bin -count=1 -run TestPreBuiltBinary ./...
//
// The build is conditional on ./bin/fakeapid being present. The
// test fails fast with a clear message if it is missing, so CI
// scripts know to run `go build -o bin/fakeapid .` first.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

// preBuiltBinaryPath is the conventional artifact path.
const preBuiltBinaryPath = "./bin/fakeapid"

// requirePreBuilt fails the test if the binary is not present. We
// don't auto-build here: smoke_bin is a "the artifact is shipped"
// check, not a build-from-source check.
func requirePreBuilt(t *testing.T) string {
	t.Helper()
	abs, err := filepath.Abs(preBuiltBinaryPath)
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	if _, err := os.Stat(abs); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			t.Fatalf("smoke_bin requires %s; run `go build -o bin/fakeapid .` first", abs)
		}
		t.Fatalf("stat %s: %v", abs, err)
	}
	return abs
}

// TestPreBuiltBinary_BootsAndServes is the smoke_bin equivalent
// of TestSpawnedBinary_BootsAndServes (in main_test.go) — it
// spawns the pre-built binary instead of building from source.
// Same assertions: /v1/account returns the AccountResponse shape,
// /v1/apps/missing-app-404 returns 404 application/problem+json.
func TestPreBuiltBinary_BootsAndServes(t *testing.T) {
	bin := requirePreBuilt(t)
	base, stop := spawnFakeAPID(t, bin)
	defer stop()

	resp, err := http.Get(base + "/v1/account")
	if err != nil {
		t.Fatalf("GET /v1/account: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("/v1/account status: got %d, want 200", resp.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["plan"] != "hobby" {
		t.Errorf("plan: got %v, want hobby", body["plan"])
	}

	resp2, err := http.Get(base + "/v1/apps/missing-app-404")
	if err != nil {
		t.Fatalf("GET /v1/apps/missing-app-404: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusNotFound {
		t.Errorf("status: got %d, want 404", resp2.StatusCode)
	}
	if ct := resp2.Header.Get("Content-Type"); ct != "application/problem+json" {
		t.Errorf("Content-Type: got %q, want application/problem+json", ct)
	}
}

// Keep the unused-imports reference; smoke_bin is a separate
// compilation unit and the linter can't see what the un-tagged
// file pulled in.
var _ = bytes.NewBuffer
