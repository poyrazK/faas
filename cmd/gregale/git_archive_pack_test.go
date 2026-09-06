package main

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestPackGitArchive_RedactsAndExcludes(t *testing.T) {
	secret := "sk" + "_live_" + "XXXXXXXXXXXXXXXXXXXXXXXXXXXX"
	inPath := writeGitArchiveFixture(t, map[string]string{
		".env":                    "STRIPE_SECRET_KEY=" + secret + "\n",
		".gregaleignore":          "ignored.txt\n",
		"config.txt":              "STRIPE_SECRET_KEY=" + secret + "\n",
		"ignored.txt":             "STRIPE_SECRET_KEY=" + secret + "\n",
		"node_modules/hidden.txt": "STRIPE_SECRET_KEY=" + secret + "\n",
		"tail.txt":                "tail survives\n",
	})

	outPath, count, findings, err := packGitArchive(inPath, defaultZeroConfigSourceCapMB, modeSourceTree, nil)
	if err != nil {
		t.Fatalf("packGitArchive: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(outPath) })
	if count != 4 {
		t.Fatalf("regular file count = %d, want 4 (ignored file and node_modules subtree omitted)", count)
	}
	if len(findings) != 2 {
		t.Fatalf("findings = %d, want 2: %+v", len(findings), findings)
	}

	entries := readGitArchiveFixture(t, outPath)
	for _, omitted := range []string{"ignored.txt", "node_modules/hidden.txt"} {
		if _, ok := entries[omitted]; ok {
			t.Errorf("filtered archive contains omitted entry %q", omitted)
		}
	}
	for name, body := range entries {
		if strings.Contains(string(body), secret) {
			t.Errorf("filtered archive contains raw secret in %q", name)
		}
	}
	if got := string(entries["tail.txt"]); got != "tail survives\n" {
		t.Errorf("tail.txt = %q, want original bytes", got)
	}
	if got := string(entries[".env"]); !strings.Contains(got, "<REDACTED") {
		t.Errorf(".env was not redacted: %q", got)
	}
	if got := string(entries["config.txt"]); !strings.Contains(got, "REDACTED secret detected") {
		t.Errorf("config.txt was not redacted: %q", got)
	}
}

func TestGitArchiveGoFunctionDetectionAndBuildModule(t *testing.T) {
	inPath := writeGitArchiveFixture(t, map[string]string{
		"README.md":                  "docs\n",
		"services/worker/handler.go": "package main\n",
	})

	sh, runtime, handler, err := detectGitArchiveShape(inPath, "services/worker")
	if err != nil {
		t.Fatal(err)
	}
	if sh != shapeFunction || runtime != runtimeGo124 || handler != defaultTemplateHandler {
		t.Fatalf("detected = (%v, %q, %q), want Go function", sh, runtime, handler)
	}
	buildOnly := map[string][]byte{"services/worker/go.mod": []byte(functionGoBuildModule)}
	outPath, count, _, err := packGitArchive(inPath, defaultZeroConfigSourceCapMB, modeOff, buildOnly)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(outPath) })
	entries := readGitArchiveFixture(t, outPath)
	if got := string(entries["services/worker/go.mod"]); got != functionGoBuildModule {
		t.Fatalf("generated go.mod = %q, want %q", got, functionGoBuildModule)
	}
	if count != 3 {
		t.Fatalf("file count = %d, want 3 including build-only go.mod", count)
	}
}

func TestGitArchiveGoBuildModuleForDetectedAndExplicitFunctions(t *testing.T) {
	withoutModule := writeGitArchiveFixture(t, map[string]string{
		"services/worker/handler.go": "package main\n",
	})
	withModule := writeGitArchiveFixture(t, map[string]string{
		"services/worker/handler.go": "package main\n",
		"services/worker/go.mod":     "module customer\n",
	})
	for _, tc := range []struct {
		name    string
		archive string
		shape   shape
		runtime string
		want    bool
	}{
		{"detected Go", withoutModule, shapeFunction, runtimeGo124, true},
		{"explicit Go Alpine", withoutModule, shapeFunction, "go124-alpine", true},
		{"committed module", withModule, shapeFunction, runtimeGo124, false},
		{"app", withoutModule, shapeApp, runtimeGo124, false},
		{"Node function", withoutModule, shapeFunction, runtimeNode22, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			files, err := gitArchiveGoBuildOnlyFiles(tc.archive, "services/worker", tc.shape, tc.runtime)
			if err != nil {
				t.Fatal(err)
			}
			_, ok := files["services/worker/go.mod"]
			if ok != tc.want {
				t.Fatalf("generated module present=%v, want %v: %v", ok, tc.want, files)
			}
		})
	}
}

func writeGitArchiveFixture(t *testing.T, files map[string]string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "input.tar.gz")
	f, err := os.Create(path) //nolint:forbidigo // test fixture creates its own archive
	if err != nil {
		t.Fatalf("create fixture: %v", err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		body := []byte(files[name])
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body))}); err != nil {
			t.Fatalf("write header %q: %v", name, err)
		}
		if _, err := tw.Write(body); err != nil {
			t.Fatalf("write body %q: %v", name, err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar fixture: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("close gzip fixture: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close fixture: %v", err)
	}
	return path
}

func readGitArchiveFixture(t *testing.T, path string) map[string][]byte {
	t.Helper()
	f, err := os.Open(path) //nolint:forbidigo // test reads the archive it just created
	if err != nil {
		t.Fatalf("open archive: %v", err)
	}
	defer func() { _ = f.Close() }()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatalf("open gzip: %v", err)
	}
	defer func() { _ = gz.Close() }()

	entries := map[string][]byte{}
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return entries
		}
		if err != nil {
			t.Fatalf("read archive: %v", err)
		}
		if hdr.Typeflag == tar.TypeDir || hdr.Typeflag == tar.TypeXHeader || hdr.Typeflag == tar.TypeXGlobalHeader {
			continue
		}
		body, err := io.ReadAll(tr)
		if err != nil {
			t.Fatalf("read entry %q: %v", hdr.Name, err)
		}
		entries[hdr.Name] = body
	}
}
