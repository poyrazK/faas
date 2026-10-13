// handlers_debug_telemetry_echo_test.go — round-trip tests for
// echoDebugSince. The previous bug was that handlers echoed
// sinceDur.String() which Go formats as "120h0m0s" — parseable
// in Go, but customer automation that consumes the response and
// feeds it back into a follow-up request breaks because tooling
// expects the short "Nd" / "Nh" canonical form. These tests
// lock in:
//  1. raw parseable input is echoed verbatim (round-trip safe)
//  2. empty raw falls through to canonical form of effective
//  3. clamped effective duration renders the canonical Nd / Nh
//     form so the customer can detect the discrepancy
//  4. unparseable raw falls through to canonical form of effective
//
// Part of PR-B's /code-review review-fix cluster.
package apidserver_test

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

func TestEchoDebugSince(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		eff  time.Duration
		want string
	}{
		{
			name: "raw Nd verbatim",
			raw:  "5d",
			eff:  5 * 24 * time.Hour,
			want: "5d",
		},
		{
			name: "raw Go duration verbatim",
			raw:  "3h",
			eff:  3 * time.Hour,
			want: "3h",
		},
		{
			name: "raw minutes verbatim",
			raw:  "90m",
			eff:  90 * time.Minute,
			want: "90m",
		},
		{
			name: "empty raw falls through to canonical Nd",
			raw:  "",
			eff:  24 * time.Hour,
			want: "1d",
		},
		{
			name: "empty raw falls through to canonical Nh",
			raw:  "",
			eff:  3 * time.Hour,
			want: "3h0m0s",
		},
		{
			name: "clamped raw 30d on 7d plan renders 7d",
			raw:  "30d",
			eff:  7 * 24 * time.Hour,
			want: "7d",
		},
		{
			name: "unparseable raw falls through to canonical",
			raw:  "garbage",
			eff:  2 * time.Hour,
			want: "2h0m0s",
		},
		{
			name: "negative raw collapses to default canonical",
			raw:  "-3h",
			eff:  24 * time.Hour,
			want: "1d",
		},
		{
			name: "raw zero collapses to default canonical",
			raw:  "0h",
			eff:  24 * time.Hour,
			want: "1d",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := apidserver.EchoDebugSince(tc.raw, tc.eff)
			if got != tc.want {
				t.Fatalf("echoDebugSince(%q, %s) = %q, want %q", tc.raw, tc.eff, got, tc.want)
			}
		})
	}
}

// TestEchoDebugSinceRoundTrip pins the property that the echo
// value, when fed back into parseDebugSinceFromString, yields
// the same effective duration (modulo clamping). This is the
// wire-contract guarantee the customer automation depends on.
func TestEchoDebugSinceRoundTrip(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		eff  time.Duration
	}{
		{name: "Nd verbatim", raw: "5d", eff: 5 * 24 * time.Hour},
		{name: "Go duration verbatim", raw: "3h", eff: 3 * time.Hour},
		{name: "minutes verbatim", raw: "90m", eff: 90 * time.Minute},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			echo := apidserver.EchoDebugSince(tc.raw, tc.eff)
			got := apidserver.ParseDebugSinceFromString(echo, -1)
			if got != tc.eff {
				t.Fatalf("round-trip: echo %q parsed back to %s, want %s", echo, got, tc.eff)
			}
		})
	}
}
