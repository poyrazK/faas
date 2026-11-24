package markers

import (
	"archive/tar"
	"compress/gzip"
	"os"
	"strings"
	"testing"
)

// Prod 2026-11-23: an invalid package.json built into an image with no app
// layer and failed later with an internal error blamed on the platform. What
// Railpack's hujson reader rejects is a problem (including a leading BOM,
// prod 2026-11-24); comments and trailing commas are not.
func TestNodePackageJSONProblemAtRoot(t *testing.T) {
	for _, tc := range []struct {
		name  string
		files map[string]string
		root  string
		want  string
	}{
		{"valid", map[string]string{"package.json": `{"name":"x"}`}, "", ""},
		{"absent", map[string]string{"requirements.txt": "flask\n"}, "", ""},
		{"utf-8 byte order mark", map[string]string{"package.json": "\xef\xbb\xbf{\"name\":\"x\"}\n"}, "", "package.json starts with a UTF-8 byte order mark"},
		{"trailing comma", map[string]string{"package.json": "{\n  \"name\": \"x\",\n}\n"}, "", ""},
		{"comments", map[string]string{"package.json": "{\n  // the app\n  \"name\": \"x\", /* inline */\n  \"main\": \"a//b.js\"\n}\n"}, "", ""},
		{"truncated", map[string]string{"package.json": "{\"name\": \"x\",\n\"scripts\": {\"start\": \"node s.js\"},\n"}, "", "package.json is not valid JSON: unexpected end of JSON input (line 3)"},
		{"missing comma", map[string]string{"package.json": "{\n  \"name\": \"x\"\n  \"main\": \"s.js\"\n}\n"}, "", "package.json is not valid JSON: invalid character '\"' after object key:value pair (line 3)"},
		{"array", map[string]string{"package.json": `["x"]`}, "", "package.json is not a JSON object"},
		{"workspace member", map[string]string{"package.json": `{}`, "apps/api/package.json": `{`}, "apps/api", "package.json is not valid JSON: unexpected end of JSON input (line 1)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := tempTarball(t)
			writeTarGz(t, path, tc.files)
			got := NodePackageJSONProblemAtRoot(path, tc.root)
			if tc.want == "" && got != "" || tc.want != "" && !strings.HasPrefix(got, tc.want) {
				t.Fatalf("NodePackageJSONProblemAtRoot = %q, want prefix %q", got, tc.want)
			}
		})
	}
}

// A package.json that is an in-archive link has no body in the tarball; the
// build guest resolves it, so it is not judged here.
func TestNodePackageJSONProblemIgnoresLinks(t *testing.T) {
	for _, typeflag := range []byte{tar.TypeSymlink, tar.TypeLink} {
		path := tempTarball(t)
		f, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		gz := gzip.NewWriter(f)
		tw := tar.NewWriter(gz)
		body := `{"name":"shared"}`
		if err := tw.WriteHeader(&tar.Header{Name: "shared/package.json", Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
		if err := tw.WriteHeader(&tar.Header{Name: "package.json", Linkname: "shared/package.json", Mode: 0o644, Typeflag: typeflag}); err != nil {
			t.Fatal(err)
		}
		if err := tw.Close(); err != nil {
			t.Fatal(err)
		}
		if err := gz.Close(); err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
		if got := NodePackageJSONProblemAtRoot(path, ""); got != "" {
			t.Fatalf("typeflag %q: linked package.json judged invalid: %q", typeflag, got)
		}
	}
}
