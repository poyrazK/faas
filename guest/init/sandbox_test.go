package main

// adr: 20261027090013 §2, §3, §4, §9 — a sandbox session runs many commands
// in the foreground and the background within their timeouts and output caps,
// and its files and archives stay inside the workspace: a path or a link that
// leaves it is refused.

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/sandboxproto"
)

type sandboxFixture struct {
	srv       *sandboxServer
	workspace string
}

func newSandboxFixture(t *testing.T) *sandboxFixture {
	t.Helper()
	dir := t.TempDir()
	workspace := filepath.Join(dir, "workspace")
	if err := os.Mkdir(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	return &sandboxFixture{srv: newSandboxServer(workspace, dir, -1, execRunner{uid: -1}, nil), workspace: workspace}
}

// call runs one operation, sending upload when set, and returns its
// streams and result.
type callResult struct {
	stdout, stderr, data bytes.Buffer
	raw                  json.RawMessage
	err                  error
}

func (f *sandboxFixture) call(t *testing.T, req sandboxproto.Request, upload io.Reader, during func(*sandboxproto.Client)) *callResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	host, guest := net.Pipe()
	go f.srv.serve(ctx, guest)
	defer func() { _ = host.Close() }()
	c, err := sandboxproto.Start(ctx, host, req)
	if err != nil {
		t.Fatalf("start %s: %v", req.Op, err)
	}
	if upload != nil {
		go func() { _ = c.Upload(ctx, upload) }()
	}
	if during != nil {
		go during(c)
	}
	out := &callResult{}
	out.raw, out.err = c.Wait(ctx, func(frameType uint32, body []byte) error {
		switch frameType {
		case sandboxproto.FrameStdout:
			out.stdout.Write(body)
		case sandboxproto.FrameStderr:
			out.stderr.Write(body)
		case sandboxproto.FrameData:
			out.data.Write(body)
		}
		return nil
	})
	return out
}

func (r *callResult) decode(t *testing.T, v any) {
	t.Helper()
	if r.err != nil {
		t.Fatalf("operation failed: %v", r.err)
	}
	if err := json.Unmarshal(r.raw, v); err != nil {
		t.Fatalf("result %s: %v", r.raw, err)
	}
}

func (r *callResult) code() string {
	var ge *sandboxproto.GuestError
	if errors.As(r.err, &ge) {
		return ge.Code
	}
	return ""
}

func execReq(argv ...string) sandboxproto.Request {
	return sandboxproto.Request{Op: sandboxproto.OpExec, Argv: argv, TimeoutMillis: 10_000, MaxOutputBytes: 1 << 20}
}

func TestSandboxManifest(t *testing.T) {
	if err := validateSandboxManifest([]byte(`{"kind":"sandbox","version":1}`)); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{`{"kind":"agent","version":1}`, `{"kind":"sandbox","version":3}`, `{"kind":"sandbox","version":1,"x":1}`,
		`{"kind":"sandbox","version":1} {}`, `not json`} {
		if validateSandboxManifest([]byte(bad)) == nil {
			t.Errorf("%s was accepted", bad)
		}
	}
}

