// adr: 050
package apidserver_test

import (
	"archive/tar"
	"os"
	"path/filepath"
	"testing"

	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

func TestExtractTarGzPreservesRepositoryRoots(t *testing.T) {
	for _, tc := range []struct {
		name    string
		entries []string
		want    []string
	}{
		{"nested before root file", []string{"web/package.json", "package.json"}, []string{"web/package.json", "package.json"}},
		{"multiple workload directories", []string{"web/index.js", "api/index.js"}, []string{"web/index.js", "api/index.js"}},
		{"shared wrapper", []string{"repo/web/index.js", "repo/api/index.js"}, []string{"web/index.js", "api/index.js"}},
		{"repeated dot wrapper", []string{"././repo/web/index.js", "././repo/api/index.js"}, []string{"web/index.js", "api/index.js"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spool := t.TempDir()
			t.Setenv(apidserver.ScanSpoolRootEnv, spool)
			var entries []tar.Header
			bodies := make(map[string][]byte)
			for _, name := range tc.entries {
				entries = append(entries, tar.Header{Name: name})
				bodies[name] = []byte("contents of " + name)
			}
			source := filepath.Join(t.TempDir(), "source.tar.gz")
			if err := os.WriteFile(source, apidserver.BuildTestTarGz(t, entries, bodies), 0o600); err != nil {
				t.Fatal(err)
			}
			dir, problem := apidserver.ExtractTarGzToDir(source, extractTestSafeLim)
			if problem != nil {
				t.Fatalf("extract repository: %+v", problem)
			}
			for index, name := range tc.want {
				data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(name)))
				if err != nil || string(data) != string(bodies[tc.entries[index]]) {
					t.Errorf("repository file %s = %q, %v; want %q", name, data, err, bodies[tc.entries[index]])
				}
			}
			info, err := os.Stat(dir)
			if err != nil {
				t.Fatal(err)
			}
			if info.Mode().Perm() != 0o700 {
				t.Errorf("extracted repository root mode = %v, want private 0700", info.Mode())
			}
			remaining, err := os.ReadDir(spool)
			if err != nil || len(remaining) != 1 || remaining[0].Name() != filepath.Base(dir) {
				t.Errorf("extraction left unexpected scratch directories: %v, %v", remaining, err)
			}
		})
	}
}

func TestExtractTarGzCleansRejectedRepository(t *testing.T) {
	spool := t.TempDir()
	t.Setenv(apidserver.ScanSpoolRootEnv, spool)
	source := filepath.Join(t.TempDir(), "source.tar.gz")
	data := apidserver.BuildTestTarGz(t,
		[]tar.Header{{Name: "repo/good.txt"}, {Name: "../outside"}},
		map[string][]byte{"repo/good.txt": []byte("valid"), "../outside": []byte("invalid")})
	if err := os.WriteFile(source, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if dir, problem := apidserver.ExtractTarGzToDir(source, extractTestSafeLim); problem == nil || dir != "" {
		t.Fatalf("extract invalid source = %q, %+v; want rejection", dir, problem)
	}
	if remaining, err := os.ReadDir(spool); err != nil || len(remaining) != 0 {
		t.Errorf("rejected extraction left scratch directories: %v, %v", remaining, err)
	}
}
