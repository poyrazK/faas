// adr: 1330 — interactive image runs hold the job command for one attached session.
package fcvm

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/apptaskproto"
)

func validInteractiveJobSpec() JobColdBootSpec {
	return JobColdBootSpec{
		KernelKey: "kernel/v1", BaseKey: "base/base.ext4", ImageRef: "jobs/image.ext4",
		Command: []string{"/bin/sh"}, VcpuCount: 1, MemSizeMiB: 128, Tap: "tap0",
		TaskTimeoutSec: 600, LeaseToken: "lease-1", AccountID: "account-1", RunID: "run-1",
		Interactive: &JobInteractive{TTY: true, AttachTimeoutSec: 60},
	}
}

func TestJobColdBootSpecValidateInteractive(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*JobColdBootSpec)
		ok     bool
	}{
		{name: "valid", ok: true},
		{name: "valid at the limits", mutate: func(s *JobColdBootSpec) {
			s.TaskTimeoutSec, s.Interactive.AttachTimeoutSec = JobInteractiveMaxTaskTimeoutSec, 300
		}, ok: true},
		{name: "ordinary job keeps the host ceiling", mutate: func(s *JobColdBootSpec) {
			s.Interactive, s.TaskTimeoutSec = nil, JobMaxTaskTimeoutSec
		}, ok: true},
		{name: "start-held", mutate: func(s *JobColdBootSpec) { s.StartHeld = true }},
		{name: "task timeout past the session limit", mutate: func(s *JobColdBootSpec) { s.TaskTimeoutSec = JobInteractiveMaxTaskTimeoutSec + 1 }},
		{name: "no attach window", mutate: func(s *JobColdBootSpec) { s.Interactive.AttachTimeoutSec = 0 }},
		{name: "attach window past the cap", mutate: func(s *JobColdBootSpec) { s.Interactive.AttachTimeoutSec = 301 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spec := validInteractiveJobSpec()
			if tc.mutate != nil {
				tc.mutate(&spec)
			}
			if err := spec.Validate(); (err == nil) != tc.ok {
				t.Fatalf("Validate() = %v, want ok=%t", err, tc.ok)
			}
		})
	}
}

// guest-init decodes job.json with unknown fields disallowed, so these names
// are the guest contract (guest/init/job_supervisor_common.go).
func TestJobManifestInteractiveWireShape(t *testing.T) {
	for _, tc := range []struct {
		name        string
		interactive *JobInteractive
		want        string
	}{
		{name: "tty", interactive: &JobInteractive{TTY: true, AttachTimeoutSec: 60}, want: `"interactive":{"tty":true,"attach_timeout_s":60}`},
		{name: "pipes", interactive: &JobInteractive{AttachTimeoutSec: 60}, want: `"interactive":{"attach_timeout_s":60}`},
		{name: "ordinary"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := json.Marshal(JobManifest{Kind: "job", Command: []string{"/bin/sh"}, Interactive: tc.interactive})
			if err != nil {
				t.Fatal(err)
			}
			switch {
			case tc.want != "" && !strings.Contains(string(raw), tc.want):
				t.Fatalf("manifest %s lacks %s", raw, tc.want)
			case tc.want == "" && strings.Contains(string(raw), "interactive"):
				t.Fatalf("ordinary manifest carries an interactive field: %s", raw)
			}
		})
	}
}

func TestBootJobCarriesInteractiveToSpecAndInstance(t *testing.T) {
	for _, interactive := range []*JobInteractive{nil, {TTY: true, AttachTimeoutSec: 60}} {
		vmm := &fakeVMM{}
		m := newTestManager(&fakeRunner{}, vmm)
		req := testJobBootRequest("job-interactive")
		req.Interactive = interactive
		inst, err := m.BootJob(context.Background(), req)
		if err != nil {
			t.Fatalf("BootJob(%+v): %v", interactive, err)
		}
		if !inst.IsJob || inst.JobInteractive != (interactive != nil) {
			t.Fatalf("instance for %+v = IsJob %t JobInteractive %t", interactive, inst.IsJob, inst.JobInteractive)
		}
		vmm.mu.Lock()
		specs := append([]JobColdBootSpec(nil), vmm.jobBootSpecs...)
		vmm.mu.Unlock()
		if len(specs) != 1 || !reflect.DeepEqual(specs[0].Interactive, interactive) {
			t.Fatalf("job boot specs = %+v", specs)
		}
	}
}

