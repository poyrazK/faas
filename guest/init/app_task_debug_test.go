package main

import (
	"reflect"
	"testing"
)

// The debug helper's argv carries the deployment identity and the command
// unchanged, including arguments that look like the separator
// (ADR-20261117070620).
func TestAppTaskDebugHelperArgsRoundTrip(t *testing.T) {
	cases := []struct {
		id      appTaskDebugIdentity
		command []string
	}{
		{appTaskDebugIdentity{UID: 1000, GID: 1000}, []string{"sh"}},
		{appTaskDebugIdentity{UID: 65532, GID: 65532, Groups: []int{10, 20}}, []string{"ls", "--", "-la"}},
		{appTaskDebugIdentity{}, []string{"wget", "-qO-", "http://api:8080/healthz"}},
	}
	for _, c := range cases {
		args := encodeAppTaskDebugHelperArgs(c.id, c.command)
		if args[0] != appTaskDebugHelperArg0 {
			t.Fatalf("arg0 = %q", args[0])
		}
		id, command, err := decodeAppTaskDebugHelperArgs(args[1:])
		if err != nil {
			t.Fatalf("decode %v: %v", args, err)
		}
		if id.UID != c.id.UID || id.GID != c.id.GID || len(id.Groups) != len(c.id.Groups) || !reflect.DeepEqual(command, c.command) {
			t.Fatalf("round trip %+v %v -> %+v %v", c.id, c.command, id, command)
		}
	}
	for _, bad := range [][]string{
		nil,
		{"1000", "1000", "", "--"},
		{"1000", "1000", "", "x", "sh"},
		{"root", "1000", "", "--", "sh"},
		{"1000", "1000", "a", "--", "sh"},
		{"1000", "1000", "", "--", " "},
	} {
		if _, _, err := decodeAppTaskDebugHelperArgs(bad); err == nil {
			t.Errorf("decode %v accepted", bad)
		}
	}
}

// The tools come after the image's own PATH, so an image's tools win.
func TestAppendAppTaskDebugPath(t *testing.T) {
	got := appendAppTaskDebugPath([]string{"HOME=/home/app", "PATH=/usr/bin:/bin"})
	want := []string{"HOME=/home/app", "PATH=/usr/bin:/bin:/.gregale/debug/bin"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	if got := appendAppTaskDebugPath([]string{"PATH="}); got[0] != "PATH=/.gregale/debug/bin" {
		t.Fatalf("empty PATH: %v", got)
	}
	if got := appendAppTaskDebugPath(nil); got[0] != "PATH="+defaultImagePath+":/.gregale/debug/bin" {
		t.Fatalf("no PATH: %v", got)
	}
}
