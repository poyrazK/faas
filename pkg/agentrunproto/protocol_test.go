package agentrunproto_test

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/agentrunproto"
)

func request() agentrunproto.Request {
	return agentrunproto.Request{
		Version: agentrunproto.Version, RunID: "run-1", Command: []string{"codex", "exec", "triage"},
		Env:        map[string]string{"OPENAI_BASE_URL": "http://127.0.0.1:8787/v1"},
		Files:      []agentrunproto.File{{Path: "prompt.txt", Content: []byte("triage")}, {Path: "steps/plan/out.json", Content: []byte("{}")}},
		TimeoutSec: 60, MaxLogBytes: 1 << 20, MaxResultBytes: 64 << 10, MaxOutputFiles: 4, MaxOutputBytes: 1 << 20,
	}
}

// exchange runs one request through Run and Serve over an in-memory pipe.
func exchange(t *testing.T, req agentrunproto.Request, handler agentrunproto.Handler, cancel <-chan struct{}) (agentrunproto.Result, []string, error, error) {
	t.Helper()
	host, guest := net.Pipe()
	ctx, stop := context.WithTimeout(context.Background(), 10*time.Second)
	defer stop()
	serveErr := make(chan error, 1)
	go func() {
		defer guest.Close()
		serveErr <- agentrunproto.Serve(ctx, guest, handler)
	}()
	var streamed []string
	result, err := agentrunproto.Run(ctx, host, req, func(_ context.Context, stream string, chunk []byte) error {
		streamed = append(streamed, stream+":"+string(chunk))
		return nil
	}, cancel)
	host.Close()
	return result, streamed, err, <-serveErr
}

func TestRoundTrip(t *testing.T) {
	result, streamed, err, serveErr := exchange(t, request(), func(_ context.Context, req agentrunproto.Request, stdout, stderr io.Writer, _ <-chan struct{}) (agentrunproto.Result, error) {
		if req.RunID != "run-1" || string(req.Files[1].Content) != "{}" || req.Env["OPENAI_BASE_URL"] == "" {
			t.Errorf("request lost fields: %+v", req)
		}
		fmt.Fprint(stdout, "thinking")
		fmt.Fprint(stderr, "warn")
		return agentrunproto.Result{Status: agentrunproto.StatusSucceeded, Result: json.RawMessage(`{"verdict":"ok"}`),
			Files: []agentrunproto.OutputFile{agentrunproto.NewOutputFile("report.md", []byte("# ok"))}}, nil
	}, nil)
	if err != nil || serveErr != nil {
		t.Fatalf("run %v, serve %v", err, serveErr)
	}
	if result.Status != agentrunproto.StatusSucceeded || string(result.Result) != `{"verdict":"ok"}` || string(result.Files[0].Content) != "# ok" {
		t.Fatalf("result %+v", result)
	}
	if string(result.Stdout) != "thinking" || string(result.Stderr) != "warn" || strings.Join(streamed, ",") != "stdout:thinking,stderr:warn" {
		t.Fatalf("streams %q %q %v", result.Stdout, result.Stderr, streamed)
	}
}

func TestLogsPastTheBudgetAreDropped(t *testing.T) {
	req := request()
	req.MaxLogBytes = 10
	result, _, err, serveErr := exchange(t, req, func(_ context.Context, _ agentrunproto.Request, stdout, _ io.Writer, _ <-chan struct{}) (agentrunproto.Result, error) {
		if n, err := fmt.Fprint(stdout, strings.Repeat("x", 25)); n != 25 || err != nil {
			t.Errorf("a full budget must not fail the harness's write: %d %v", n, err)
		}
		return agentrunproto.Result{Status: agentrunproto.StatusFailed, ExitCode: 1}, nil
	}, nil)
	if err != nil || serveErr != nil {
		t.Fatalf("run %v, serve %v", err, serveErr)
	}
	if len(result.Stdout) != 10 || !result.LogTruncated || result.Status != agentrunproto.StatusFailed {
		t.Fatalf("result %+v, stdout %d bytes", result, len(result.Stdout))
	}
}

func TestCancelReachesTheGuest(t *testing.T) {
	cancel := make(chan struct{})
	close(cancel)
	result, _, err, serveErr := exchange(t, request(), func(_ context.Context, _ agentrunproto.Request, _, _ io.Writer, cancelled <-chan struct{}) (agentrunproto.Result, error) {
		select {
		case <-cancelled:
			return agentrunproto.Result{Status: agentrunproto.StatusCancelled, ExitCode: 143}, nil
		case <-time.After(5 * time.Second):
			return agentrunproto.Result{}, errors.New("cancel never arrived")
		}
	}, cancel)
	if err != nil || serveErr != nil || result.Status != agentrunproto.StatusCancelled {
		t.Fatalf("result %+v, run %v, serve %v", result, err, serveErr)
	}
}

func TestGuestRefusesBadResults(t *testing.T) {
	good := agentrunproto.NewOutputFile("a.txt", []byte("a"))
	bad := good
	bad.SHA256 = "sha256:" + strings.Repeat("0", 64)
	for name, result := range map[string]agentrunproto.Result{
		"outputs on failure": {Status: agentrunproto.StatusFailed, Files: []agentrunproto.OutputFile{good}},
		"result not object":  {Status: agentrunproto.StatusSucceeded, Result: json.RawMessage(`[1]`)},
		"wrong digest":       {Status: agentrunproto.StatusSucceeded, Files: []agentrunproto.OutputFile{bad}},
		"unknown status":     {Status: "paused"},
		"escaping name":      {Status: agentrunproto.StatusSucceeded, Files: []agentrunproto.OutputFile{agentrunproto.NewOutputFile("../x", nil)}},
		"too many files": {Status: agentrunproto.StatusSucceeded, Files: []agentrunproto.OutputFile{
			agentrunproto.NewOutputFile("1", nil), agentrunproto.NewOutputFile("2", nil), agentrunproto.NewOutputFile("3", nil),
			agentrunproto.NewOutputFile("4", nil), agentrunproto.NewOutputFile("5", nil)}},
	} {
		_, _, err, serveErr := exchange(t, request(), func(context.Context, agentrunproto.Request, io.Writer, io.Writer, <-chan struct{}) (agentrunproto.Result, error) {
			return result, nil
		}, nil)
		if !errors.Is(err, agentrunproto.ErrGuestFailed) || serveErr == nil {
			t.Errorf("%s: run %v, serve %v", name, err, serveErr)
		}
	}
}

