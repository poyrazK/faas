package main

import (
	"archive/tar"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func buildTestTar(t *testing.T, entries []tar.Header, bodies map[string]string) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, header := range entries {
		body := bodies[header.Name]
		header.Size = int64(len(body))
		if err := tw.WriteHeader(&header); err != nil {
			t.Fatal(err)
		}
		_, _ = tw.Write([]byte(body))
	}
	_ = tw.Close()
	return &buf
}

func distArchive(t *testing.T) *bytes.Buffer {
	return buildTestTar(t, []tar.Header{
		{Name: "dist/", Typeflag: tar.TypeDir, Mode: 0o755},
		{Name: "dist/index.html", Typeflag: tar.TypeReg, Mode: 0o644},
		{Name: "dist/assets/app.js", Typeflag: tar.TypeReg, Mode: 0o600},
	}, map[string]string{"dist/index.html": "<html>", "dist/assets/app.js": "js"})
}

func TestExtractAppTaskTarIntoExistingDirectory(t *testing.T) {
	dest := t.TempDir()
	if err := extractAppTaskTar(distArchive(t), dest); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(dest, "dist", "assets", "app.js"))
	if err != nil || string(body) != "js" {
		t.Fatalf("app.js = %q, %v", body, err)
	}
}

func TestExtractAppTaskTarRenamesToMissingDestination(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "site")
	if err := extractAppTaskTar(distArchive(t), dest); err != nil {
		t.Fatal(err)
	}
	if body, err := os.ReadFile(filepath.Join(dest, "index.html")); err != nil || string(body) != "<html>" {
		t.Fatalf("site/index.html = %q, %v", body, err)
	}
	file := filepath.Join(t.TempDir(), "VERSION.txt")
	single := buildTestTar(t, []tar.Header{{Name: "VERSION", Typeflag: tar.TypeReg, Mode: 0o644}}, map[string]string{"VERSION": "7"})
	if err := extractAppTaskTar(single, file); err != nil {
		t.Fatal(err)
	}
	if body, _ := os.ReadFile(file); string(body) != "7" {
		t.Fatalf("single file = %q", body)
	}
}

func TestExtractAppTaskTarRefusesEscapes(t *testing.T) {
	for name, entries := range map[string][]tar.Header{
		"parent path":   {{Name: "../evil", Typeflag: tar.TypeReg, Mode: 0o644}},
		"absolute path": {{Name: "/etc/evil", Typeflag: tar.TypeReg, Mode: 0o644}},
		"through link": {
			{Name: "x/", Typeflag: tar.TypeDir, Mode: 0o755},
			{Name: "x/out", Typeflag: tar.TypeSymlink, Linkname: "/tmp"},
			{Name: "x/out/evil", Typeflag: tar.TypeReg, Mode: 0o644},
		},
	} {
		dest := t.TempDir()
		err := extractAppTaskTar(buildTestTar(t, entries, nil), dest)
		if err == nil {
			t.Fatalf("%s: extraction succeeded", name)
		}
	}
}

func TestParsePortForwardSpec(t *testing.T) {
	for spec, want := range map[string]struct {
		local  int
		target string
	}{
		"db.svc.gregale:5432":       {5432, "db.svc.gregale:5432"},
		"15432:db.svc.gregale:5432": {15432, "db.svc.gregale:5432"},
		"0:[fd00::1]:8080":          {0, "[fd00::1]:8080"},
		"[fd00::1]:8080":            {8080, "[fd00::1]:8080"},
	} {
		local, target, err := parsePortForwardSpec(spec)
		if err != nil || local != want.local || target != want.target {
			t.Fatalf("%s = %d %q %v", spec, local, target, err)
		}
	}
	for _, bad := range []string{"db", "db:0", "70000:db:5432", "x:db:5432", ":5432"} {
		if _, _, err := parsePortForwardSpec(bad); err == nil || !strings.Contains(err.Error(), "") {
			t.Fatalf("%s accepted", bad)
		}
	}
}
