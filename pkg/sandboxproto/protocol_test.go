// adr: 20261027090013 §4 — a sandbox operation is one validated request
// answered by streams and exactly one result or error; paths the host sends
// resolve inside the workspace or are refused.
package sandboxproto_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/sandboxproto"
)

func exec(argv ...string) sandboxproto.Request {
	return sandboxproto.Request{Op: sandboxproto.OpExec, Argv: argv, TimeoutMillis: 1000, MaxOutputBytes: 1 << 20}
}

func TestRequestValidate(t *testing.T) {
	ok := []sandboxproto.Request{
		exec("ls", "-la"),
		{Op: sandboxproto.OpExecList},
		{Op: sandboxproto.OpExecOutput, Handle: "c0123456789abcdef"},
		{Op: sandboxproto.OpExecKill, Handle: "c0123456789abcdef", Signal: "TERM"},
		{Op: sandboxproto.OpFileRead, Path: "/workspace/a.txt"},
		{Op: sandboxproto.OpFileWrite, Path: "a.txt", Mode: 0o600},
		{Op: sandboxproto.OpFileList, Path: ".", Depth: 3},
		{Op: sandboxproto.OpFileRename, Path: "a", To: "b"},
		{Op: sandboxproto.OpArchiveCreate, MaxBytes: 1 << 20},
		{Op: sandboxproto.OpArchiveExtract, Path: "restore", MaxBytes: 1 << 20, MaxEntries: 100},
	}
	for _, r := range ok {
		if err := r.Validate(); err != nil {
			t.Errorf("%s: %v", r.Op, err)
		}
	}
	tty := exec("sh")
	tty.TTY, tty.Background = true, true
	rows := exec("sh")
	rows.Rows = 24
	badEnv := exec("sh")
	badEnv.Env = map[string]string{"1BAD": "x"}
	root := exec("sh")
	root.RunAs = "admin"
	noTimeout := exec("sh")
	noTimeout.TimeoutMillis = 0
	for name, r := range map[string]sandboxproto.Request{
		"an unknown op":           {Op: "shell"},
		"no argv":                 exec(),
		"an empty command":        exec(""),
		"NUL in argv":             exec("sh", "a\x00b"),
		"a background terminal":   tty,
		"rows without a terminal": rows,
		"a bad env name":          badEnv,
		"an unknown user":         root,
		"no timeout":              noTimeout,
		"a bad handle":            {Op: sandboxproto.OpExecOutput, Handle: "cmd-1"},
		"a bad signal":            {Op: sandboxproto.OpExecKill, Handle: "c0123456789abcdef", Signal: "HUP"},
		"a mode with type bits":   {Op: sandboxproto.OpFileWrite, Path: "a", Mode: 0o40755},
		"too deep a list":         {Op: sandboxproto.OpFileList, Depth: sandboxproto.MaxListDepth + 1},
		"an archive with no cap":  {Op: sandboxproto.OpArchiveCreate},
		"an extraction no limit":  {Op: sandboxproto.OpArchiveExtract, MaxBytes: 10},
		"NUL in a path":           {Op: sandboxproto.OpFileStat, Path: "a\x00"},
	} {
		if err := r.Validate(); !errors.Is(err, sandboxproto.ErrInvalidRequest) {
			t.Errorf("%s: %v, want ErrInvalidRequest", name, err)
		}
	}
}

func TestWorkspacePath(t *testing.T) {
	for in, want := range map[string]string{
		"": ".", ".": ".", "/workspace": ".", "/workspace/": ".", "a/b": "a/b", "./a//b/": "a/b",
		"/workspace/a/../b": "b", "a/../b": "b",
	} {
		if got, ok := sandboxproto.WorkspacePath("/workspace", in); !ok || got != want {
			t.Errorf("%q = %q %v, want %q", in, got, ok, want)
		}
	}
	for _, in := range []string{"/etc/passwd", "/workspacex/a", "..", "../a", "a/../../b", "/workspace/../etc"} {
		if got, ok := sandboxproto.WorkspacePath("/workspace", in); ok {
			t.Errorf("%q = %q, want outside", in, got)
		}
	}
}

// serve runs handler on the guest end of a pipe and returns the host end.
func serve(t *testing.T, handler sandboxproto.Handler) net.Conn {
	t.Helper()
	host, guest := net.Pipe()
	go func() { _ = sandboxproto.Serve(context.Background(), guest, handler) }()
	t.Cleanup(func() { _ = host.Close() })
	return host
}