func TestBootJobRejectsInvalidInteractiveSpec(t *testing.T) {
	vmm := &fakeVMM{}
	m := newTestManager(&fakeRunner{}, vmm)
	req := testJobBootRequest("job-held-interactive")
	req.StartHeld = true
	req.Interactive = &JobInteractive{AttachTimeoutSec: 60}
	if _, err := m.BootJob(context.Background(), req); err == nil {
		t.Fatal("BootJob accepted a start-held interactive job")
	}
	if got := m.LiveCount(); got != 0 {
		t.Fatalf("live count = %d after a rejected boot", got)
	}
}

// jobSessionTestVMM serves one interactive session over net.Pipe: stdin is
// echoed, stdin close exits 3.
type jobSessionTestVMM struct {
	*fakeVMM
	mu      sync.Mutex
	dialed  []string
	request apptaskproto.Request
}

func (v *jobSessionTestVMM) DialJobSession(ctx context.Context, lease Lease) (*AppTaskSession, error) {
	v.mu.Lock()
	v.dialed = append(v.dialed, lease.Instance)
	v.mu.Unlock()
	host, guest := net.Pipe()
	go func() {
		defer guest.Close()
		batch := func(context.Context, apptaskproto.Request, *apptaskproto.OutputWriter, *apptaskproto.OutputWriter) (apptaskproto.Result, error) {
			return apptaskproto.Result{}, errors.New("batch request on an interactive job")
		}
		_ = apptaskproto.ServeSession(ctx, guest, batch, func(_ context.Context, session *apptaskproto.InteractiveSession) (apptaskproto.Result, error) {
			v.mu.Lock()
			v.request = session.Request
			v.mu.Unlock()
			for event := range session.Input {
				switch event.Kind {
				case apptaskproto.InputStdin:
					_, _ = session.Stdout.Write(event.Data)
				case apptaskproto.InputStdinClose:
					exit := 3
					return apptaskproto.Result{Status: apptaskproto.StatusFailed, ExitCode: &exit, FailureCode: "command_failed"}, nil
				}
			}
			return apptaskproto.Result{}, errors.New("input closed")
		})
	}()
	return NewAppTaskSession(host)
}

func interactiveJobRequest() apptaskproto.Request {
	return apptaskproto.Request{Version: apptaskproto.InteractiveVersion, TaskID: "job-vm-1", Command: []string{"job"},
		TimeoutSeconds: 30, Interactive: true}
}

func TestManagerExecuteInteractiveJobKeepsTheVM(t *testing.T) {
	vmm := &jobSessionTestVMM{fakeVMM: &fakeVMM{}}
	m := NewManager(&fakeRunner{}, vmm, Paths{}, "1.0.0", nil, nil)
	lease, err := m.alloc.Acquire("job-vm-1")
	if err != nil {
		t.Fatal(err)
	}
	m.live["job-vm-1"] = &Instance{Lease: lease, IsJob: true, JobInteractive: true}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	input := make(chan apptaskproto.InputEvent, 2)
	input <- apptaskproto.InputEvent{Kind: apptaskproto.InputStdin, Data: []byte("ping")}
	input <- apptaskproto.InputEvent{Kind: apptaskproto.InputStdinClose}
	var mu sync.Mutex
	var output strings.Builder
	receive := func(_ context.Context, stream string, chunk []byte) error {
		mu.Lock()
		defer mu.Unlock()
		if stream == "stdout" {
			output.Write(chunk)
		}
		return nil
	}
	result, err := m.ExecuteInteractiveJob(ctx, "job-vm-1", interactiveJobRequest(), input, receive)
	if err != nil {
		t.Fatalf("ExecuteInteractiveJob: %v", err)
	}
	if result.ExitCode == nil || *result.ExitCode != 3 || result.Status != apptaskproto.StatusFailed {
		t.Fatalf("result = %+v", result)
	}
	mu.Lock()
	got := output.String()
	mu.Unlock()
	if got != "ping" {
		t.Fatalf("output = %q, want ping", got)
	}
	// The job exit path owns teardown: the session must not stop the VM.
	if n := m.LiveCount(); n != 1 {
		t.Fatalf("live count = %d, want 1", n)
	}
	vmm.mu.Lock()
	defer vmm.mu.Unlock()
	if len(vmm.dialed) != 1 || vmm.dialed[0] != "job-vm-1" || vmm.request.TaskID != "job-vm-1" || !vmm.request.Interactive {
		t.Fatalf("dialed %v with request %+v", vmm.dialed, vmm.request)
	}
	vmm.fakeVMM.mu.Lock()
	defer vmm.fakeVMM.mu.Unlock()
	if len(vmm.killed) != 0 || len(vmm.destroyedWithExport) != 0 {
		t.Fatalf("session stopped the VM: killed %v destroyed %v", vmm.killed, vmm.destroyedWithExport)
	}
}