func TestRequestValidation(t *testing.T) {
	for name, mutate := range map[string]func(*agentrunproto.Request){
		"version":    func(r *agentrunproto.Request) { r.Version = 9 },
		"no run id":  func(r *agentrunproto.Request) { r.RunID = " " },
		"no command": func(r *agentrunproto.Request) { r.Command = nil },
		"nul in arg": func(r *agentrunproto.Request) { r.Command = []string{"a\x00b"} },
		"huge arg": func(r *agentrunproto.Request) {
			r.Command = []string{"x", strings.Repeat("p", agentrunproto.MaxCommandArgBytes+1)}
		},
		"huge command": func(r *agentrunproto.Request) {
			r.Command = []string{"x", strings.Repeat("p", agentrunproto.MaxCommandArgBytes), strings.Repeat("p", agentrunproto.MaxCommandArgBytes)}
		},
		"long timeout":   func(r *agentrunproto.Request) { r.TimeoutSec = 3601 },
		"bad env":        func(r *agentrunproto.Request) { r.Env = map[string]string{"1X": "y"} },
		"absolute input": func(r *agentrunproto.Request) { r.Files = []agentrunproto.File{{Path: "/etc/passwd"}} },
		"escaping input": func(r *agentrunproto.Request) { r.Files = []agentrunproto.File{{Path: "../x"}} },
		"unclean input":  func(r *agentrunproto.Request) { r.Files = []agentrunproto.File{{Path: "a//b"}} },
		"duplicate":      func(r *agentrunproto.Request) { r.Files = append(r.Files, r.Files[0]) },
		"file as dir": func(r *agentrunproto.Request) {
			r.Files = []agentrunproto.File{{Path: "steps"}, {Path: "steps/a"}}
		},
		"big log budget": func(r *agentrunproto.Request) { r.MaxLogBytes = 1 << 30 },
	} {
		req := request()
		mutate(&req)
		if err := req.Validate(); !errors.Is(err, agentrunproto.ErrInvalidRequest) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if err := request().Validate(); err != nil {
		t.Fatal(err)
	}
}

// A guest that is sent garbage answers with an error frame, and the host
// never reads a frame larger than its cap.
func TestFrameErrors(t *testing.T) {
	host, guest := net.Pipe()
	defer host.Close()
	done := make(chan error, 1)
	go func() {
		defer guest.Close()
		done <- agentrunproto.Serve(context.Background(), guest, func(context.Context, agentrunproto.Request, io.Writer, io.Writer, <-chan struct{}) (agentrunproto.Result, error) {
			t.Error("handler ran for a bad request")
			return agentrunproto.Result{}, nil
		})
	}()
	var hdr [8]byte
	binary.BigEndian.PutUint32(hdr[0:4], agentrunproto.FrameRequest)
	binary.BigEndian.PutUint32(hdr[4:8], 2)
	if _, err := host.Write(append(hdr[:], []byte("{}")...)); err != nil {
		t.Fatal(err)
	}
	reply := make([]byte, 8)
	if _, err := io.ReadFull(host, reply); err != nil || binary.BigEndian.Uint32(reply[0:4]) != agentrunproto.FrameError {
		t.Fatalf("reply %v %v", reply, err)
	}
	_, _ = io.ReadAll(host)
	if err := <-done; !errors.Is(err, agentrunproto.ErrInvalidRequest) {
		t.Fatalf("serve %v", err)
	}

	host2, guest2 := net.Pipe()
	defer host2.Close()
	go func() {
		defer guest2.Close()
		// Read the whole request frame, then answer with an oversized header.
		reqHdr := make([]byte, 8)
		if _, err := io.ReadFull(guest2, reqHdr); err != nil {
			return
		}
		if _, err := io.CopyN(io.Discard, guest2, int64(binary.BigEndian.Uint32(reqHdr[4:8]))); err != nil {
			return
		}
		var big [8]byte
		binary.BigEndian.PutUint32(big[0:4], agentrunproto.FrameStdout)
		binary.BigEndian.PutUint32(big[4:8], 1<<30)
		_, _ = guest2.Write(big[:])
		_, _ = io.Copy(io.Discard, guest2)
	}()
	ctx, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	if _, err := agentrunproto.Run(ctx, host2, request(), nil, nil); !errors.Is(err, agentrunproto.ErrFrameTooLarge) {
		t.Fatalf("oversized frame: %v", err)
	}
}

func TestValidRelativePath(t *testing.T) {
	for p, ok := range map[string]bool{
		"prompt.txt": true, "steps/plan/out.json": true, "a b.txt": true,
		"": false, "/abs": false, "..": false, "../x": false, "a/../b": false, "a/": false, ".": false,
		"a\\b": false, "tab\there": false, strings.Repeat("d/", 8) + "f": false,
	} {
		if err := agentrunproto.ValidRelativePath(p); (err == nil) != ok {
			t.Errorf("%q: %v", p, err)
		}
	}
}
