package api

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseDockerHubMirrors(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  []string
		err   string
	}{
		{value: "", want: nil},
		{value: " mirror.gcr.io , cache.internal:5000,mirror.gcr.io", want: []string{"mirror.gcr.io", "cache.internal:5000"}},
		{value: "https://mirror.gcr.io", err: "without a scheme"},
		{value: "mirror.gcr.io/library", err: "without a scheme or path"},
		{value: "Mirror.GCR.io", err: "lowercase"},
		{value: "cache.internal:0", err: "invalid port"},
		{value: "cache.internal:70000", err: "invalid port"},
		{value: "registry-1.docker.io", err: "Docker Hub itself"},
		{value: "docker.io", err: "Docker Hub itself"},
		{value: `mirror"]`, err: "registry host"},
		{value: "a.io,b.io,c.io,d.io,e.io", err: "at most 4"},
	} {
		got, err := ParseDockerHubMirrors(tc.value)
		if tc.err != "" {
			if err == nil || !strings.Contains(err.Error(), tc.err) {
				t.Errorf("%q: err = %v, want %q", tc.value, err, tc.err)
			}
			continue
		}
		if err != nil || !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%q: got %v, %v; want %v", tc.value, got, err, tc.want)
		}
	}
	if err := ValidDockerHubMirrors([]string{"mirror.gcr.io", "bad host"}); err == nil {
		t.Error("ValidDockerHubMirrors accepted a host with a space")
	}
}
