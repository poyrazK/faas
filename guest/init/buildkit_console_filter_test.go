package main

import (
	"bytes"
	"testing"
)

// prod 2026-11-19: a failed npm install streamed buildkitd debug entries and
// moby/buildkit stack traces into the customer's deploy output.
func TestBuildkitConsoleFilterDropsDebugAndStackFrames(t *testing.T) {
	in := "#6 importing cache manifest from local:3630261811984277240\n" +
		"github.com/moby/buildkit/solver/llbsolver/ops.(*ExecOp).Exec\n" +
		"\tgithub.com/moby/buildkit/solver/llbsolver/ops/exec.go:555\n" +
		"github.com/moby/buildkit/solver.(*sharedOp).Exec.func2\n" +
		"\tgithub.com/moby/buildkit/solver/jobs.go:1248\n" +
		"time=\"2026-11-19T19:17:19Z\" level=debug msg=\"session finished: <nil>\" spanID=c7ab\n" +
		"  debug continuation\n" +
		"time=\"2026-11-19T19:17:19Z\" level=warning msg=\"failed check for fsverity support\" error=\"enable fsverity failed\" path=/build/cache\n" +
		"time=\"2026-11-19T19:17:19Z\" level=warning msg=\"cache export slow\"\n" +
		"npm error code E404\n" +
		"ERROR: process \"npm install\" did not complete successfully: exit code: 1\n"
	want := "#6 importing cache manifest from local:3630261811984277240\n" +
		"time=\"2026-11-19T19:17:19Z\" level=warning msg=\"cache export slow\"\n" +
		"npm error code E404\n" +
		"ERROR: process \"npm install\" did not complete successfully: exit code: 1\n"
	var out bytes.Buffer
	f := newBuildkitConsoleFilter(&out)
	// Split mid-line to exercise buffering.
	for _, chunk := range []string{in[:37], in[37:200], in[200:]} {
		if n, err := f.Write([]byte(chunk)); err != nil || n != len(chunk) {
			t.Fatalf("write = %d, %v", n, err)
		}
	}
	if out.String() != want {
		t.Fatalf("filtered output:\n%s\nwant:\n%s", out.String(), want)
	}
}

func TestBuildkitConsoleFilterKeepsOrdinaryLines(t *testing.T) {
	for _, line := range []string{
		"github.com/acme/app is a module path printed by a build step",
		"#12 [stage-0 3/4] RUN go build ./...",
		"\tsome indented line without a go file",
		"main.go:10:2: undefined: foo",
	} {
		if isGoStackFrameLine(line) {
			t.Errorf("%q treated as a stack frame", line)
		}
	}
}
