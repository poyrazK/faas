package builderd

import "testing"

func TestBuildWorkdir(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		root string
		want string
	}{
		{name: "legacy empty root", root: "", want: "/build/src"},
		{name: "archive root", root: ".", want: "/build/src"},
		{name: "workspace member", root: "apps/api", want: "/build/src/apps/api"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := buildWorkdir(tc.root)
			if err != nil {
				t.Fatalf("buildWorkdir(%q): %v", tc.root, err)
			}
			if got != tc.want {
				t.Fatalf("buildWorkdir(%q) = %q, want %q", tc.root, got, tc.want)
			}
		})
	}
	if _, err := buildWorkdir("../escape"); err == nil {
		t.Fatal("buildWorkdir accepted an escaping source root")
	}
}
