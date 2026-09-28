package main

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/jobresult"
)

type jobObjectReader struct{ body string }

func (r jobObjectReader) ReadObject(_ context.Context, _, _ string) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader(r.body)), nil
}

func TestJobManagedObjectManifestAndVerification(t *testing.T) {
	uri := "obj://123e4567-e89b-12d3-a456-426614174000/123e4567-e89b-12d3-a456-426614174001/results/task.json"
	app, bucket, key, err := parseJobManagedObjectURI(uri)
	if err != nil || app == "" || bucket == "" || key != "results/task.json" {
		t.Fatalf("parse = %q %q %q, %v", app, bucket, key, err)
	}
	if _, _, _, err := parseJobManagedObjectURI("obj://other-account/bucket/key"); err == nil {
		t.Fatal("accepted non-UUID bucket identity")
	}
	body := `[{"input_id":"first","input_ref":"data/first"}]`
	digest := fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(body)))
	inputs, err := readJobInputManifest(context.Background(), jobObjectReader{body}, bucket, key, digest)
	if err != nil || len(inputs) != 1 || inputs[0].ID != "first" {
		t.Fatalf("inputs = %+v, %v", inputs, err)
	}
	if _, err := readJobInputManifest(context.Background(), jobObjectReader{body}, bucket, key, "sha256:"+strings.Repeat("0", 64)); err == nil {
		t.Fatal("accepted wrong input checksum")
	}
	artifact := jobresult.Artifact{Name: "part", URI: uri, SizeBytes: int64(len(body)), SHA256: digest}
	if _, err := verifyJobManagedArtifact(context.Background(), jobObjectReader{body}, bucket, key, artifact); err != nil {
		t.Fatal(err)
	}
	artifact.SizeBytes++
	if _, err := verifyJobManagedArtifact(context.Background(), jobObjectReader{body}, bucket, key, artifact); err == nil {
		t.Fatal("accepted wrong artifact size")
	}
}