func TestSandboxExecForeground(t *testing.T) {
	f := newSandboxFixture(t)
	if err := os.Mkdir(filepath.Join(f.workspace, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	req := execReq("sh", "-c", `echo out; echo err >&2; echo "$FOO $GREGALE_OUTBOUND_URL"; pwd; exit 3`)
	req.Env = map[string]string{"FOO": "bar", "GREGALE_OUTBOUND_URL": "http://evil"}
	req.Workdir = "sub"
	r := f.call(t, req, nil, nil)
	var res sandboxproto.ExecResult
	r.decode(t, &res)
	want := "out\nbar " + OutboundRelayEndpoint + "\n" + filepath.Join(f.workspace, "sub") + "\n"
	if res.ExitCode != 3 || res.TimedOut || res.Killed || r.stdout.String() != want || r.stderr.String() != "err\n" {
		t.Fatalf("result %+v stdout %q stderr %q", res, r.stdout.String(), r.stderr.String())
	}

	if r := f.call(t, execReq("/no/such/command"), nil, nil); r.code() != sandboxproto.CodeStartFailed {
		t.Fatalf("a missing command: %v", r.err)
	}
}

func TestSandboxExecLimits(t *testing.T) {
	f := newSandboxFixture(t)
	capped := execReq("sh", "-c", "head -c 100000 /dev/zero; echo done >&2")
	capped.MaxOutputBytes = 1000
	r := f.call(t, capped, nil, nil)
	var res sandboxproto.ExecResult
	r.decode(t, &res)
	if res.ExitCode != 0 || !res.OutputTruncated || r.stdout.Len()+r.stderr.Len() != 1000 {
		t.Fatalf("an output cap: %+v, %d+%d bytes", res, r.stdout.Len(), r.stderr.Len())
	}

	slow := execReq("sleep", "30")
	slow.TimeoutMillis = 200
	started := time.Now()
	r = f.call(t, slow, nil, nil)
	r.decode(t, &res)
	if !res.TimedOut || res.Signal != 15 || res.ExitCode != 143 || time.Since(started) > 10*time.Second {
		t.Fatalf("a timeout: %+v after %v", res, time.Since(started))
	}

	r = f.call(t, execReq("sleep", "30"), nil, func(c *sandboxproto.Client) {
		time.Sleep(100 * time.Millisecond)
		_ = c.Send(context.Background(), sandboxproto.FrameKill, nil)
	})
	r.decode(t, &res)
	if !res.Killed || res.Signal != 9 {
		t.Fatalf("a kill: %+v", res)
	}
}

func TestSandboxExecStdin(t *testing.T) {
	f := newSandboxFixture(t)
	r := f.call(t, execReq("cat"), nil, func(c *sandboxproto.Client) {
		_ = c.Send(context.Background(), sandboxproto.FrameStdin, []byte("hello "))
		_ = c.Send(context.Background(), sandboxproto.FrameStdin, []byte("world"))
		_ = c.Send(context.Background(), sandboxproto.FrameStdinEOF, nil)
	})
	var res sandboxproto.ExecResult
	r.decode(t, &res)
	if res.ExitCode != 0 || r.stdout.String() != "hello world" {
		t.Fatalf("stdin: %+v %q", res, r.stdout.String())
	}
}

// A command that leaves a grandchild holding its output still ends.
func TestSandboxExecDetachedChild(t *testing.T) {
	f := newSandboxFixture(t)
	started := time.Now()
	r := f.call(t, execReq("sh", "-c", "sleep 30 & echo parent"), nil, nil)
	var res sandboxproto.ExecResult
	r.decode(t, &res)
	if res.ExitCode != 0 || r.stdout.String() != "parent\n" || time.Since(started) > 5*time.Second {
		t.Fatalf("a detached child: %+v %q after %v", res, r.stdout.String(), time.Since(started))
	}
}

func TestSandboxBackgroundCommands(t *testing.T) {
	f := newSandboxFixture(t)
	bg := execReq("sh", "-c", "echo started; echo oops >&2; sleep 30")
	bg.Background = true
	var started sandboxproto.ExecResult
	f.call(t, bg, nil, nil).decode(t, &started)
	if started.Handle == "" {
		t.Fatalf("no handle: %+v", started)
	}
	var out sandboxproto.ExecOutput
	deadline := time.Now().Add(5 * time.Second)
	for out.StdoutOffset == 0 || out.StderrOffset == 0 {
		if time.Now().After(deadline) {
			t.Fatalf("no output: %+v", out)
		}
		time.Sleep(20 * time.Millisecond)
		f.call(t, sandboxproto.Request{Op: sandboxproto.OpExecOutput, Handle: started.Handle}, nil, nil).decode(t, &out)
	}
	if string(out.Stdout) != "started\n" || string(out.Stderr) != "oops\n" || !out.Running || out.StdoutOffset != 8 {
		t.Fatalf("output %+v", out)
	}
	var list sandboxproto.ExecList
	f.call(t, sandboxproto.Request{Op: sandboxproto.OpExecList}, nil, nil).decode(t, &list)
	if len(list.Commands) != 1 || list.Commands[0].Handle != started.Handle || !list.Commands[0].Running {
		t.Fatalf("list %+v", list)
	}
	f.call(t, sandboxproto.Request{Op: sandboxproto.OpExecKill, Handle: started.Handle}, nil, nil).decode(t, &sandboxproto.Command{})
	deadline = time.Now().Add(5 * time.Second)
	for out.Running {
		if time.Now().After(deadline) {
			t.Fatal("the killed command still runs")
		}
		time.Sleep(20 * time.Millisecond)
		f.call(t, sandboxproto.Request{Op: sandboxproto.OpExecOutput, Handle: started.Handle, StdoutOffset: out.StdoutOffset,
			StderrOffset: out.StderrOffset}, nil, nil).decode(t, &out)
	}
	if out.Signal != 9 || len(out.Stdout) != 0 {
		t.Fatalf("after the kill %+v", out)
	}
	if r := f.call(t, sandboxproto.Request{Op: sandboxproto.OpExecOutput, Handle: "c0000000000000000"}, nil, nil); r.code() != sandboxproto.CodeNotFound {
		t.Fatalf("an unknown handle: %v", r.err)
	}
}

// A background command keeps its most recent output; older output is
// reported as skipped.
func TestOutputRing(t *testing.T) {
	r := newOutputRing(10)
	_, _ = r.Write([]byte("0123456789abcdef"))
	data, next, skipped := r.read(0, 100)
	if string(data) != "6789abcdef" || next != 16 || !skipped {
		t.Fatalf("read from 0: %q %d %v", data, next, skipped)
	}
	data, next, skipped = r.read(12, 2)
	if string(data) != "cd" || next != 14 || skipped {
		t.Fatalf("read from 12: %q %d %v", data, next, skipped)
	}
	if data, next, _ := r.read(99, 10); len(data) != 0 || next != 16 {
		t.Fatalf("read past the end: %q %d", data, next)
	}
}

func TestSandboxFiles(t *testing.T) {
	f := newSandboxFixture(t)
	write := func(path, content string, parents bool) *callResult {
		return f.call(t, sandboxproto.Request{Op: sandboxproto.OpFileWrite, Path: path, Parents: parents}, strings.NewReader(content), nil)
	}
	var written sandboxproto.TransferResult
	write(filepath.Join(f.workspace, "notes", "today.txt"), "hello", true).decode(t, &written)
	if written.Bytes != 5 {
		t.Fatalf("write %+v", written)
	}
	if r := write("missing/dir/a.txt", "x", false); r.code() != sandboxproto.CodeNotFound {
		t.Fatalf("a write without parents: %v", r.err)
	}
	r := f.call(t, sandboxproto.Request{Op: sandboxproto.OpFileRead, Path: "notes/today.txt"}, nil, nil)
	var read sandboxproto.TransferResult
	r.decode(t, &read)
	if r.data.String() != "hello" || read.Bytes != 5 {
		t.Fatalf("read %q %+v", r.data.String(), read)
	}
	r = f.call(t, sandboxproto.Request{Op: sandboxproto.OpFileRead, Path: "notes/today.txt", MaxBytes: 2}, nil, nil)
	r.decode(t, &read)
	if r.data.String() != "he" || !read.Truncated {
		t.Fatalf("a bounded read %q %+v", r.data.String(), read)
	}

	var info sandboxproto.FileInfo
	f.call(t, sandboxproto.Request{Op: sandboxproto.OpFileStat, Path: "notes"}, nil, nil).decode(t, &info)
	if info.Type != sandboxproto.TypeDir || info.Path != "notes" {
		t.Fatalf("stat %+v", info)
	}
	f.call(t, sandboxproto.Request{Op: sandboxproto.OpFileMkdir, Path: "a/b/c", Parents: true}, nil, nil).decode(t, &struct{}{})
	if r := f.call(t, sandboxproto.Request{Op: sandboxproto.OpFileMkdir, Path: "a"}, nil, nil); r.code() != sandboxproto.CodeExists {
		t.Fatalf("mkdir of an existing directory: %v", r.err)
	}
	var list sandboxproto.FileList
	f.call(t, sandboxproto.Request{Op: sandboxproto.OpFileList, Path: "."}, nil, nil).decode(t, &list)
	if names := entryPaths(list); names != "a notes" {
		t.Fatalf("list depth 1: %s", names)
	}
	f.call(t, sandboxproto.Request{Op: sandboxproto.OpFileList, Path: "a", Depth: 5}, nil, nil).decode(t, &list)
	if names := entryPaths(list); names != "a/b a/b/c" {
		t.Fatalf("list a deep: %s", names)
	}
	f.call(t, sandboxproto.Request{Op: sandboxproto.OpFileRename, Path: "notes/today.txt", To: "a/today.txt"}, nil, nil).decode(t, &struct{}{})
	if r := f.call(t, sandboxproto.Request{Op: sandboxproto.OpFileRemove, Path: "a"}, nil, nil); r.code() != sandboxproto.CodeNotEmpty {
		t.Fatalf("removing a full directory: %v", r.err)
	}
	f.call(t, sandboxproto.Request{Op: sandboxproto.OpFileRemove, Path: "a", Recursive: true}, nil, nil).decode(t, &struct{}{})
	if r := f.call(t, sandboxproto.Request{Op: sandboxproto.OpFileStat, Path: "a"}, nil, nil); r.code() != sandboxproto.CodeNotFound {
		t.Fatalf("a removed directory: %v", r.err)
	}
	for name, tc := range map[string]struct {
		req  sandboxproto.Request
		code string
	}{
		"reading a directory":    {sandboxproto.Request{Op: sandboxproto.OpFileRead, Path: "notes"}, sandboxproto.CodeIsADirectory},
		"removing the root":      {sandboxproto.Request{Op: sandboxproto.OpFileRemove, Path: f.workspace}, sandboxproto.CodeInvalidRequest},
		"listing a file":         {sandboxproto.Request{Op: sandboxproto.OpFileList, Path: "notes"}, ""},
		"renaming onto the root": {sandboxproto.Request{Op: sandboxproto.OpFileRename, Path: "notes", To: "."}, sandboxproto.CodeOutsideWorkspace},
	} {
		if name == "listing a file" {
			write("notes/x.txt", "x", false)
			tc.req.Path, tc.code = "notes/x.txt", sandboxproto.CodeNotADirectory
		}
		if r := f.call(t, tc.req, nil, nil); r.code() != tc.code {
			t.Errorf("%s: %v, want %s", name, r.err, tc.code)
		}
	}
}

func entryPaths(list sandboxproto.FileList) string {
	names := make([]string, 0, len(list.Entries))
	for _, e := range list.Entries {
		names = append(names, e.Path)
	}
	return strings.Join(names, " ")
}

// Paths the host names, and links inside the workspace, never reach a file
// outside it.
func TestSandboxFilesStayInTheWorkspace(t *testing.T) {
	f := newSandboxFixture(t)
	outside := filepath.Join(filepath.Dir(f.workspace), "secret.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Dir(f.workspace), filepath.Join(f.workspace, "up")); err != nil {
		t.Fatal(err)
	}
	for name, req := range map[string]sandboxproto.Request{
		"an absolute path":          {Op: sandboxproto.OpFileRead, Path: "/etc/passwd"},
		"a climbing path":           {Op: sandboxproto.OpFileRead, Path: "../secret.txt"},
		"a link out":                {Op: sandboxproto.OpFileRead, Path: "up/secret.txt"},
		"a write through a link":    {Op: sandboxproto.OpFileMkdir, Path: "up/new"},
		"a rename out":              {Op: sandboxproto.OpFileRename, Path: "up", To: "../moved"},
		"an archive of the parent":  {Op: sandboxproto.OpArchiveCreate, Path: "up", MaxBytes: 1 << 20},
		"an archive outside":        {Op: sandboxproto.OpArchiveCreate, Path: "/", MaxBytes: 1 << 20},
		"an extraction through out": {Op: sandboxproto.OpArchiveExtract, Path: "up", MaxBytes: 1 << 20, MaxEntries: 10},
	} {
		var upload io.Reader
		if req.Op == sandboxproto.OpArchiveExtract {
			upload = bytes.NewReader(tarGz(t, map[string]string{"a.txt": "x"}))
		}
		r := f.call(t, req, upload, nil)
		if code := r.code(); code != sandboxproto.CodeOutsideWorkspace && code != sandboxproto.CodeNotADirectory {
			t.Errorf("%s: %v, want outside_workspace", name, r.err)
		}
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(f.workspace), "new")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a write through a link reached outside the workspace")
	}
	// The link itself is a workspace entry: stat reports it without
	// following it.
	var info sandboxproto.FileInfo
	f.call(t, sandboxproto.Request{Op: sandboxproto.OpFileStat, Path: "up"}, nil, nil).decode(t, &info)
	if info.Type != sandboxproto.TypeSymlink || info.LinkTarget != filepath.Dir(f.workspace) {
		t.Fatalf("stat of a link %+v", info)
	}
}

func tarGz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, content := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(content)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		_, _ = tw.Write([]byte(content))
	}
	if err := errors.Join(tw.Close(), gz.Close()); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestSandboxArchiveRoundTrip(t *testing.T) {
	f := newSandboxFixture(t)
	src := filepath.Join(f.workspace, "project")
	if err := os.MkdirAll(filepath.Join(src, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "src", "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "run.sh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("src/main.go", filepath.Join(src, "entry")); err != nil {
		t.Fatal(err)
	}
	r := f.call(t, sandboxproto.Request{Op: sandboxproto.OpArchiveCreate, Path: "project", MaxBytes: 1 << 20}, nil, nil)
	var created sandboxproto.ArchiveResult
	r.decode(t, &created)
	if created.Entries != 4 || created.Bytes != int64(r.data.Len()) || len(created.SHA256) != 64 {
		t.Fatalf("archive %+v of %d bytes", created, r.data.Len())
	}
	var extracted sandboxproto.ArchiveResult
	f.call(t, sandboxproto.Request{Op: sandboxproto.OpArchiveExtract, Path: "restored", MaxBytes: 1 << 20, MaxEntries: 10},
		bytes.NewReader(r.data.Bytes()), nil).decode(t, &extracted)
	if extracted.Entries != 4 {
		t.Fatalf("extract %+v", extracted)
	}
	got, err := os.ReadFile(filepath.Join(f.workspace, "restored", "src", "main.go"))
	if err != nil || string(got) != "package main\n" {
		t.Fatalf("restored file %q %v", got, err)
	}
	if info, err := os.Stat(filepath.Join(f.workspace, "restored", "run.sh")); err != nil || info.Mode().Perm()&0o100 == 0 {
		t.Fatalf("restored mode %v %v", info, err)
	}
	if link, err := os.Readlink(filepath.Join(f.workspace, "restored", "entry")); err != nil || link != "src/main.go" {
		t.Fatalf("restored link %q %v", link, err)
	}

	for name, tc := range map[string]struct {
		req    sandboxproto.Request
		upload []byte
		code   string
	}{
		"an archive over its cap": {sandboxproto.Request{Op: sandboxproto.OpArchiveCreate, Path: "project", MaxBytes: 10}, nil, sandboxproto.CodeTooLarge},
		"too many entries": {sandboxproto.Request{Op: sandboxproto.OpArchiveExtract, Path: "x", MaxBytes: 1 << 20, MaxEntries: 1},
			tarGz(t, map[string]string{"a": "1", "b": "2"}), sandboxproto.CodeTooLarge},
		"too many bytes": {sandboxproto.Request{Op: sandboxproto.OpArchiveExtract, Path: "x", MaxBytes: 3, MaxEntries: 10},
			tarGz(t, map[string]string{"a": "1234"}), sandboxproto.CodeTooLarge},
		"an entry that climbs out": {sandboxproto.Request{Op: sandboxproto.OpArchiveExtract, Path: "x", MaxBytes: 1 << 20, MaxEntries: 10},
			tarGz(t, map[string]string{"../evil": "x"}), sandboxproto.CodeOutsideWorkspace},
		"not an archive": {sandboxproto.Request{Op: sandboxproto.OpArchiveExtract, Path: "x", MaxBytes: 1 << 20, MaxEntries: 10},
			[]byte("plain text"), sandboxproto.CodeInvalidRequest},
	} {
		var upload io.Reader
		if tc.upload != nil {
			upload = bytes.NewReader(tc.upload)
		}
		if r := f.call(t, tc.req, upload, nil); r.code() != tc.code {
			t.Errorf("%s: %v, want %s", name, r.err, tc.code)
		}
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(f.workspace), "evil")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("an archive entry reached outside the workspace")
	}
}
