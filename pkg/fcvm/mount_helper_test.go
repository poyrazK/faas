package fcvm

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveMountHelperReleaseCompatibility(t *testing.T) {
	for _, tc := range []struct {
		name, kind string
		wantHelper bool
		wantError  bool
	}{
		{name: "older bundle", kind: "missing"},
		{name: "bundled helper", kind: "executable", wantHelper: true},
		{name: "broken helper permissions", kind: "nonexecutable", wantError: true},
		{name: "directory is not a helper", kind: "directory", wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			executable := filepath.Join(root, "vmmd")
			helper := filepath.Join(root, "vmmd-jail-helper")
			switch tc.kind {
			case "executable", "nonexecutable":
				mode := os.FileMode(0o600)
				if tc.kind == "executable" {
					mode = 0o700
				}
				if err := os.WriteFile(helper, []byte("release helper"), mode); err != nil {
					t.Fatal(err)
				}
			case "directory":
				if err := os.Mkdir(helper, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			got, err := resolveMountHelper(executable)
			if (err != nil) != tc.wantError {
				t.Fatalf("resolve: %v, want error=%v", err, tc.wantError)
			}
			if tc.wantError {
				return
			}
			want := executable
			if tc.wantHelper {
				want = helper
			}
			if got != want {
				t.Fatalf("helper = %q, want this release's %q", got, want)
			}
		})
	}
}
