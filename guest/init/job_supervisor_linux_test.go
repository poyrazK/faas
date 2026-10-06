//go:build linux

package main

import (
	"bytes"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/jobresult"
)

func TestSuperviseJobCommandCapturesExit(t *testing.T) {
	payload := superviseJobCommand(JobManifest{
		Command:        []string{"/bin/sh", "-c", "exit 7"},
		TaskTimeoutSec: 5,
		LeaseToken:     "lease-1",
	}, nil, 50*time.Millisecond, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if payload.ExitCode != 7 || payload.ErrorClass != "failed" || payload.Signal != 0 {
		t.Fatalf("payload = %+v, want failed exit 7", payload)
	}
	if payload.LeaseToken != "lease-1" || payload.FinishedAtUnixNano == 0 {
		t.Fatalf("payload metadata = %+v", payload)
	}
}

func TestSuperviseJobCommandEnforcesTimeout(t *testing.T) {
	payload := superviseJobCommand(JobManifest{
		Command:        []string{"/bin/sh", "-c", "trap '' TERM; sleep 30"},
		TaskTimeoutSec: 1,
		LeaseToken:     "lease-timeout",
	}, nil, 25*time.Millisecond, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if payload.ExitCode != 124 || payload.ErrorClass != "timeout" {
		t.Fatalf("payload = %+v, want timeout exit 124", payload)
	}
}

func TestSuperviseJobCommandCapturesStdoutAndStderr(t *testing.T) {
	var stdout, stderr bytes.Buffer
	payload := superviseJobCommandWithOutput(JobManifest{
		Command:        []string{"/bin/sh", "-c", "printf stdout-marker; printf stderr-marker >&2"},
		TaskTimeoutSec: 5,
		LeaseToken:     "lease-output",
	}, nil, 50*time.Millisecond, slog.New(slog.NewTextHandler(io.Discard, nil)), &stdout, &stderr)
	if payload.ExitCode != 0 || payload.ErrorClass != "succeeded" {
		t.Fatalf("payload = %+v, want succeeded", payload)
	}
	if got := stdout.String(); got != "stdout-marker" {
		t.Fatalf("stdout = %q, want stdout-marker", got)
	}
	if got := stderr.String(); got != "stderr-marker" {
		t.Fatalf("stderr = %q, want stderr-marker", got)
	}
}

func TestSuperviseJobCommandShipsStructuredOutcomeForFailedPartition(t *testing.T) {
	manifestPath := filepath.Join(t.TempDir(), "result.json")
	command := []string{"/bin/sh", "-c", `printf '%s' '{"version":1,"artifacts":[{"name":"partial","uri":"s3://results/partial.bin","size_bytes":0,"sha256":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}],"outcome_code":"invalid_record"}' > "$GREGALE_OUTPUT_MANIFEST_PATH"; exit 1`}
	job := JobManifest{
		Command: command, TaskTimeoutSec: 5, LeaseToken: "lease-outcome",
		Env: map[string]string{"GREGALE_OUTPUT_MANIFEST_PATH": manifestPath},
	}
	payload := superviseJobCommand(job, buildEnvForJob(job), 50*time.Millisecond,
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	if payload.ExitCode != 1 || payload.ErrorClass != "failed" {
		t.Fatalf("payload = %+v, want failed exit 1", payload)
	}
	result, err := jobresult.Validate(payload.OutputManifest)
	if err != nil || result.OutcomeCode != "invalid_record" || len(result.Artifacts) != 0 {
		t.Fatalf("failed result manifest = %+v, err %v; want outcome without artifacts", result, err)
	}
}
