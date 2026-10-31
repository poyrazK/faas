package api

import (
	"strings"
	"testing"
)

func TestComposeResources(t *testing.T) {
	two, zero := 2, 0
	for _, tc := range []struct {
		name string
		r    *ComposeResources
		zero bool
		want string
	}{
		{"nil", nil, true, ""},
		{"empty", &ComposeResources{}, true, ""},
		{"valid", &ComposeResources{Replicas: &two, MemoryMB: 512, CPUMillicores: 500}, false, ""},
		{"zero replicas", &ComposeResources{Replicas: &zero}, false, "at least 1"},
		{"negative memory", &ComposeResources{MemoryMB: -1}, false, "must be positive"},
		{"uneven CPU share", &ComposeResources{CPUMillicores: 300}, false, "one of 250, 500, 1000"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.r.IsZero(); got != tc.zero {
				t.Fatalf("IsZero = %t, want %t", got, tc.zero)
			}
			err := tc.r.Validate()
			if tc.want == "" {
				if err != nil {
					t.Fatalf("err = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
	original := &ComposeResources{Replicas: &two, MemoryMB: 256}
	clone := original.Clone()
	*clone.Replicas = 5
	if *original.Replicas != 2 || clone.MemoryMB != 256 {
		t.Fatalf("clone shares replicas: original %d, clone %+v", *original.Replicas, clone)
	}
}
