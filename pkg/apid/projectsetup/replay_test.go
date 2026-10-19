package projectsetup

import (
	"errors"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestReplayStreamsMetadataAndExactReviewedSource(t *testing.T) {
	path := filepath.Join(t.TempDir(), "source.tar.gz")
	if err := os.WriteFile(path, []byte("reviewed-source"), 0o600); err != nil {
		t.Fatal(err)
	}
	original := httptest.NewRequest("POST", "/v1/projects/apply-plan", nil)
	original.Header.Set("Idempotency-Key", "stable")
	req, closeRequest, err := Replay(original, path, map[string]string{"initial_secrets": `{"api":{"TOKEN":"transient"}}`, "only": "api"})
	if err != nil {
		t.Fatal(err)
	}
	defer closeRequest()
	reader, err := req.MultipartReader()
	if err != nil {
		t.Fatal(err)
	}
	parts := map[string]string{}
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(part)
		if err != nil {
			t.Fatal(err)
		}
		parts[part.FormName()] = string(body)
	}
	if parts["source"] != "reviewed-source" || parts["only"] != "api" || parts["initial_secrets"] != `{"api":{"TOKEN":"transient"}}` || req.Header.Get("Idempotency-Key") != "stable" {
		t.Fatalf("unexpected replay fields or headers")
	}
	if original.Header.Get("Content-Type") != "" {
		t.Fatal("original request mutated")
	}
}
