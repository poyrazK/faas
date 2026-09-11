package fcvm

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/executionproto"
)

func TestManagerExecuteExecutionRejectsOrdinaryAppInstance(t *testing.T) {
	m := NewManager(&fakeRunner{}, &fakeVMM{}, Paths{}, "1.0.0", nil, nil)
	m.live["app-vm-1"] = &Instance{Lease: Lease{Instance: "app-vm-1"}}

	_, err := m.ExecuteExecution(context.Background(), "app-vm-1", executionproto.Request{
		Version:     executionproto.Version,
		ExecutionID: "exec-1",
		Runtime:     api.ExecutionRuntimeNode22,
		Source:      "1",
		Input:       json.RawMessage(`null`),
		TimeoutMS:   1000,
		MaxOutput:   1024,
		NetworkMode: api.ExecutionNetworkNone,
	})
	if !errors.Is(err, ErrExecutionInstanceNotFound) {
		t.Fatalf("error = %v, want ErrExecutionInstanceNotFound", err)
	}
}

type executionTestVMM struct{ *fakeVMM }

func (v *executionTestVMM) DialExecution(_ context.Context, _ Lease) (*ExecutionSession, error) {
	host, guest := net.Pipe()
	go func() {
		defer guest.Close()
		var header [8]byte
		if _, err := io.ReadFull(guest, header[:]); err != nil {
			return
		}
		bodyLen := binary.BigEndian.Uint32(header[4:])
		if bodyLen > 0 {
			_, _ = io.CopyN(io.Discard, guest, int64(bodyLen))
		}
		body, _ := json.Marshal(executionproto.Result{
			Status: api.ExecutionStatusSucceeded,
			Result: json.RawMessage(`{"ok":true}`),
		})
		binary.BigEndian.PutUint32(header[:4], executionproto.FrameResult)
		binary.BigEndian.PutUint32(header[4:], uint32(len(body)))
		_, _ = guest.Write(header[:])
		_, _ = guest.Write(body)
	}()
	return NewExecutionSession(host)
}

func TestManagerExecuteExecutionDestroysExecutionOnlyInstance(t *testing.T) {
	vmm := &executionTestVMM{fakeVMM: &fakeVMM{}}
	m := NewManager(&fakeRunner{}, vmm, Paths{}, "1.0.0", nil, nil)
	m.live["exec-vm-1"] = &Instance{
		Lease:         Lease{Instance: "exec-vm-1"},
		ExecutionOnly: true,
	}

	result, err := m.ExecuteExecution(context.Background(), "exec-vm-1", executionproto.Request{
		Version:     executionproto.Version,
		ExecutionID: "exec-1",
		Runtime:     api.ExecutionRuntimeNode22,
		Source:      "1",
		Input:       json.RawMessage(`null`),
		TimeoutMS:   1000,
		MaxOutput:   1024,
		NetworkMode: api.ExecutionNetworkNone,
	})
	if err != nil {
		t.Fatalf("ExecuteExecution: %v", err)
	}
	if result.Status != api.ExecutionStatusSucceeded || string(result.Result) != `{"ok":true}` {
		t.Fatalf("result = %+v", result)
	}
	if got := m.LiveCount(); got != 0 {
		t.Fatalf("live count = %d, want 0 after one-shot teardown", got)
	}
	vmm.mu.Lock()
	defer vmm.mu.Unlock()
	if len(vmm.destroyedWithExport) != 1 || vmm.destroyedWithExport[0] != "exec-vm-1" {
		t.Fatalf("destroy calls = %v, want [exec-vm-1]", vmm.destroyedWithExport)
	}
}
