package main

import (
	"reflect"
	"testing"
)

func TestStampDevDebugEnv(t *testing.T) {
	cases := []struct {
		name   string
		env    []string
		want   []string
		active bool
	}{
		{name: "not debugging", env: []string{"PATH=/bin"}, want: []string{"PATH=/bin"}},
		{name: "unsupported runtime", env: []string{"FAAS_DEV_DEBUG=python"}, want: []string{"FAAS_DEV_DEBUG=python"}},
		{name: "node without options", env: []string{"FAAS_DEV_DEBUG=node"},
			want: []string{"FAAS_DEV_DEBUG=node", "NODE_OPTIONS=--inspect=0.0.0.0:9229"}, active: true},
		{name: "node keeps existing options", env: []string{"NODE_OPTIONS=--max-old-space-size=200", "FAAS_DEV_DEBUG=node"},
			want: []string{"FAAS_DEV_DEBUG=node", "NODE_OPTIONS=--max-old-space-size=200 --inspect=0.0.0.0:9229"}, active: true},
		{name: "explicit inspect wins", env: []string{"NODE_OPTIONS=--inspect=0.0.0.0:9339", "FAAS_DEV_DEBUG=node"},
			want: []string{"NODE_OPTIONS=--inspect=0.0.0.0:9339", "FAAS_DEV_DEBUG=node"}, active: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			devDebugActive.Store(false)
			if got := StampDevDebugEnv(tc.env); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("env = %v, want %v", got, tc.want)
			}
			if devDebugActive.Load() != tc.active {
				t.Fatalf("debug active = %t, want %t", devDebugActive.Load(), tc.active)
			}
		})
	}
	devDebugActive.Store(false)
}
