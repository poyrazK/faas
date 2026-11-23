// Customer-facing filter for the build command's output. Build-tag free so
// the unit tests run on every platform; the linux builder wires it in
// main_linux.go.

package main

import (
	"bytes"
	"io"
	"strings"
)

// buildOutputFilter forwards the build command's output to the build console
// minus lines the platform's own build tooling causes and the customer cannot
// act on. Railpack's Node provider sets NPM_CONFIG_PRODUCTION=false, so every
// npm step printed "npm warn config production Use `--omit=dev` instead."
// (prod 2026-11-20). The build log tail builderd shows is cut from the same
// filtered text (withoutPlatformBuildNoise, prod hunt r29).
type buildOutputFilter struct {
	out     io.Writer
	partial []byte
}

func newBuildOutputFilter(out io.Writer) *buildOutputFilter {
	return &buildOutputFilter{out: out}
}

// Write never fails because of the filter: it reports len(p) so a slow or
// broken console cannot stall the build.
func (f *buildOutputFilter) Write(p []byte) (int, error) {
	f.partial = append(f.partial, p...)
	for {
		i := bytes.IndexByte(f.partial, '\n')
		if i < 0 {
			break
		}
		line := f.partial[:i+1]
		if !platformBuildNoise(string(line[:i])) {
			_, _ = f.out.Write(line)
		}
		f.partial = f.partial[i+1:]
	}
	// Progress output without newlines (spinners, \r updates) passes
	// through once it grows past 64 KiB.
	if len(f.partial) > 64<<10 {
		_, _ = f.out.Write(f.partial)
		f.partial = f.partial[:0]
	}
	return len(p), nil
}

// Flush writes a final line that has no trailing newline.
func (f *buildOutputFilter) Flush() {
	if len(f.partial) > 0 && !platformBuildNoise(string(f.partial)) {
		_, _ = f.out.Write(f.partial)
	}
	f.partial = f.partial[:0]
}

// withoutPlatformBuildNoise drops platformBuildNoise lines from a complete
// build-command capture. builderd shows the build report's log tail to the
// customer as "[guest build output]" on every deploy, and that tail was cut
// from the unfiltered capture, so the noise the console filter drops still
// reached the deploy log (prod hunt r29, rc.373).
func withoutPlatformBuildNoise(raw []byte) []byte {
	var out bytes.Buffer
	f := newBuildOutputFilter(&out)
	_, _ = f.Write(raw)
	f.Flush()
	return out.Bytes()
}

// platformBuildNoise matches npm's deprecation of the production config,
// with or without buildctl's "#12 0.534 " step prefix, and the fs-verity
// probe warning. The builder's cache disk cannot do fs-verity; the build
// command's own content store warned about it on every deploy
// (`level=warning msg="failed check for fsverity support" … path=/build/cache`,
// prod hunt r28), separately from buildkitd's copy buildkitConsoleFilter drops.
func platformBuildNoise(line string) bool {
	line = strings.TrimRight(line, "\r")
	return strings.HasSuffix(line, "npm warn config production Use `--omit=dev` instead.") ||
		strings.Contains(line, `msg="failed check for fsverity support"`)
}
