package sourcecontext

import (
	"strings"
	"testing"
)

func TestNormalize(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   string
		want string
	}{
		{name: "empty is archive root", in: "", want: DefaultRoot},
		{name: "dot is archive root", in: ".", want: DefaultRoot},
		{name: "trims form whitespace", in: " apps/api ", want: "apps/api"},
		{name: "nested member", in: "services/api_2", want: "services/api_2"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Normalize(tc.in)
			if err != nil {
				t.Fatalf("Normalize(%q): %v", tc.in, err)
			}
			if got != tc.want {
				t.Fatalf("Normalize(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestNormalizeRejectsAmbiguousOrUnsafePaths(t *testing.T) {
	t.Parallel()
	cases := []string{
		"/apps/api",
		"../api",
		"apps/../api",
		"apps/./api",
		"apps//api",
		`apps\\api`,
		"apps\x00api",
		strings.Repeat("a", MaxRootBytes+1),
	}
	for _, raw := range cases {
		raw := raw
		t.Run(raw, func(t *testing.T) {
			if got, err := Normalize(raw); err == nil {
				t.Fatalf("Normalize(%q) = %q, want error", raw, got)
			}
		})
	}
}

func TestStorageAndEffectiveRoot(t *testing.T) {
	t.Parallel()
	stored, err := StorageRoot(".")
	if err != nil {
		t.Fatalf("StorageRoot: %v", err)
	}
	if stored != "" {
		t.Fatalf("StorageRoot(.) = %q, want empty", stored)
	}
	if got, err := EffectiveRoot(stored); err != nil || got != DefaultRoot {
		t.Fatalf("EffectiveRoot(%q) = %q, %v; want %q", stored, got, err, DefaultRoot)
	}
	stored, err = StorageRoot("apps/api")
	if err != nil {
		t.Fatalf("StorageRoot(apps/api): %v", err)
	}
	if stored != "apps/api" {
		t.Fatalf("StorageRoot(apps/api) = %q, want apps/api", stored)
	}
	if got, err := EffectiveRoot(stored); err != nil || got != stored {
		t.Fatalf("EffectiveRoot(%q) = %q, %v; want %q", stored, got, err, stored)
	}
}
