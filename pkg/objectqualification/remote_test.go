//go:build linux

package objectqualification

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectnode"
	"golang.org/x/time/rate"
)

func TestRemoteConfigurationAndReplicationBounds(t *testing.T) {
	o := DefaultOptions()
	o.Daemon, o.Control, o.Directory = "/bin/true", "/bin/true", filepath.Join(t.TempDir(), "new")
	for _, mutate := range []func(*Options){
		func(o *Options) { o.WorkBytesPerSecond = 0 },
		func(o *Options) { o.WorkEntriesPerSecond = api.ObjectNodeReplicationMaxWorkEntries + 1 },
		func(o *Options) { o.ReplicationDelay = api.ObjectQualificationMaxNetworkDelay + 1 },
		func(o *Options) { o.ReplicationBytesPerSecond = -1 },
		func(o *Options) { o.Remote.AllowSharedHost = true },
		func(o *Options) { o.RequireDedicatedStorage = true },
		func(o *Options) {
			o.RequireDedicatedStorage = true
			o.Remote = RemoteOptions{Host: "host", Agent: "/agent", Daemon: "/daemon", Control: "/ctl", Directory: "/data", AllowSharedHost: true}
		},
		func(o *Options) {
			o.Remote = RemoteOptions{Host: "-oProxyCommand=bad", Agent: "/agent", Daemon: "/daemon", Control: "/ctl", Directory: "/data"}
		},
		func(o *Options) { o.Remote = RemoteOptions{Host: "host", Agent: "relative"} },
	} {
		bad := o
		mutate(&bad)
		if err := bad.validate(); err == nil {
			t.Fatal("unsafe configuration accepted", bad)
		}
	}
	o.Remote = RemoteOptions{Host: "operator@standby", Agent: "/path with 'quotes'/agent", Daemon: "/daemon", Control: "/ctl", Directory: "/disposable"}
	if err := o.validate(); err != nil {
		t.Fatal(err)
	}
	if got := shellArgument("a'b$(ignored)"); got != "'a'\"'\"'b$(ignored)'" {
		t.Fatal("remote executable not shell quoted", got)
	}
}

func TestBackupTransferRejectsEscapesAliasesAndReplacement(t *testing.T) {
	root := t.TempDir()
	if err := writeTransfer(root, "data/file", 0, []byte("first")); err != nil {
		t.Fatal(err)
	}
	if err := writeTransfer(root, "data/file", 5, []byte("second")); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []struct {
		name   string
		offset int64
		data   []byte
	}{
		{"../escape", 0, nil}, {"/escape", 0, nil}, {"data/file", 0, nil}, {"data/file", 1, nil}, {"data/large", 0, make([]byte, api.ObjectQualificationAgentChunkBytes+1)},
	} {
		if err := writeTransfer(root, bad.name, bad.offset, bad.data); err == nil {
			t.Fatal("unsafe chunk accepted", bad.name, bad.offset)
		}
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(root, "outside")); err != nil {
		t.Fatal(err)
	}
	if err := writeTransfer(root, "outside/file", 0, nil); err == nil {
		t.Fatal("symlink escape accepted")
	}
	if err := os.Link(filepath.Join(root, "data/file"), filepath.Join(root, "alias")); err != nil {
		t.Fatal(err)
	}
	if _, err := readTransfer(root, "alias", 0); err == nil {
		t.Fatal("hard link source accepted")
	}
	if err := writeTransfer(root, "data/file", 11, nil); err == nil {
		t.Fatal("hard link destination accepted")
	}
}

