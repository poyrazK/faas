package frameworkprofile

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestAnalyzeTarballAtRoot_FlatAndWrappedArchives(t *testing.T) {
	entries := map[string]string{
		"package.json": `{"engines":{"node":"22.11.0"},"dependencies":{"express":"^5"},"scripts":{"start":"node server.js"}}`,
		"server.js":    "app.listen(process.env.PORT);\n",
	}
	for _, tc := range []struct {
		name    string
		entries map[string]string
	}{
		{name: "flat", entries: entries},
		{name: "wrapped", entries: map[string]string{
			"repo/package.json": entries["package.json"],
			"repo/server.js":    entries["server.js"],
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			archivePath := writeProfileArchive(t, tc.entries)
			got, err := AnalyzeTarballAtRoot(archivePath, "")
			if err != nil {
				t.Fatal(err)
			}
			if got.Framework != "express" || got.FrameworkVer != "22.11.0" || got.StartCommand != "npm run start" || got.Port != 3000 || !got.Inferred {
				t.Fatalf("profile = %+v, want express/22.11.0/npm run start/3000/inferred", got)
			}
		})
	}
}

func TestAnalyzeTarballAtRoot_UsesSelectedWorkspaceMember(t *testing.T) {
	archivePath := writeProfileArchive(t, map[string]string{
		"package.json":          `{"workspaces":["apps/*"]}`,
		"apps/api/package.json": `{"dependencies":{"hono":"^4"},"scripts":{"start":"node server.js"}}`,
		"apps/api/server.js":    "export default { fetch(){ return new Response('ok') } }\n",
		"apps/web/package.json": `{"dependencies":{"express":"^5"},"scripts":{"start":"node web.js"}}`,
	})
	got, err := AnalyzeTarballAtRoot(archivePath, "apps/api")
	if err != nil {
		t.Fatal(err)
	}
	if got.Framework != "hono" || got.StartCommand != "npm run start" {
		t.Fatalf("profile = %+v, want hono/npm run start", got)
	}
}

func TestAnalyzeTarballAtRoot_BoundsStaticInput(t *testing.T) {
	large := bytes.Repeat([]byte("x"), maxProfileArchiveBytes)
	archivePath := writeProfileArchiveBytes(t, map[string][]byte{
		"package.json": large,
	})
	got, err := AnalyzeTarballAtRoot(archivePath, "")
	if err != nil {
		t.Fatal(err)
	}
	if got.Framework != "unknown" || got.Inferred {
		t.Fatalf("profile = %+v, want unknown and not inferred", got)
	}
	seen := false
	for _, warning := range got.Warnings {
		if warning.Code == "profile_input_truncated" {
			seen = true
		}
	}
	if !seen {
		t.Fatalf("warnings = %+v, want profile_input_truncated", got.Warnings)
	}
}

func writeProfileArchive(t *testing.T, entries map[string]string) string {
	contents := make(map[string][]byte, len(entries))
	for name, body := range entries {
		contents[name] = []byte(body)
	}
	return writeProfileArchiveBytes(t, contents)
}

func writeProfileArchiveBytes(t *testing.T, entries map[string][]byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "source.tar.gz")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zr := gzip.NewWriter(f)
	tw := tar.NewWriter(zr)
	for name, body := range entries {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body))}); err != nil {
			t.Fatal(err)
		}
		if _, err := io.Copy(tw, bytes.NewReader(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zr.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}