func TestCallStreamsAndResult(t *testing.T) {
	host := serve(t, func(_ context.Context, req sandboxproto.Request, call *sandboxproto.Call) (any, error) {
		_, _ = call.Stdout().Write(bytes.Repeat([]byte("o"), sandboxproto.ChunkBytes+10))
		_, _ = call.Stderr().Write([]byte("e"))
		return sandboxproto.ExecResult{ExitCode: len(req.Argv)}, nil
	})
	var stdout, stderr int
	var result sandboxproto.ExecResult
	err := sandboxproto.Do(context.Background(), host, exec("a", "b"), func(frameType uint32, body []byte) error {
		if frameType == sandboxproto.FrameStdout {
			stdout += len(body)
		} else {
			stderr += len(body)
		}
		return nil
	}, &result)
	if err != nil || stdout != sandboxproto.ChunkBytes+10 || stderr != 1 || result.ExitCode != 2 {
		t.Fatalf("do = %v, stdout %d stderr %d result %+v", err, stdout, stderr, result)
	}
}

func TestCallErrors(t *testing.T) {
	host := serve(t, func(context.Context, sandboxproto.Request, *sandboxproto.Call) (any, error) {
		return nil, &sandboxproto.GuestError{Code: sandboxproto.CodeNotFound, Message: "a.txt does not exist"}
	})
	var ge *sandboxproto.GuestError
	if err := sandboxproto.Do(context.Background(), host, sandboxproto.Request{Op: sandboxproto.OpFileStat, Path: "a.txt"}, nil, nil); !errors.As(err, &ge) ||
		ge.Code != sandboxproto.CodeNotFound || ge.Message != "a.txt does not exist" {
		t.Fatalf("a guest refusal: %v", err)
	}
	internal := serve(t, func(context.Context, sandboxproto.Request, *sandboxproto.Call) (any, error) {
		return nil, errors.New("disk exploded at /secret/path")
	})
	if err := sandboxproto.Do(context.Background(), internal, sandboxproto.Request{Op: sandboxproto.OpExecList}, nil, nil); !errors.As(err, &ge) ||
		ge.Code != sandboxproto.CodeInternal || strings.Contains(ge.Message, "secret") {
		t.Fatalf("an internal failure leaks or is not internal: %v", err)
	}

	// A request the guest cannot validate never reaches the handler.
	reached := false
	strict := serve(t, func(context.Context, sandboxproto.Request, *sandboxproto.Call) (any, error) {
		reached = true
		return struct{}{}, nil
	})
	c := sandboxproto.NewConn(strict)
	_ = c.WriteJSON(context.Background(), sandboxproto.FrameRequest, map[string]any{"op": "exec", "argv": []string{"sh"}})
	_, body, err := c.Read(context.Background())
	var e sandboxproto.ErrorFrame
	if err != nil || json.Unmarshal(body, &e) != nil || e.Code != sandboxproto.CodeInvalidRequest || reached {
		t.Fatalf("an invalid request: %v %s, reached %v", err, body, reached)
	}
}

func TestUploadIsBounded(t *testing.T) {
	var got bytes.Buffer
	handler := func(_ context.Context, req sandboxproto.Request, call *sandboxproto.Call) (any, error) {
		n, err := call.ReadUpload(&got, req.MaxBytes)
		return sandboxproto.TransferResult{Bytes: n}, err
	}
	for _, tc := range []struct {
		size  int
		limit int64
		code  string
	}{{3*sandboxproto.ChunkBytes + 7, 1 << 20, ""}, {200, 100, sandboxproto.CodeTooLarge}} {
		got.Reset()
		host := serve(t, handler)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		c, err := sandboxproto.Start(ctx, host, sandboxproto.Request{Op: sandboxproto.OpFileWrite, Path: "a", MaxBytes: tc.limit})
		if err != nil {
			t.Fatal(err)
		}
		go func() { _ = c.Upload(ctx, bytes.NewReader(bytes.Repeat([]byte("x"), tc.size))) }()
		raw, err := c.Wait(ctx, nil)
		cancel()
		var ge *sandboxproto.GuestError
		switch {
		case tc.code == "" && (err != nil || got.Len() != tc.size || !strings.Contains(string(raw), `"bytes":`)):
			t.Errorf("an upload of %d: %v %s, got %d", tc.size, err, raw, got.Len())
		case tc.code != "" && (!errors.As(err, &ge) || ge.Code != tc.code):
			t.Errorf("an upload of %d over %d: %v", tc.size, tc.limit, err)
		}
	}
}

func TestFrameLimits(t *testing.T) {
	host, guest := net.Pipe()
	defer func() { _ = host.Close() }()
	go func() {
		// A header that claims more than any frame may hold.
		_, _ = guest.Write([]byte{0, 0, 0, 2, 0xff, 0xff, 0xff, 0xff})
		_ = guest.Close()
	}()
	if _, _, err := sandboxproto.NewConn(host).Read(context.Background()); !errors.Is(err, sandboxproto.ErrFrameTooLarge) {
		t.Fatalf("an oversized frame: %v", err)
	}
	if err := sandboxproto.NewConn(guest).Write(context.Background(), sandboxproto.FrameData, make([]byte, 5<<20)); !errors.Is(err, sandboxproto.ErrFrameTooLarge) {
		t.Fatalf("writing an oversized frame: %v", err)
	}
}
