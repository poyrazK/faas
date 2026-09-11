// adr: 171
package fcvm

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/executionproto"
)

func TestDialExecutionCompletesHandshakeAndRunsOneExchange(t *testing.T) {
	base, err := os.MkdirTemp("/tmp", "gx")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(base)
	root := filepath.Join(base, "fc", "exec-1", "root")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	sock := filepath.Join(root, VsockUDSSocketName)
	listener, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	serverDone := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			serverDone <- err
			return
		}
		defer conn.Close()
		line, err := bufio.NewReader(conn).ReadString('\n')
		if err != nil {
			serverDone <- err
			return
		}
		if line != "CONNECT 1030\n" {
			serverDone <- &unexpectedHandshake{got: line}
			return
		}
		if _, err := conn.Write([]byte("OK 5000\n")); err != nil {
			serverDone <- err
			return
		}
		serverDone <- executionproto.Serve(ctx, conn, func(_ context.Context, req executionproto.Request, stdout, _ *executionproto.OutputWriter) (executionproto.Result, error) {
			if req.ExecutionID != "exec-1" {
				return executionproto.Result{}, &unexpectedHandshake{got: req.ExecutionID}
			}
			_, _ = stdout.Write([]byte("ok\n"))
			return executionproto.Result{Status: api.ExecutionStatusSucceeded, Result: json.RawMessage(`{"ok":true}`)}, nil
		})
	}()

	v := &JailerVMM{chrootBase: base, fcName: "fc"}
	session, err := v.DialExecution(ctx, Lease{Instance: "exec-1"})
	if err != nil {
		t.Fatal(err)
	}
	wireReq := executionproto.Request{
		Version:     executionproto.Version,
		ExecutionID: "exec-1",
		Runtime:     api.ExecutionRuntimeNode22,
		Source:      "export default async function main() { return true; }",
		Input:       json.RawMessage("null"),
		TimeoutMS:   1000,
		MaxOutput:   1024,
		NetworkMode: api.ExecutionNetworkNone,
	}
	result, err := session.Execute(ctx, wireReq)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != api.ExecutionStatusSucceeded || string(result.Stdout) != "ok\n" {
		t.Fatalf("result = %+v", result)
	}
	if err := session.Destroy(ctx); err != nil {
		t.Fatal(err)
	}
	if err := session.Destroy(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-serverDone; err != nil {
		t.Fatal(err)
	}
}

type unexpectedHandshake struct{ got string }

func (e *unexpectedHandshake) Error() string { return "unexpected handshake: " + e.got }
