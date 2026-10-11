//go:build linux

package main

import (
	"bytes"
	"encoding/binary"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/jobresult"
	"golang.org/x/sys/unix"
)

func TestWaitForJobStartIsBoundedAndCancellationWins(t *testing.T) {
	started := make(chan struct{})
	signals := make(chan os.Signal, 1)
	signals <- syscall.SIGTERM
	if released, signal := waitForJobStart(started, signals, time.Second); released || signal != syscall.SIGTERM {
		t.Fatalf("cancel-before-release = released %t, signal %v", released, signal)
	}
	if released, signal := waitForJobStart(make(chan struct{}), nil, time.Millisecond); released || signal != nil {
		t.Fatalf("unreleased gate = released %t, signal %v", released, signal)
	}
	releasedGate := make(chan struct{})
	close(releasedGate)
	if released, signal := waitForJobStart(releasedGate, signals, time.Second); !released || signal != nil {
		t.Fatalf("released gate = released %t, signal %v", released, signal)
	}
}

func TestHeldJobStartControlAcknowledgesIdempotentRelease(t *testing.T) {
	started := make(chan struct{})
	signals := make(chan os.Signal, 2)
	var once sync.Once
	for attempt := 0; attempt < 2; attempt++ {
		fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
		if err != nil {
			t.Fatal(err)
		}
		go handleJobStartControl(fds[1], started, signals, &once)
		var frame [8]byte
		binary.BigEndian.PutUint32(frame[:4], VsockJobStartMsgType)
		if _, err := unix.Write(fds[0], frame[:]); err != nil {
			_ = unix.Close(fds[0])
			t.Fatal(err)
		}
		var ack [1]byte
		if _, err := unix.Read(fds[0], ack[:]); err != nil || ack[0] != VsockJobControlAckOK {
			_ = unix.Close(fds[0])
			t.Fatalf("release attempt %d ack=%d err=%v", attempt, ack[0], err)
		}
		_ = unix.Close(fds[0])
	}
	select {
	case <-started:
	default:
		t.Fatal("start gate remained closed after acknowledged release")
	}
}

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

func TestBuildEnvForJobSecretOverridesJobEnvironment(t *testing.T) {
	const key = "GREGALE_JOB_SECRET_PRECEDENCE_TEST"
	t.Setenv(key, "system-value")
	job := JobManifest{Env: map[string]string{key: "job-value", "FAAS_JOB": "0"}}
	got := buildEnvForJobWithSecrets(job, map[string]string{key: "sealed-secret"})
	values := map[string]string{}
	for _, entry := range got {
		name, value, ok := strings.Cut(entry, "=")
		if ok {
			values[name] = value
		}
	}
	if values[key] != "sealed-secret" || values["FAAS_JOB"] != "1" || values["FAAS_RUNTIME_KIND"] != "job" {
		t.Fatalf("job environment precedence = secret %q, FAAS_JOB %q, runtime %q", values[key], values["FAAS_JOB"], values["FAAS_RUNTIME_KIND"])
	}
}
