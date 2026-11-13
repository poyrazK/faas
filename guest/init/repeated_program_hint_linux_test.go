//go:build linux

package main

import (
	"strings"
	"testing"
)

// Hunt 34 H34-35: cmd ["/http-echo", "-listen=:5678", ...] on an image whose
// entrypoint is /http-echo ran "/http-echo /http-echo -listen=:5678 ...".
func TestRepeatedProgramHint(t *testing.T) {
	for _, tc := range []struct {
		name    string
		program string
		args    []string
		want    bool
	}{
		{"entrypoint repeated", "/http-echo", []string{"/http-echo", "-listen=:5678"}, true},
		{"bare name repeated", "/usr/bin/redis-server", []string{"redis-server", "--port", "6380"}, true},
		{"arguments only", "/http-echo", []string{"-listen=:5678"}, false},
		{"another program", "/docker-entrypoint.sh", []string{"nginx", "-g", "daemon off;"}, false},
		{"no arguments", "/http-echo", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hint := repeatedProgramHint(tc.program, tc.args)
			if got := hint != ""; got != tc.want {
				t.Fatalf("repeatedProgramHint(%q, %q) = %q, want hint %v", tc.program, tc.args, hint, tc.want)
			}
			if tc.want {
				for _, arg := range tc.args[1:] {
					if strings.Contains(hint, arg) {
						t.Fatalf("hint %q repeats argument %q", hint, arg)
					}
				}
			}
		})
	}
}
