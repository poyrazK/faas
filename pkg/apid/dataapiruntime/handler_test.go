package dataapiruntime_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale/templates"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/dataapiruntime"
)

func TestServesTheEmbeddedRuntime(t *testing.T) {
	w := httptest.NewRecorder()
	dataapiruntime.Serve(w, httptest.NewRequest(http.MethodGet, "/v1/apps/notes/data-api/runtime", nil))
	if w.Code != http.StatusOK || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("serve = %d %v", w.Code, w.Header())
	}
	var runtime api.DataAPIRuntime
	if err := json.Unmarshal(w.Body.Bytes(), &runtime); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Validate(); err != nil {
		t.Fatal(err)
	}
	want, err := templates.DataAPIRuntimeVersion()
	if err != nil || runtime.Version != want {
		t.Fatalf("version = %q, want %q (%v)", runtime.Version, want, err)
	}
	files := untar(t, runtime.Archive)
	if !strings.Contains(string(files[templates.DataAPIRuntimeVersionFile]), want) {
		t.Fatalf("archive %s = %q, want version %s", templates.DataAPIRuntimeVersionFile, files[templates.DataAPIRuntimeVersionFile], want)
	}
	for _, required := range []string{"Dockerfile", "Procfile", "server.mjs", "package-lock.json"} {
		if _, ok := files[required]; !ok {
			t.Fatalf("archive lacks %s", required)
		}
	}
	for name := range files {
		if strings.HasPrefix(name, "test/") || strings.HasPrefix(name, ".") {
			t.Fatalf("archive ships %s", name)
		}
	}
	again, err := templates.DataAPIRuntimeArchive()
	if err != nil || !bytes.Equal(again, runtime.Archive) {
		t.Fatal("the runtime archive is not deterministic")
	}
}

func TestValidateRejectsATamperedArchive(t *testing.T) {
	runtime, err := dataapiruntime.Current()
	if err != nil {
		t.Fatal(err)
	}
	tampered := runtime
	tampered.Archive = append([]byte{}, runtime.Archive...)
	tampered.Archive[len(tampered.Archive)-1] ^= 1
	if tampered.Validate() == nil {
		t.Fatal("a tampered archive validated")
	}
	short := runtime
	short.Version = "abc"
	if short.Validate() == nil {
		t.Fatal("a malformed version validated")
	}
	large := runtime
	large.Archive = make([]byte, api.DataAPIRuntimeArchiveMaxBytes+1)
	if large.Validate() == nil {
		t.Fatal("an oversized archive validated")
	}
}

func untar(t *testing.T, archive []byte) map[string][]byte {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	files := map[string][]byte{}
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return files
		}
		if err != nil {
			t.Fatal(err)
		}
		name, ok := strings.CutPrefix(hdr.Name, templates.DataAPIRuntimeArchiveRoot+"/")
		if !ok {
			t.Fatalf("entry %s is outside %s/", hdr.Name, templates.DataAPIRuntimeArchiveRoot)
		}
		content, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		files[name] = content
	}
}
