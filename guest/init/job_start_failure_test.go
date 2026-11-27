package main

import (
	"errors"
	"io/fs"
	"os/exec"
	"syscall"
	"testing"
)

// A missing or non-executable entrypoint is the customer's error, not an
// infrastructure fault (prod hunt r62: `--command "echo a; echo b"` ran the
// whole line as a program name and dead-lettered as infra with exit 126).
func TestJobStartFailureClassifiesCustomerErrors(t *testing.T) {
	cases := []struct {
		name  string
		err   error
		code  int32
		class string
	}{
		{"not in PATH", &exec.Error{Name: "echo a; echo b", Err: exec.ErrNotFound}, 127, "failed"},
		{"absolute path missing", &fs.PathError{Op: "fork/exec", Path: "/bin/nope", Err: syscall.ENOENT}, 127, "failed"},
		{"not executable", &fs.PathError{Op: "fork/exec", Path: "/app/run.sh", Err: syscall.EACCES}, 126, "failed"},
		{"bad format", &fs.PathError{Op: "fork/exec", Path: "/app/run", Err: syscall.ENOEXEC}, 126, "failed"},
		{"directory", &fs.PathError{Op: "fork/exec", Path: "/app", Err: syscall.EISDIR}, 126, "failed"},
		{"fork out of memory", &fs.PathError{Op: "fork/exec", Path: "/bin/sh", Err: syscall.ENOMEM}, 126, "infra"},
		{"other", errors.New("pipe broke"), 126, "infra"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, class := jobStartFailure(tc.err)
			if code != tc.code || class != tc.class {
				t.Fatalf("jobStartFailure(%v) = %d %q, want %d %q", tc.err, code, class, tc.code, tc.class)
			}
		})
	}
}

func TestJobStartHintFlagsAShellLine(t *testing.T) {
	if jobStartHint([]string{"echo task=$I; sleep 2"}) == "" {
		t.Fatal("a one-argument shell line got no hint")
	}
	for _, argv := range [][]string{{"/app/run"}, {"/bin/sh", "-c", "echo a; echo b"}} {
		if hint := jobStartHint(argv); hint != "" {
			t.Fatalf("jobStartHint(%q) = %q, want none", argv, hint)
		}
	}
}
