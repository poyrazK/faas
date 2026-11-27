package main

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// next dev crash-looped in a watch-mode developer environment on prod
// (2026-11-21): EACCES mkdir /app/.next, because the image owns /app as root
// and the server runs as the app user. Watch mode hands the working directory
// and node_modules (the directories only) to the app user, never follows a
// link, and leaves directories outside /app alone.
func TestPrepareDevWatchWorkdir(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, app string)
		dir   func(app string) string
		want  []string
	}{
		{name: "working directory and node_modules",
			setup: func(t *testing.T, app string) {
				mkdir(t, filepath.Join(app, "node_modules", "next"))
				mkdir(t, filepath.Join(app, "src"))
			},
			dir:  func(app string) string { return app },
			want: []string{".", "node_modules"}},
		{name: "no node_modules",
			dir:  func(app string) string { return app },
			want: []string{"."}},
		{name: "node_modules link is not followed",
			setup: func(t *testing.T, app string) {
				target := t.TempDir()
				if err := os.Symlink(target, filepath.Join(app, "node_modules")); err != nil {
					t.Fatal(err)
				}
			},
			dir:  func(app string) string { return app },
			want: []string{"."}},
		{name: "subdirectory of the app",
			setup: func(t *testing.T, app string) { mkdir(t, filepath.Join(app, "web", "node_modules")) },
			dir:   func(app string) string { return filepath.Join(app, "web") },
			want:  []string{"web", "web/node_modules"}},
		{name: "outside the app is left alone",
			dir:  func(string) string { return "/" },
			want: nil},
		{name: "sibling with the app root as prefix is left alone",
			dir:  func(app string) string { return app + "-other" },
			want: nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			app := filepath.Join(t.TempDir(), "app")
			mkdir(t, app)
			if tc.setup != nil {
				tc.setup(t, app)
			}
			var got []string
			record := func(path string, uid, gid int) error {
				if uid != 1000 || gid != 1001 {
					t.Fatalf("lchown %s to %d:%d", path, uid, gid)
				}
				rel, err := filepath.Rel(app, path)
				if err != nil {
					t.Fatal(err)
				}
				got = append(got, filepath.ToSlash(rel))
				return nil
			}
			if err := prepareDevWatchWorkdirIn(app, tc.dir(app), 1000, 1001, record); err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(got, tc.want) {
				t.Fatalf("handed over %v, want %v", got, tc.want)
			}
		})
	}
}

func mkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
}
