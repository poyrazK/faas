package e2etest_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/onebox-faas/faas/pkg/e2etest"
)

// CallerDir must find this checkout's package directory with and without
// -trimpath; go test runs the test binary in that directory.
func TestCallerDirIsThePackageDirectory(t *testing.T) {
	got, err := e2etest.CallerDir()
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	gotReal, err := filepath.EvalSymlinks(got)
	if err != nil {
		t.Fatal(err)
	}
	wantReal, err := filepath.EvalSymlinks(want)
	if err != nil {
		t.Fatal(err)
	}
	if gotReal != wantReal {
		t.Fatalf("CallerDir() = %q, want %q", got, want)
	}
}
