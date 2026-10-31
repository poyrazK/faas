package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale/templates"
	"github.com/onebox-faas/faas/pkg/api"
)

// servedRuntimeVersion is the version servedDataAPIRuntime reports; it differs
// from the embedded one so a test can tell which runtime was uploaded.
const servedRuntimeVersion = "0123456789abcdef"

// servedDataAPIRuntime is a control-plane runtime whose server.mjs ends with
// marker.
func servedDataAPIRuntime(t testing.TB, marker string) api.DataAPIRuntime {
	t.Helper()
	files, err := templates.DataAPIRuntimeFiles()
	if err != nil {
		t.Fatal(err)
	}
	files["server.mjs"] = append(append([]byte{}, files["server.mjs"]...), []byte("\n// "+marker+"\n")...)
	files[templates.DataAPIRuntimeVersionFile] = []byte(`{"version":"` + servedRuntimeVersion + `"}`)
	archive, err := templates.DataAPIRuntimeArchiveOf(files)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(archive)
	return api.DataAPIRuntime{Version: servedRuntimeVersion, ArchiveSHA256: hex.EncodeToString(sum[:]), Archive: archive}
}

func embeddedRuntimeForTest(t testing.TB) dataAPIRuntimeBundle {
	t.Helper()
	runtime, err := embeddedDataAPIRuntime()
	if err != nil {
		t.Fatal(err)
	}
	return runtime
}

// uploadedSourceFiles returns the files of the source archive in a deployment
// upload, keyed by archive path.
func uploadedSourceFiles(t testing.TB, contentType string, body []byte) map[string]string {
	t.Helper()
	_, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		t.Fatal(err)
	}
	reader := multipart.NewReader(bytes.NewReader(body), params["boundary"])
	for {
		part, err := reader.NextPart()
		if err != nil {
			t.Fatalf("no source part: %v", err)
		}
		if part.FormName() != "source" {
			continue
		}
		gz, err := gzip.NewReader(part)
		if err != nil {
			t.Fatal(err)
		}
		files := map[string]string{}
		tr := tar.NewReader(gz)
		for {
			hdr, err := tr.Next()
			if errors.Is(err, io.EOF) {
				return files
			}
			if err != nil {
				t.Fatal(err)
			}
			content, err := io.ReadAll(tr)
			if err != nil {
				t.Fatal(err)
			}
			files[hdr.Name] = string(content)
		}
	}
}

func uploadedServerHas(files map[string]string, marker string) bool {
	return strings.Contains(files[templates.DataAPIRuntimeArchiveRoot+"/server.mjs"], marker)
}
