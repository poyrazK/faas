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
	"slices"
	"strings"
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
		append([]byte{VsockFrameworkReadyHostTypeQualificationConfig}, mustJSON(t, fcvm.EnvironmentQualificationConfigReceipt{
			Token: "123e4567-e89b-12d3-a456-426614174000", Workload: fcvm.WorkloadNameMain, APIEnvSHA256: strings.Repeat("a", 64),
			SecretKeysMAC: strings.Repeat("b", 64),
		})...),
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

type recordingSidecarEmitter struct {
	initExits, restarts, health []string
}

func (e *recordingSidecarEmitter) EmitSidecarInitExit(_ context.Context, _, _, _, _ string, wire sidecarInitExitWire) {
	e.initExits = append(e.initExits, wire.Sidecar)
}

func (e *recordingSidecarEmitter) EmitSidecarRestart(_ context.Context, _, _, _ string, wire sidecarRestartWire) {
	e.restarts = append(e.restarts, wire.Sidecar)
}

func (e *recordingSidecarEmitter) EmitSidecarHealth(_ context.Context, _, _, _, _ string, wire sidecarHealthWire) {
	e.health = append(e.health, wire.Sidecar)
}

// Sidecar names on the guest event stream become Prometheus label values in
// root vmmd (vmmd_sidecar_restart_total and the health transition counter).
// A frame naming a sidecar the instance was not booted with is dropped, so a
// guest cannot mint unbounded series.
// adr: 069
func TestGuestEventStreamDropsUndeclaredSidecars(t *testing.T) {
	mgr := fcvm.NewManager(nil, nil, fcvm.Paths{}, "test", slog.New(slog.NewTextHandler(io.Discard, nil)), nil).
		RegisterInstanceForTest("instance-1", "deployment-1", "app-1", "account-1").
		SetInstanceSidecarsForTest("instance-1", "worker", "migrate")
	emitter := &recordingSidecarEmitter{}
	receiver := &FrameworkReadyReceiver{ctx: context.Background(), log: slog.New(slog.NewTextHandler(io.Discard, nil)), mgr: mgr, emitter: emitter}
	// Health also requires the stream's prepared VM identity (ADR-570).
	conn := fcvm.GuestVsockOriginConnForTest(nil, fcvm.GuestVsockStreamOrigin{AppID: "app-1"})
	for _, name := range []string{"worker", "attacker-1", "main", ""} {
		receiver.dispatchSidecarRestart("instance-1", sidecarRestartWire{Sidecar: name, Attempt: 1})
		receiver.dispatchSidecarHealth("instance-1", conn, sidecarHealthWire{Sidecar: name, Status: sidecarHealthHealthy})
	}
	for _, name := range []string{"migrate", "attacker-2"} {
		receiver.dispatchSidecarInitExit("instance-1", sidecarInitExitWire{Sidecar: name, Status: sidecarStatusInitOK})
	}
	if !slices.Equal(emitter.restarts, []string{"worker"}) || !slices.Equal(emitter.health, []string{"worker"}) || !slices.Equal(emitter.initExits, []string{"migrate"}) {
		t.Fatalf("emitted restarts=%q health=%q init=%q; want only declared sidecars", emitter.restarts, emitter.health, emitter.initExits)
	}
}
