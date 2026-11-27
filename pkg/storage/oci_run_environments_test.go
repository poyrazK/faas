package storage

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

func TestRunEnvironmentOCISurvivesClientRestartAndDeletes(t *testing.T) {
	f := newFakeRegistry(t)
	writer := f.client(t)
	stem := "run-environments/12345678-1234-4234-8234-123456789abc/22345678-1234-4234-8234-123456789abc/32345678-1234-4234-8234-123456789abc/"
	keys := []string{stem + "image.tar", stem + "base.ext4", stem + "layer.ext4", stem + "base.ext4.digest", stem + "base.ext4.content", "scans/" + stem + "base.ext4.scan.json"}
	for _, key := range keys {
		repo, tag, err := writer.plan(key)
		if err != nil || len(tag) > 128 {
			t.Fatal(repo, tag, err)
		}
		if decoded, ok := writer.unplan(repo, tag); !ok || decoded != key {
			t.Fatal(decoded, ok)
		}
		if err := writer.Put(t.Context(), key, bytes.NewBufferString(key)); err != nil {
			t.Fatal(err)
		}
	}
	reader := f.client(t)
	for _, prefix := range []string{stem, "scans/" + stem} {
		listed, err := reader.List(t.Context(), prefix)
		want := 5
		if strings.HasPrefix(prefix, "scans/") {
			want = 1
		}
		if err != nil || len(listed) != want {
			t.Fatal(listed, err)
		}
	}
	for _, key := range keys {
		r, err := reader.Get(t.Context(), key)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(r)
		_ = r.Close()
		if err != nil || string(body) != key {
			t.Fatal(string(body), err)
		}
		if err := reader.Delete(t.Context(), key); err != nil {
			t.Fatal(err)
		}
		if exists, err := reader.Exists(t.Context(), key); err != nil || exists {
			t.Fatal(key, exists, err)
		}
	}
}

func TestRunEnvironmentOCIRejectsUnknownAndNoncanonicalKeys(t *testing.T) {
	stem := "run-environments/12345678-1234-4234-8234-123456789abc/22345678-1234-4234-8234-123456789abc/32345678-1234-4234-8234-123456789abc/"
	for _, key := range []string{stem + "secret", stem + "../base.ext4", strings.Replace(stem, "22345678", "INVALID", 1) + "base.ext4", "scans/" + stem + "image.tar"} {
		if _, _, err := planRunEnvironment(key); err == nil {
			t.Fatal("accepted", key)
		}
	}
}
