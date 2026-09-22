//go:build linux

package main

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/binary"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/workloadidentity"
)

func TestGuestEventStreamDispatchesClosedEventSet(t *testing.T) {
	mgr := fcvm.NewManager(nil, nil, fcvm.Paths{}, "test", slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	mgr.RegisterInstanceForTest("instance-1", "deployment-1")
	receiver := &FrameworkReadyReceiver{ctx: context.Background(), log: slog.New(slog.NewTextHandler(io.Discard, nil)), mgr: mgr, emitter: noopSidecarEventEmitter{}}

	tail := make([]byte, 16)
	tail[0] = VsockFrameworkReadyHostTypeTail
	tail[1] = tailEventOutcomeCompleted
	binary.BigEndian.PutUint64(tail[8:], 17)
	frames := [][]byte{
		{VsockFrameworkReadyHostTypeReady, 0},
		append([]byte{VsockFrameworkReadyHostTypeInitExit}, mustJSON(t, sidecarInitExitWire{Sidecar: "migrate", Status: sidecarStatusInitOK})...),
		append([]byte{VsockFrameworkReadyHostTypeRestart}, mustJSON(t, sidecarRestartWire{Sidecar: "worker", Attempt: 1})...),
		append([]byte{VsockFrameworkReadyHostTypeSidecarHealth}, mustJSON(t, sidecarHealthWire{Sidecar: "worker", Status: sidecarHealthHealthy, Reason: "started"})...),
		tail,
		append([]byte{VsockFrameworkReadyHostTypeWorkloadOOM}, mustJSON(t, workloadOOMWire{PeakMB: 512, PlanMB: 256})...),
		append([]byte{VsockFrameworkReadyHostTypeDisk}, mustJSON(t, diskUsageWire{UsedBytes: 20, CapacityBytes: 100})...),
	}
	for _, frame := range frames {
		server, client := net.Pipe()
		done := make(chan struct {
			kind string
			err  error
		}, 1)
		go func() {
			kind, err := receiver.handleGuestStream("instance-1", server)
			done <- struct {
				kind string
				err  error
			}{kind, err}
		}()
		if _, err := client.Write(frame); err != nil {
			t.Fatal(err)
		}
		_ = client.Close()
		select {
		case result := <-done:
			if result.err != nil || result.kind != "" {
				t.Fatalf("frame type 0x%02x: kind=%q err=%v", frame[0], result.kind, result.err)
			}
		case <-time.After(time.Second):
			t.Fatalf("frame type 0x%02x timed out", frame[0])
		}
	}
}

func TestWorkloadIdentityUsesInstanceBoundUnixStream(t *testing.T) {
	mgr := fcvm.NewManager(nil, nil, fcvm.Paths{}, "test", nil, nil).
		RegisterInstanceForTest("instance-1", "deployment-1", "app-1", "account-1")
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := workloadidentity.NewSigner(key, workloadidentity.DefaultIssuer, "test-key", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	receiver := &WorkloadIdentityReceiver{ctx: context.Background(), log: slog.New(slog.NewTextHandler(io.Discard, nil)), mgr: mgr, signer: signer, now: time.Now}
	server, client := net.Pipe()
	done := make(chan error, 1)
	go func() {
		kind, err := receiver.handleGuestStream("instance-1", server)
		if kind != "" {
			done <- &receiverTestError{kind: kind, err: err}
			return
		}
		done <- err
	}()
	body := mustJSON(t, workloadIdentityRequest{Audience: "https://api.example.test"})
	frame := make([]byte, 4+len(body))
	binary.BigEndian.PutUint32(frame[:4], uint32(len(body)))
	copy(frame[4:], body)
	if _, err := client.Write(frame); err != nil {
		t.Fatal(err)
	}
	responseBody, err := readIdentityFrame(client)
	if err != nil {
		t.Fatal(err)
	}
	var response workloadIdentityResponse
	if err := json.Unmarshal(responseBody, &response); err != nil {
		t.Fatal(err)
	}
	if response.Error != "" || response.AccessToken == "" || response.TokenType != "Bearer" {
		t.Fatalf("response = %+v", response)
	}
	_ = client.Close()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestGuestReceiverStartupDoesNotBindHostCID(t *testing.T) {
	jailer := fcvm.NewJailerVMM(t.TempDir(), time.Second)
	if _, err := StartFrameworkReadyReceiver(context.Background(), nil, nil, jailer); err != nil {
		t.Fatalf("event receiver registration: %v", err)
	}
	if _, err := StartWorkloadIdentityReceiver(context.Background(), nil, nil, nil, jailer); err != nil {
		t.Fatalf("identity receiver registration: %v", err)
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

type receiverTestError struct {
	kind string
	err  error
}

func (e *receiverTestError) Error() string { return e.kind + ": " + e.err.Error() }
