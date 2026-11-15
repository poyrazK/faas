package main

import (
	"os"
	"path/filepath"
	"testing"
)

func writePackTestFile(t *testing.T, root, rel, body string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// A Compose project honours each service's own .dockerignore inside that
// service's build context, keeps the Dockerfile Docker would send, and lets
// .gregaleignore have the last word.
func TestPackRulesHonourServiceDockerignore(t *testing.T) {
	root := t.TempDir()
	writePackTestFile(t, root, "compose.yaml", `services:
  web: {build: ./src/web, ports: ["80:80"]}
  api:
    build: {context: ./src/api, dockerfile: docker/Dockerfile}
  other: {build: ./src/other}
`)
	writePackTestFile(t, root, "src/web/.dockerignore", "fixtures\n*.log\nDockerfile\n")
	writePackTestFile(t, root, "src/api/.dockerignore", "*\n!main.go\n")
	writePackTestFile(t, root, ".gregaleignore", "!src/api/testdata/\n")
	rules := loadPackRules(root, false)
	for _, tc := range []struct {
		rel     string
		dir     bool
		exclude bool
	}{
		{"src/web/fixtures", true, true},
		{"src/web/app.log", false, true},
		{"src/web/main.go", false, false},
		{"src/web/Dockerfile", false, false},    // Docker always sends it
		{"src/web/.dockerignore", false, false}, // and the ignore file
		{"src/api/docker", true, false},         // the Dockerfile's directory
		{"src/api/docker/Dockerfile", false, false},
		{"src/api/main.go", false, false},
		{"src/api/secrets", true, true},     // `*` inside the context only
		{"src/other/fixtures", true, false}, // no .dockerignore of its own
		{"fixtures", true, false},           // patterns stay inside the context
		{"src/api/testdata", true, false},   // .gregaleignore re-includes it
	} {
		if got := rules.exclude(tc.rel, tc.dir); got != tc.exclude {
			t.Errorf("exclude(%s) = %v, want %v", tc.rel, got, tc.exclude)
		}
	}
}

func TestComposeServiceDockerignoreSkipsRootAndOutsideContexts(t *testing.T) {
	files := map[string][]byte{
		"docker-compose.yml":         []byte("services:\n  a: {build: .}\n  b: {build: ../elsewhere}\n  c: {build: \"${DIR}\"}\n  d: {image: x}\n"),
		".dockerignore":              []byte("*\n"),
		"../elsewhere/.dockerignore": []byte("*\n"),
	}
	patterns := composeServiceDockerignorePatterns(func(rel string) ([]byte, bool) {
		data, ok := files[rel]
		return data, ok
	})
	if len(patterns) != 0 {
		t.Fatalf("patterns = %+v, want none", patterns)
	}
}
