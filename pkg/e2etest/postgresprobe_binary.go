package e2etest

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"time"
)

var postgresProbeOnce sync.Once
var postgresProbeBytes []byte
var postgresProbeErr error

// PostgresProbeExecutable builds the disposable customer application from
// this checkout. Its scratch Dockerfile needs no registry or package downloads.
// /bin/sh is the same binary with an adapter for the exact fixture release argv.
func PostgresProbeExecutable() ([]byte, error) {
	postgresProbeOnce.Do(func() {
		dir, err := os.MkdirTemp("", "gregale-postgres-probe-*")
		if err != nil {
			postgresProbeErr = err
			return
		}
		defer func() { _ = os.RemoveAll(dir) }()
		_, source, _, ok := runtime.Caller(0)
		if !ok {
			postgresProbeErr = fmt.Errorf("cannot locate PostgreSQL probe source")
			return
		}
		output := filepath.Join(dir, "postgres-probe")
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		command := exec.CommandContext(ctx, "go", "build", "-p", "1", "-trimpath", "-buildvcs=false", "-ldflags=-s -w", "-o", output, ".")
		command.Dir = filepath.Join(filepath.Dir(source), "testdata", "postgresprobe")
		command.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH=amd64")
		if err := command.Run(); err != nil {
			postgresProbeErr = fmt.Errorf("build PostgreSQL probe: %w", err)
			return
		}
		postgresProbeBytes, postgresProbeErr = os.ReadFile(output)
	})
	return bytes.Clone(postgresProbeBytes), postgresProbeErr
}