func TestManagerExecuteInteractiveJobFences(t *testing.T) {
	batch := interactiveJobRequest()
	batch.Version, batch.Interactive, batch.MaxOutputBytes = apptaskproto.Version, false, 1024
	for _, tc := range []struct {
		name     string
		instance *Instance
		req      apptaskproto.Request
		dialer   bool
		want     error
	}{
		{name: "batch request", instance: &Instance{IsJob: true, JobInteractive: true}, req: batch, dialer: true, want: apptaskproto.ErrInvalidRequest},
		{name: "unknown instance", req: interactiveJobRequest(), dialer: true, want: ErrJobSessionInstanceNotFound},
		{name: "app task instance", instance: &Instance{AppTaskOnly: true}, req: interactiveJobRequest(), dialer: true, want: ErrJobSessionInstanceNotFound},
		{name: "ordinary job", instance: &Instance{IsJob: true}, req: interactiveJobRequest(), dialer: true, want: ErrJobSessionInstanceNotFound},
		{name: "vmm without job sessions", instance: &Instance{IsJob: true, JobInteractive: true}, req: interactiveJobRequest(), want: ErrAppTaskNotConfigured},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plain := &fakeVMM{}
			var vmm VMM = plain
			sessions := &jobSessionTestVMM{fakeVMM: plain}
			if tc.dialer {
				vmm = sessions
			}
			m := NewManager(&fakeRunner{}, vmm, Paths{}, "1.0.0", nil, nil)
			if tc.instance != nil {
				lease, err := m.alloc.Acquire("job-vm-1")
				if err != nil {
					t.Fatal(err)
				}
				tc.instance.Lease = lease
				m.live["job-vm-1"] = tc.instance
			}
			_, err := m.ExecuteInteractiveJob(context.Background(), "job-vm-1", tc.req, nil, nil)
			if !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
			sessions.mu.Lock()
			dialed := len(sessions.dialed)
			sessions.mu.Unlock()
			if dialed != 0 {
				t.Fatalf("dialed the guest %d times for a fenced request", dialed)
			}
			if tc.instance != nil && m.LiveCount() != 1 {
				t.Fatalf("live count = %d: a fenced request stopped the VM", m.LiveCount())
			}
		})
	}
}

// DialJobSession retries while guest-init is still starting, and its session
// never stops the VM; DialAppTask's session does.
func TestDialJobSessionWaitsForGuestAndNeverKills(t *testing.T) {
	base, err := os.MkdirTemp("/tmp", "gjs")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(base)
	root := filepath.Join(base, "fc", "job-vm-1", "root")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	sock := filepath.Join(root, VsockUDSSocketName)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	accepted := make(chan error, 2)
	go func() {
		// The guest listener appears only after the first dial attempts
		// fail, as it does while the job VM boots.
		time.Sleep(300 * time.Millisecond)
		listener, err := net.Listen("unix", sock)
		if err != nil {
			accepted <- err
			return
		}
		defer listener.Close()
		for range 2 {
			conn, err := listener.Accept()
			if err != nil {
				accepted <- err
				return
			}
			defer conn.Close()
			line, err := bufio.NewReader(conn).ReadString('\n')
			if err == nil && line != "CONNECT 1031\n" {
				err = &unexpectedHandshake{got: line}
			}
			if err == nil {
				_, err = conn.Write([]byte("OK 5000\n"))
			}
			accepted <- err
		}
		<-ctx.Done()
	}()

	v := &JailerVMM{chrootBase: base, fcName: "fc"}
	session, err := v.DialJobSession(ctx, Lease{Instance: "job-vm-1"})
	if err != nil {
		t.Fatalf("DialJobSession: %v", err)
	}
	if err := <-accepted; err != nil {
		t.Fatal(err)
	}
	if session.destroyVM != nil {
		t.Fatal("a job session carries a VM destroy func")
	}
	if err := session.Destroy(ctx); err != nil {
		t.Fatalf("Destroy: %v", err)
	}

	appSession, err := v.DialAppTask(ctx, Lease{Instance: "job-vm-1"})
	if err != nil {
		t.Fatalf("DialAppTask: %v", err)
	}
	if err := <-accepted; err != nil {
		t.Fatal(err)
	}
	if appSession.destroyVM == nil {
		t.Fatal("an app task session lost its VM destroy func")
	}
	_ = appSession.conn.Close()
}
