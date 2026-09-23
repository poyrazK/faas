// adr: 222 — task VM sessions use a private one-shot command exchange.
package fcvm

import (
	"bufio"
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/apptaskproto"
)

func TestDialAppTaskCompletesHandshakeAndRunsOneExchange(t *testing.T) {
	base, err := os.MkdirTemp("/tmp", "gat")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(base)
	root := filepath.Join(base, "fc", "task-vm-1", "root")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", filepath.Join(root, VsockUDSSocketName))
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			done <- err
			return
		}
		defer conn.Close()
		line, err := bufio.NewReader(conn).ReadString('\n')
		if err != nil {
			done <- err
			return
		}
		if line != "CONNECT 1031\n" {
			done <- &unexpectedHandshake{got: line}
			return
		}
		if _, err := conn.Write([]byte("OK 5000\n")); err != nil {
			done <- err
			return
		}
		done <- apptaskproto.Serve(ctx, conn, func(_ context.Context, req apptaskproto.Request, stdout, _ *apptaskproto.OutputWriter) (apptaskproto.Result, error) {
			if req.TaskID != "task-1" {
				return apptaskproto.Result{}, &unexpectedHandshake{got: req.TaskID}
			}
			_, _ = stdout.Write([]byte("ok\n"))
			exit := 0
			return apptaskproto.Result{Status: apptaskproto.StatusSucceeded, ExitCode: &exit}, nil
		})
	}()

	v := &JailerVMM{chrootBase: base, fcName: "fc"}
	session, err := v.DialAppTask(ctx, Lease{Instance: "task-vm-1"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := session.Execute(ctx, appTaskProtocolRequest("task-1"))
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != apptaskproto.StatusSucceeded || string(result.Stdout) != "ok\n" {
		t.Fatalf("result = %+v", result)
	}
	if err := session.Destroy(ctx); err != nil {
		t.Fatal(err)
	}
	if err := session.Destroy(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func appTaskProtocolRequest(taskID string) apptaskproto.Request {
	return apptaskproto.Request{
		Version: apptaskproto.Version, TaskID: taskID, Command: []string{"true"},
		TimeoutSeconds: 1, MaxOutputBytes: 1024,
	}
}
