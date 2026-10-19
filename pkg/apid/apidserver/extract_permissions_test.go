// adr: 050
package apidserver_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestProjectSourceStagingPreservesExecutableFiles(t *testing.T) {
	for _, tc := range []struct {
		name      string
		mode      int64
		priorMode int64
		want      os.FileMode
	}{
		{name: "executable script", mode: 0o755, want: 0o755},
		{name: "ordinary source", mode: 0o644, want: 0o644},
		{name: "privileged and writable bits removed", mode: 0o6777, want: 0o755},
		{name: "owner executable only", mode: 0o700, want: 0o744},
		{name: "replacement removes executable bits", priorMode: 0o755, mode: 0o644, want: 0o644},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(apidserver.ScanSpoolRootEnv, t.TempDir())
			t.Setenv("FAAS_SPOOL_ROOT", t.TempDir())
			const archiveName = "repo/web/entrypoint.sh"
			const body = "#!/bin/sh\necho ready\n"
			var entries []tar.Header
			if tc.priorMode != 0 {
				entries = append(entries, tar.Header{Name: archiveName, Mode: tc.priorMode})
			}
			entries = append(entries, tar.Header{Name: archiveName, Mode: tc.mode})
			raw := apidserver.BuildTestTarGz(t, entries, map[string][]byte{archiveName: []byte(body)})
			src := filepath.Join(t.TempDir(), "source.tar.gz")
			if err := os.WriteFile(src, raw, 0o600); err != nil {
				t.Fatal(err)
			}
			scanDir, prob := apidserver.ExtractTarGzToDir(src, extractTestSafeLim)
			if prob != nil {
				t.Fatalf("extract project: %+v", prob)
			}
			info, err := os.Stat(filepath.Join(scanDir, "web", "entrypoint.sh"))
			if err != nil {
				t.Fatal(err)
			}
			if info.Mode() != tc.want {
				t.Errorf("extracted mode = %v, want %v", info.Mode(), tc.want)
			}
			for _, composeContext := range []bool{false, true} {
				staged, _, err := apidserver.StageApplyTarball(t.Context(), scanDir, "acct", "project", state.App{ID: "app", RootDir: "web"}, composeContext)
				if err != nil {
					t.Fatal(err)
				}
				name := "web/entrypoint.sh"
				if composeContext {
					name = "entrypoint.sh"
				}
				header, content := stagedSourceFile(t, staged, name)
				if header.Mode != int64(tc.want) || string(content) != body {
					t.Errorf("compose=%v staged file mode=%#o, body=%q; want mode=%#o, body=%q", composeContext, header.Mode, content, tc.want, body)
				}
			}
		})
	}
}

func stagedSourceFile(t *testing.T, archive, name string) (*tar.Header, []byte) {
	t.Helper()
	raw, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	gz, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			t.Fatalf("staged archive omitted %q", name)
		}
		if err != nil {
			t.Fatal(err)
		}
		if header.Name == name {
			body, err := io.ReadAll(tr)
			if err != nil {
				t.Fatal(err)
			}
			return header, body
		}
	}
}