func TestAgentDisconnectStopsAndReapsFollower(t *testing.T) {
	root := t.TempDir()
	ready := filepath.Join(root, "ready")
	t.Setenv("FAAS_QUALIFICATION_AGENT_READY", ready)
	control := filepath.Join(root, "control")
	if err := os.WriteFile(control, []byte("#!/bin/sh\nprintf '%s' \"$$\" > \"$FAAS_QUALIFICATION_AGENT_READY\"\nexec /bin/sleep 3600\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	input, writer := io.Pipe()
	reader, output := io.Pipe()
	done := make(chan error, 1)
	go func() { done <- ServeAgent(t.Context(), input, output); _ = output.Close() }()
	defer func() { _ = writer.Close(); _ = reader.Close() }()
	encoder, scanner := json.NewEncoder(writer), bufio.NewScanner(reader)
	call := func(request agentRequest) agentResponse {
		t.Helper()
		if err := encoder.Encode(request); err != nil {
			t.Fatal(err)
		}
		if !scanner.Scan() {
			t.Fatal("agent response absent", scanner.Err())
		}
		var response agentResponse
		if err := json.Unmarshal(scanner.Bytes(), &response); err != nil || response.Error != "" {
			t.Fatal(response, err)
		}
		return response
	}
	response := call(agentRequest{Operation: "init", Init: &agentInit{Directory: filepath.Join(root, "node"), Daemon: "/bin/true", Control: control, Key: "synthetic", Secret: "isolated", Workers: 1, WorkBytes: api.ObjectNodeReplicationDefaultWorkBytes, WorkEntries: api.ObjectNodeReplicationDefaultWorkEntries}})
	if response.Identity == nil || response.Identity.KernelBootSHA256 == "" {
		t.Fatal("missing remote identity")
	}
	call(agentRequest{Operation: "start-follower", Source: "http://127.0.0.1:1"})
	wait, cancel := context.WithTimeout(t.Context(), api.ObjectQualificationStartupTimeout)
	defer cancel()
	if err := poll(wait, func() (bool, error) { _, err := os.Stat(ready); return err == nil, nil }); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(ready)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(string(data))
	if err != nil {
		t.Fatal(err)
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-wait.Done():
		t.Fatal("agent did not stop on EOF")
	}
	if err = syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatal("disconnected follower survived", pid, err)
	}
}

func TestAgentBackupTransferPreservesEmptyDirectories(t *testing.T) {
	root := t.TempDir()
	origin, seed := filepath.Join(root, "origin"), filepath.Join(root, "seed")
	store, err := objectnode.Open(origin)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.CreateBucket("physical"); err != nil {
		t.Fatal(err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = objectnode.EnableReplication(t.Context(), origin); err != nil {
		t.Fatal(err)
	}
	if _, err = objectnode.Backup(t.Context(), origin, seed); err != nil {
		t.Fatal(err)
	}
	input, writer := io.Pipe()
	reader, output := io.Pipe()
	done := make(chan error, 1)
	go func() { done <- ServeAgent(t.Context(), input, output); _ = output.Close() }()
	defer func() { _ = writer.Close(); _ = reader.Close(); <-done }()
	r := &remoteAgent{input: writer, encoder: json.NewEncoder(writer), scanner: bufio.NewScanner(reader), gate: make(chan struct{}, 1)}
	r.scanner.Buffer(make([]byte, api.ObjectQualificationAgentChunkBytes), api.ObjectQualificationAgentLineBytes)
	_, err = r.call(t.Context(), agentRequest{Operation: "init", Init: &agentInit{Directory: filepath.Join(root, "agent"), Daemon: "/bin/true", Control: "/bin/true", Key: "synthetic", Secret: "isolated", Workers: 1, WorkBytes: api.ObjectNodeReplicationDefaultWorkBytes, WorkEntries: api.ObjectNodeReplicationDefaultWorkEntries}})
	if err != nil {
		t.Fatal(err)
	}
	if err = r.uploadBackup(t.Context(), seed); err != nil {
		t.Fatal(err)
	}
	result, err := r.call(t.Context(), agentRequest{Operation: "bootstrap"})
	if err != nil || result.Status.Role != "follower" {
		t.Fatal("transferred backup rejected", result.Status, err)
	}
}

func TestReplicationNetworkProfileCancelsAndLeavesCustomerTrafficAvailable(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ok") }))
	defer upstream.Close()
	p := newFaultProxy()
	defer p.server.Close()
	defer p.transport.CloseIdleConnections()
	o := DefaultOptions()
	o.ReplicationDelay = time.Second
	p.configureNetwork(o)
	p.target.Store(upstream.URL)
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.server.URL+"/?"+objectnode.ReplicationQuery+"=status", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.DefaultClient.Do(req)
	if response != nil {
		_ = response.Body.Close()
	}
	if err == nil {
		t.Fatal("delayed replication ignored cancellation")
	}
	response, err = http.Get(p.server.URL + "/physical/key")
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatal("customer traffic affected", response.StatusCode)
	}
	limiter := rate.NewLimiter(1, api.ObjectQualificationAgentChunkBytes)
	if err = limiter.WaitN(t.Context(), api.ObjectQualificationAgentChunkBytes); err != nil {
		t.Fatal(err)
	}
	body := &shapedBody{ReadCloser: io.NopCloser(strings.NewReader("body")), ctx: ctx, limiter: limiter}
	if n, err := body.Read(make([]byte, 1)); n != 0 || err == nil {
		t.Fatal("payload bandwidth ignored cancellation", n, err)
	}
}

func TestQualificationSSHProcesses(t *testing.T) {
	if os.Getenv("FAAS_OBJECT_QUALIFICATION_SSH_HOST") == "" {
		t.Skip("set SSH qualification fixture variables; see operating guide")
	}
	o := DefaultOptions()
	o.Daemon, o.Control = os.Getenv("FAAS_OBJECT_QUALIFICATION_DAEMON"), os.Getenv("FAAS_OBJECT_QUALIFICATION_CTL")
	o.Remote = RemoteOptions{Host: os.Getenv("FAAS_OBJECT_QUALIFICATION_SSH_HOST"), SSHConfig: os.Getenv("FAAS_OBJECT_QUALIFICATION_SSH_CONFIG"), Agent: os.Getenv("FAAS_OBJECT_QUALIFICATION_AGENT"), Daemon: o.Daemon, Control: o.Control, Directory: os.Getenv("FAAS_OBJECT_QUALIFICATION_REMOTE_DIR"), AllowSharedHost: os.Getenv("FAAS_OBJECT_QUALIFICATION_SHARED_HOST") == "1"}
	o.Directory = filepath.Join(t.TempDir(), "controller")
	o.Cycles, o.MaxWorkflows, o.Duration = 1, 4, 30*time.Second
	report, err := Run(t.Context(), o)
	if err != nil || !report.Passed || len(report.Runs) != 2 {
		data, _ := json.MarshalIndent(report, "", "  ")
		t.Fatal(string(data), err)
	}
	for _, mode := range report.Runs {
		if mode.Topology == nil || mode.Resources.RemoteSamples < 1 || len(mode.Checks) != 12 || len(mode.Recoveries) != 3 {
			t.Fatal("incomplete SSH coverage", mode)
		}
		if !o.Remote.AllowSharedHost && !mode.Topology.DistinctKernelBoots {
			t.Fatal("host separation unverified")
		}
		for _, recovery := range mode.Recoveries {
			if !recovery.CaughtUp {
				t.Fatal("rejoin failed", recovery)
			}
		}
	}
}
