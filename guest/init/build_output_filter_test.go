package main

import (
	"bytes"
	"testing"
)

// prod 2026-11-20 (R10-6): every Node build printed npm's production-config
// deprecation, caused by Railpack's own NPM_CONFIG_PRODUCTION, not the app.
func TestBuildOutputFilterDropsPlatformNpmNoise(t *testing.T) {
	var out bytes.Buffer
	f := newBuildOutputFilter(&out)
	in := "#12 [install] RUN npm ci\n" +
		// Prod hunt r28: the build command's own fs-verity probe warning.
		`time="2026-11-22T19:12:28Z" level=warning msg="failed check for fsverity support" error="enable fsverity failed: inappropriate ioctl for device" path=/build/cache` + "\n" +
		"#12 0.534 npm warn config production Use `--omit=dev` instead.\n" +
		"npm warn config production Use `--omit=dev` instead.\r\n" +
		"#12 3.100 npm warn deprecated inflight@1.0.6: leaks memory\n" +
		"#12 DONE 3.2s\n" +
		"tail without newline"
	// Split mid-line to exercise buffering.
	_, _ = f.Write([]byte(in[:40]))
	_, _ = f.Write([]byte(in[40:]))
	f.Flush()
	want := "#12 [install] RUN npm ci\n" +
		"#12 3.100 npm warn deprecated inflight@1.0.6: leaks memory\n" +
		"#12 DONE 3.2s\n" +
		"tail without newline"
	if out.String() != want {
		t.Fatalf("console =\n%q\nwant\n%q", out.String(), want)
	}
	// Prod hunt r29: the log tail builderd shows as "[guest build output]"
	// is cut from the whole capture; it drops the same lines.
	if got := string(withoutPlatformBuildNoise([]byte(in))); got != want {
		t.Fatalf("log tail =\n%q\nwant\n%q", got, want)
	}
}
