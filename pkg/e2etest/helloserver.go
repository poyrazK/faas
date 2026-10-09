package e2etest

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
)

// helloServerSource is the fixture server's package, relative to this file.
// It lives under testdata so `go build ./...` ignores it; the harness builds
// it explicitly.
const helloServerSource = "testdata/helloserver"

var (
	helloServerOnce  sync.Once
	helloServerBytes []byte
	helloServerErr   error
)

// helloServerBinary returns the fixture server built once per process as a
// static Linux/amd64 binary matching the fixture OCI platform and production
// fleet, independently of the developer machine architecture.
func helloServerBinary() ([]byte, error) {
	helloServerOnce.Do(func() {
		// Hosts without a matching Go toolchain (the native test node) supply
		// a prebuilt static binary instead of compiling the fixture.
		if prebuilt := os.Getenv("FAAS_E2E_HELLO_SERVER_BINARY"); prebuilt != "" {
			helloServerBytes, helloServerErr = os.ReadFile(prebuilt)
			return
		}
		dir, err := os.MkdirTemp("", "faas-e2e-helloserver-*")
		if err != nil {
			helloServerErr = err
			return
		}
		defer func() { _ = os.RemoveAll(dir) }()
		out := filepath.Join(dir, "hello-server")
		if err := buildHelloServer("linux", "amd64", out); err != nil {
			helloServerErr = err
			return
		}
		helloServerBytes, helloServerErr = os.ReadFile(out)
	})
	return helloServerBytes, helloServerErr
}

// buildHelloServer compiles the fixture server for goos/goarch to out with
// CGO disabled, so the result runs in an image that has no libc.
func buildHelloServer(goos, goarch, out string) error {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		return fmt.Errorf("helloserver: cannot locate source")
	}
	src := filepath.Join(filepath.Dir(thisFile), helloServerSource)
	cmd := exec.Command("go", "build", "-trimpath", "-ldflags=-s -w", "-o", out, ".")
	cmd.Dir = src
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS="+goos, "GOARCH="+goarch)
	if b, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("helloserver: go build in %s: %w\n%s", src, err, b)
	}
	return nil
}
