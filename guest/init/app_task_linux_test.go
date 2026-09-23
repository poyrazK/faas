//go:build linux

package main

import (
	"context"
	"io"
	"net"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apptaskproto"
)

func TestDecideModeAppTaskMarkerWins(t *testing.T) {
	fsys := fstest.MapFS{
		appTaskManifestRelativePath:   &fstest.MapFile{Data: []byte(`{"kind":"app_task","version":1}`)},
		executionManifestRelativePath: &fstest.MapFile{Data: []byte(`{"kind":"execution","version":1}`)},
		"etc/faas/job.json":           &fstest.MapFile{Data: []byte(`{"kind":"job"}`)},
	}
	mode, _, err := decideMode(fsys)
	if err != nil {
		t.Fatal(err)
	}
	if mode != modeAppTask {
		t.Fatalf("mode = %v, want modeAppTask", mode)
	}
}

func TestInvalidAppTaskMarkerFailsClosed(t *testing.T) {
	for _, data := range [][]byte{
		[]byte(`{"kind":"app_task","version":2}`),
		[]byte(`{"kind":"app","version":1}`),
		[]byte(`{"kind":"app_task","version":1,"command":"bad"}`),
	} {
		if _, _, err := decideMode(fstest.MapFS{appTaskManifestRelativePath: &fstest.MapFile{Data: data}}); err == nil {
			t.Fatalf("accepted marker %s", data)
		}
	}
}

func TestExecuteAppTaskCommandUsesScopedEnvironmentAndWorkingDir(t *testing.T) {
	dir := t.TempDir()
	manifest := api.AppManifest{User: "0", WorkingDir: dir, Env: map[string]string{"LAYER": "manifest", "MANIFEST_ONLY": "yes"}}
	req := apptaskproto.Request{
		Version: apptaskproto.Version, TaskID: "task-1", CommandShell: true,
		Command:        []string{"printf '%s|%s|%s|%s' \"$PWD\" \"$LAYER\" \"$MANIFEST_ONLY\" \"$SECRET\""},
		TimeoutSeconds: 2, MaxOutputBytes: 1024,
	}
	var stdout strings.Builder
	result, err := executeAppTaskCommand(context.Background(), req, manifest,
		map[string]string{"SECRET": "hidden"}, map[string]string{"LAYER": "api"}, &stdout, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != apptaskproto.StatusSucceeded {
		t.Fatalf("result = %+v", result)
	}
	if got, want := stdout.String(), dir+"|api|yes|hidden"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
}

func TestServeAppTaskOnceHandlesOneCommand(t *testing.T) {
	server, client := net.Pipe()
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- serveAppTaskOnce(ctx, oneConnListener{conn: server}, func(_ context.Context, _ apptaskproto.Request, stdout, _ *apptaskproto.OutputWriter) (apptaskproto.Result, error) {
			_, _ = stdout.Write([]byte("ok"))
			exit := 0
			return apptaskproto.Result{Status: apptaskproto.StatusSucceeded, ExitCode: &exit}, nil
		})
	}()
	protoClient, _ := apptaskproto.NewClient(client)
	result, err := protoClient.Execute(ctx, apptaskproto.Request{
		Version: apptaskproto.Version, TaskID: "task-1", Command: []string{"true"}, TimeoutSeconds: 1, MaxOutputBytes: 1024,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != apptaskproto.StatusSucceeded || string(result.Stdout) != "ok" {
		t.Fatalf("result = %+v", result)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestAppTaskConstantsMatchProtocol(t *testing.T) {
	if VsockAppTaskPort != apptaskproto.VsockPort {
		t.Fatalf("app task port = %d, protocol = %d", VsockAppTaskPort, apptaskproto.VsockPort)
	}
}
