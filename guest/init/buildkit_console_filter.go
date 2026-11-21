// Customer-facing filter for buildkitd's console output. Build-tag free so the
// unit tests run on every platform; the linux builder wires it in
// main_linux.go.

package main

import (
	"bytes"
	"io"
	"strings"
)

// buildkitConsoleFilter forwards buildkitd's log lines to the build console
// the customer reads, minus debug entries and Go stack frames. buildkitd runs
// with --debug so the start-failure tail keeps full detail; that tail is
// written separately and is not filtered. The console streamed lines such as
// `level=debug msg="session finished"` and moby/buildkit stack traces into
// every deploy (prod 2026-11-19).
type buildkitConsoleFilter struct {
	out     io.Writer
	partial []byte
	inDebug bool
}

func newBuildkitConsoleFilter(out io.Writer) *buildkitConsoleFilter {
	return &buildkitConsoleFilter{out: out}
}

// Write never fails because of the filter: it reports len(p) so a slow or
// broken console cannot stall buildkitd.
func (f *buildkitConsoleFilter) Write(p []byte) (int, error) {
	f.partial = append(f.partial, p...)
	for {
		i := bytes.IndexByte(f.partial, '\n')
		if i < 0 {
			break
		}
		line := f.partial[:i+1]
		if f.keep(string(line[:i])) {
			_, _ = f.out.Write(line)
		}
		f.partial = f.partial[i+1:]
	}
	// A line longer than 64 KiB without a newline is passed through as is.
	if len(f.partial) > 64<<10 {
		_, _ = f.out.Write(f.partial)
		f.partial = f.partial[:0]
	}
	return len(p), nil
}

func (f *buildkitConsoleFilter) keep(line string) bool {
	if strings.HasPrefix(line, "time=") {
		f.inDebug = strings.Contains(line, " level=debug ")
		// The builder's cache disk cannot do fs-verity; buildkitd warns
		// about it on every build, three times (prod 2026-11-20).
		return !f.inDebug && !strings.Contains(line, `msg="failed check for fsverity support"`)
	}
	if f.inDebug {
		// Continuation lines of a debug entry.
		return false
	}
	return !isGoStackFrameLine(line)
}

// isGoStackFrameLine matches the two lines of a Go stack frame:
// "github.com/moby/buildkit/solver.(*sharedOp).Exec.func2" and
// "\tgithub.com/moby/buildkit/solver/jobs.go:1248".
func isGoStackFrameLine(line string) bool {
	if strings.HasPrefix(line, "\t") {
		trimmed := strings.TrimSpace(line)
		colon := strings.LastIndex(trimmed, ".go:")
		return colon > 0 && !strings.ContainsRune(trimmed[:colon], ' ')
	}
	if strings.ContainsRune(line, ' ') {
		return false
	}
	for _, prefix := range []string{"github.com/", "golang.org/", "google.golang.org/", "go.opentelemetry.io/", "runtime.", "main."} {
		if strings.HasPrefix(line, prefix) && (strings.Contains(line, ".(") || strings.HasSuffix(line, ")") || strings.Contains(line, ".func")) {
			return true
		}
	}
	return false
}
