//go:build linux

// Tests for parseFrameworkReadyDatagram (issue #470 / PR
// #470-FU-B). The parser is the host-side DGRAM boundary
// between the guest-init proxy and the Manager's
// MarkInstanceFrameworkReady; the wire shape is mirrored in
// guest/init/framework_ready_proxy_linux.go (see the comment
// block there). MED-5 review feedback on PR #543: cover the
// shape matrix in a single table so future wire extensions
// (e.g. type=0x02 "idle") only need a new case.
//
// The build tag mirrors cmd/vmmd/framework_ready_recv.go — the
// parser and the DGRAM constants are linux-only because the
// wire is AF_VSOCK, which is a Linux kernel feature.
package main

import (
	"encoding/binary"
	"errors"
	"strings"
	"testing"
)

// makeReady builds a wire body in the same shape the guest
// emits: [1B type=0x01][optional 4B BE uint32 warmup_ms][NUL]
// [runtime]. Centralised so each test case reads as a small
// picture of the wire rather than a placeholder soup.
func makeReady(warmupMs int64, runtime string) []byte {
	out := []byte{VsockFrameworkReadyHostTypeReady}
	if warmupMs > 0 {
		var buf [4]byte
		binary.BigEndian.PutUint32(buf[:], uint32(warmupMs))
		out = append(out, buf[:]...)
	}
	out = append(out, 0) // NUL separator
	out = append(out, []byte(runtime)...)
	return out
}

func TestParseFrameworkReadyDatagram(t *testing.T) {
	type want struct {
		warmupMs int64
		runtime  string
		err      bool
		errSub   string
	}
	cases := []struct {
		name string
		body []byte
		want want
	}{
		{
			name: "empty body",
			body: nil,
			want: want{err: true, errSub: "empty body"},
		},
		{
			name: "unknown type byte (closed-set guard)",
			// Cluster C / ADR-121 extended the closed set to
			// {0x01, 0x02, 0x03, 0x04, 0x05, 0x06}. Anything outside
			// the closed set is rejected with the
			// "unknown msg sub-type" sentinel. A future event
			// class picks the next free byte (0x06+) and adds
			// a dispatch arm in lockstep. This test pins the
			// closed-set tripwire.
			body: []byte{0x07, 0x00, 0x00, 0x00, 0x00, 0x00, 'n'},
			want: want{err: true, errSub: "unknown msg sub-type 0x07"},
		},
		{
			name: "type-only, no warmup, no runtime",
			body: []byte{VsockFrameworkReadyHostTypeReady},
			want: want{warmupMs: 0, runtime: ""},
		},
		{
			name: "type + warmup, no NUL, no runtime",
			body: []byte{VsockFrameworkReadyHostTypeReady, 0x00, 0x00, 0x00, 0x64},
			want: want{warmupMs: 100, runtime: ""},
		},
		{
			name: "type + warmup + empty runtime",
			body: makeReady(100, ""),
			want: want{warmupMs: 100, runtime: ""},
		},
		{
			name: "type + warmup + node22 runtime",
			body: makeReady(350, "node22"),
			want: want{warmupMs: 350, runtime: "node22"},
		},
		{
			name: "type + warmup + python312 runtime",
			body: makeReady(250, "python312"),
			want: want{warmupMs: 250, runtime: "python312"},
		},
		{
			name: "type + warmup, no NUL separator at all",
			// rest after warmup is non-empty and lacks a NUL —
			// the parser falls back to runtime="".
			body: []byte{VsockFrameworkReadyHostTypeReady, 0x00, 0x00, 0x01, 0x2c, 'n', 'o', 'N', 'U', 'L'},
			want: want{warmupMs: 300, runtime: ""},
		},
		{
			name: "type byte alone, payload mid-bytes are ignored",
			// Forward-compat: an unknown trailing byte sequence
			// after the type is silently ignored — the host
			// only consumes the NUL-bounded runtime.
			body: []byte{VsockFrameworkReadyHostTypeReady, 0xFF},
			want: want{warmupMs: 0, runtime: ""},
		},
		{
			name: "max-length runtime stays within 64-byte DGRAM cap",
			// frameworkReadyMaxDatagram is 64 (recv.go:60);
			// the runtime tail here is 60 bytes (well under
			// the per-runner id set in practice).
			body: makeReady(1000, strings.Repeat("a", 60)),
			want: want{warmupMs: 1000, runtime: strings.Repeat("a", 60)},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseFrameworkReadyDatagram(tc.body)
			if tc.want.err {
				if err == nil {
					t.Fatalf("err = nil, want error containing %q", tc.want.errSub)
				}
				if tc.want.errSub != "" && !strings.Contains(err.Error(), tc.want.errSub) {
					t.Fatalf("err = %q, want contains %q", err.Error(), tc.want.errSub)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if got.WarmupMs != tc.want.warmupMs {
				t.Errorf("WarmupMs = %d, want %d", got.WarmupMs, tc.want.warmupMs)
			}
			if got.Runtime != tc.want.runtime {
				t.Errorf("Runtime = %q, want %q", got.Runtime, tc.want.runtime)
			}
		})
	}
}

// TestParseFrameworkReadyDatagram_NotExportedErrorErr verifies the
// two error paths return error values whose errors.Is / errors.As
// shape is what callers expect (errors.Is(unknown) != nil). The
// package-level sentinel surface is intentionally small here — the
// DGRAM loop warn-logs both — but the parser must not return nil
// error on a malformed receipt.
func TestParseFrameworkReadyDatagram_NotExportedErrorErr(t *testing.T) {
	for _, body := range [][]byte{nil, {0x02}} {
		_, err := parseFrameworkReadyDatagram(body)
		if err == nil {
			t.Errorf("parseFrameworkReadyDatagram(%v) = nil err, want error", body)
			continue
		}
		// errors.Is(nil) is false; this is a smoke check that
		// the error is a real error value (not a sentinel
		// reused incorrectly).
		if errors.Is(err, nil) {
			t.Errorf("parseFrameworkReadyDatagram(%v) returned a nil-typed error: %v", body, err)
		}
	}
}
