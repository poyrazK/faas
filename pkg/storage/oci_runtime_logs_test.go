package storage

import (
	"github.com/onebox-faas/faas/pkg/region"
	"io"
	"strings"
	"testing"
)

func TestOCIRuntimeLogArchiveFreshReader(t *testing.T) {
	registry := newFakeRegistry(t)
	writer := registry.client(t)
	key := region.RuntimeLogArchiveKey("", "ad0813cb-8311-43a8-8c17-60ad618eb162", "2026-10-02")
	if err := writer.Put(t.Context(), key, strings.NewReader("gzip archive")); err != nil {
		t.Fatal(err)
	}
	reader := registry.client(t)
	r, err := reader.Get(t.Context(), key)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(r)
	_ = r.Close()
	if err != nil || string(body) != "gzip archive" {
		t.Fatalf("body=%q err=%v", body, err)
	}
	refs, err := reader.List(t.Context(), "faas-logs/")
	if err != nil || len(refs) != 1 || refs[0] != key {
		t.Fatalf("list=%+v %v", refs, err)
	}
	if err := reader.Delete(t.Context(), key); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.Get(t.Context(), key); !IsNotFound(err) {
		t.Fatalf("archive survived delete: %v", err)
	}
	for _, bad := range []string{"faas-logs/../2026/10/2026-10-02.jsonl.gz", "faas-logs/i/2026/09/2026-10-02.jsonl.gz", region.RuntimeLogArchiveKey("", strings.Repeat("x", 128), "2026-10-02")} {
		if _, _, err := reader.plan(bad); err == nil {
			t.Fatalf("bad OCI key accepted: %q", bad)
		}
	}
}
