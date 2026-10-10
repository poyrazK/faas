//go:build linux

package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"net"
	"testing"

	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestQualificationRestoreFrameworkReceiverAcceptsConfigAndPrivateReadyReceipt(t *testing.T) {
	manager := fcvm.NewManager(nil, nil, fcvm.Paths{}, "test", nil, nil)
	var recordedFrame state.EnvironmentQualificationExecution
	var recordedRuntime string
	var recordedWarmup int64
	receiver := (&FrameworkReadyReceiver{mgr: manager}).WithQualificationFrameworkReadyRecorder(
		func(_ context.Context, frame state.EnvironmentQualificationExecution, runtime string, warmup int64) error {
			recordedFrame, recordedRuntime, recordedWarmup = frame, runtime, warmup
			return nil
		})
	execution := state.EnvironmentQualificationExecution{InstanceID: "qualification-target"}
	receipt, err := json.Marshal(fcvm.EnvironmentQualificationConfigReceipt{
		Token: "12345678-1234-1234-1234-123456789012", Workload: fcvm.WorkloadNameMain,
		APIEnvSHA256:  "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		SecretKeysMAC: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
	})
	if err != nil {
		t.Fatal(err)
	}
	body := append([]byte{VsockFrameworkReadyHostTypeQualificationConfig}, receipt...)
	client, server := net.Pipe()
	go func() { _, _ = client.Write(body); _ = client.Close() }()
	err = receiver.handleQualificationRestoreStream(context.Background(), execution, server)
	_ = server.Close()
	if !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("config receipt without an active attempt waiter = %v, want not found", err)
	}

	client, server = net.Pipe()
	ready := make([]byte, 1+4+1+len("node22"))
	ready[0] = VsockFrameworkReadyHostTypeReady
	binary.BigEndian.PutUint32(ready[1:5], 17)
	ready[5] = 0
	copy(ready[6:], "node22")
	go func() { _, _ = client.Write(ready); _ = client.Close() }()
	err = receiver.handleQualificationRestoreStream(context.Background(), execution, server)
	_ = server.Close()
	if err != nil || recordedFrame != execution || recordedRuntime != "node22" || recordedWarmup != 17 {
		t.Fatalf("private framework-ready evidence: frame=%+v runtime=%q warmup=%d err=%v", recordedFrame, recordedRuntime, recordedWarmup, err)
	}

	client, server = net.Pipe()
	go func() { _, _ = client.Write([]byte{VsockFrameworkReadyHostTypeInitExit, '{', '}'}); _ = client.Close() }()
	err = receiver.handleQualificationRestoreStream(context.Background(), execution, server)
	_ = server.Close()
	if err == nil {
		t.Fatal("private qualification receiver accepted a sidecar lifecycle event")
	}
}

func TestQualificationRestoreIdentityAndRuntimeConfigStayUnavailable(t *testing.T) {
	workload := &WorkloadIdentityReceiver{}
	client, server := net.Pipe()
	workloadDone := make(chan error, 1)
	go func(server net.Conn) {
		workloadDone <- workload.handleQualificationRestoreStream(context.Background(), state.EnvironmentQualificationExecution{}, server)
		_ = server.Close()
	}(server)
	body, err := readIdentityFrame(client)
	_ = client.Close()
	if err != nil {
		t.Fatal("identity response frame:", err)
	}
	var identity workloadIdentityResponse
	if err := json.Unmarshal(body, &identity); err != nil || identity.Error != "qualification_identity_unavailable" || identity.Token.AccessToken != "" {
		t.Fatal("qualification target received workload identity", identity, err)
	}
	if err := <-workloadDone; err != nil {
		t.Fatal("identity denial response failed", err)
	}

	config := &runtimeConfigReceiver{}
	client, server = net.Pipe()
	configDone := make(chan error, 1)
	go func(server net.Conn) {
		configDone <- config.handleQualificationRestoreStream(context.Background(), state.EnvironmentQualificationExecution{}, server)
		_ = server.Close()
	}(server)
	body, err = readRuntimeConfigFrameLimit(client, runtimeConfigMaxFrame)
	_ = client.Close()
	if err != nil {
		t.Fatal("runtime config response frame:", err)
	}
	var response runtimeConfigResponse
	if err := json.Unmarshal(body, &response); err != nil || response.Error != "qualification_config_unavailable" {
		t.Fatal("qualification target received mutable runtime config", response, err)
	}
	if err := <-configDone; err != nil {
		t.Fatal("runtime config denial response failed", err)
	}
}

func TestPlatformReceiversRegisterSeparateQualificationHandlers(t *testing.T) {
	jailer := fcvm.NewJailerVMM(t.TempDir(), 0)
	manager := fcvm.NewManager(nil, nil, fcvm.Paths{}, "test", nil, nil)
	if _, err := StartFrameworkReadyReceiver(context.Background(), nil, manager, jailer); err != nil {
		t.Fatal("framework-ready receiver:", err)
	}
	if _, err := StartWorkloadIdentityReceiver(context.Background(), nil, manager, nil, jailer); err != nil {
		t.Fatal("workload-identity receiver:", err)
	}
	if _, err := StartRuntimeConfigReceiver(context.Background(), nil, manager, nil, jailer); err != nil {
		t.Fatal("runtime-config receiver:", err)
	}
	for _, port := range []uint32{VsockFrameworkReadyHostPort, VsockWorkloadIdentityHostPort, VsockRuntimeConfigHostPort} {
		err := jailer.RegisterEnvironmentQualificationRestoreStreamHandler(port, func(context.Context, state.EnvironmentQualificationExecution, net.Conn) error {
			return nil
		})
		if err == nil {
			t.Fatalf("qualification handler on port %d was not registered at receiver startup", port)
		}
	}
}
