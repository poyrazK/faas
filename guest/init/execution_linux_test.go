//go:build linux

package main

import (
	"context"
	"encoding/json"
	"net"
	"testing"
	"testing/fstest"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/executionproto"
)

func TestDecideMode_ExecutionMarkerWins(t *testing.T) {
	fsys := fstest.MapFS{
		"etc/faas/execution.json": &fstest.MapFile{Data: []byte(`{"kind":"execution","version":1}`)},
		"etc/faas/job.json":       &fstest.MapFile{Data: []byte(`{"kind":"job"}`)},
		"etc/faas/build.json":     &fstest.MapFile{Data: []byte(`{"kind":"build"}`)},
	}
	mode, _, err := decideMode(fsys)
	if err != nil {
		t.Fatalf("decideMode: %v", err)
	}
	if mode != modeExecution {
		t.Fatalf("mode = %v, want modeExecution", mode)
	}
}

func TestDecideMode_InvalidExecutionMarkerFailsClosed(t *testing.T) {
	for _, data := range [][]byte{
		[]byte(`{"kind":"execution","version":2}`),
		[]byte(`{"kind":"app","version":1}`),
		[]byte(`{"kind":"execution","version":1} {}`),
		[]byte(`not-json`),
	} {
		fsys := fstest.MapFS{"etc/faas/execution.json": &fstest.MapFile{Data: data}}
		if _, _, err := decideMode(fsys); err == nil {
			t.Fatalf("decideMode accepted invalid marker %q", data)
		}
	}
}

func TestValidateExecutionManifestRejectsUnknownFields(t *testing.T) {
	if err := validateExecutionManifest([]byte(`{"kind":"execution","version":1,"source":"bad"}`)); err == nil {
		t.Fatal("unknown marker field was accepted")
	}
}

func TestServeExecutionOnceHandlesOneRequest(t *testing.T) {
	server, client := net.Pipe()
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	handler := func(_ context.Context, req executionproto.Request, out, _ *executionproto.OutputWriter) (executionproto.Result, error) {
		if req.ExecutionID != "exec-test" {
			t.Fatalf("execution id = %q", req.ExecutionID)
		}
		_, _ = out.Write([]byte("hello"))
		return executionproto.Result{Status: api.ExecutionStatusSucceeded, Result: json.RawMessage(`{"ok":true}`)}, nil
	}
	serveDone := make(chan error, 1)
	ln := oneConnListener{conn: server}
	go func() { serveDone <- serveExecutionOnce(ctx, ln, handler) }()

	clientCtx, clientCancel := context.WithTimeout(context.Background(), time.Second)
	defer clientCancel()
	protoClient, err := executionproto.NewClient(client)
	if err != nil {
		t.Fatal(err)
	}
	result, err := protoClient.Execute(clientCtx, executionproto.Request{
		Version: executionproto.Version, ExecutionID: "exec-test", Runtime: api.ExecutionRuntimeNode22,
		Source: "export default async () => ({ok:true})", Input: json.RawMessage("null"),
		TimeoutMS: 1000, MaxOutput: 1024, NetworkMode: api.ExecutionNetworkNone,
	})
	if err != nil {
		t.Fatalf("client execute: %v", err)
	}
	if string(result.Result) != `{"ok":true}` || string(result.Stdout) != "hello" {
		t.Fatalf("result = %+v", result)
	}
	if err := <-serveDone; err != nil {
		t.Fatalf("serve: %v", err)
	}
}

type oneConnListener struct {
	conn net.Conn
}

func (l oneConnListener) Accept() (net.Conn, error) { return l.conn, nil }
func (l oneConnListener) Close() error              { return nil }
func (l oneConnListener) Addr() net.Addr            { return oneConnAddr{} }

type oneConnAddr struct{}

func (oneConnAddr) Network() string { return "pipe" }
func (oneConnAddr) String() string  { return "pipe" }

func TestExecutionConstantsMatchProtocol(t *testing.T) {
	if VsockExecutionPort != executionproto.VsockPort {
		t.Fatalf("execution port = %d, protocol = %d", VsockExecutionPort, executionproto.VsockPort)
	}
	if VsockExecutionBindCID != 0xffffffff {
		t.Fatalf("bind CID = %#x", VsockExecutionBindCID)
	}
}
