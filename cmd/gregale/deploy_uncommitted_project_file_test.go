package main_test

import (
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

// A HEAD deploy without its untracked compose.yaml planned zero workloads and
// said only "scan produced zero workloads" (microservices-demo, 2026-11-16).
func TestUncommittedProjectFile(t *testing.T) {
	for _, tc := range []struct {
		name, porcelain, source string
		file                    string
		untracked               bool
	}{
		{"untracked compose at root", " D README.md\n?? compose.yaml\n", "/repo", "compose.yaml", true},
		{"modified gregale.yaml", " M gregale.yaml\n", "/repo", "gregale.yaml", false},
		{"compose in deployed subdir", "?? services/docker-compose.yml\n", "/repo/services", "services/docker-compose.yml", true},
		{"compose elsewhere", "?? other/compose.yaml\n", "/repo/services", "", false},
		{"unrelated changes", " M src/main.go\n?? notes.txt\n", "/repo", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			file, untracked := main.UncommittedProjectFile(tc.porcelain, "/repo", tc.source)
			if file != tc.file || untracked != tc.untracked {
				t.Fatalf("got (%q, %v), want (%q, %v)", file, untracked, tc.file, tc.untracked)
			}
		})
	}
}
