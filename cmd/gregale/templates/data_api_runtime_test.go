package templates

import (
	"os"
	"path/filepath"
	"testing"
)

// The version a running Data API reports is the digest of its image files, so
// a changed runtime always reads as a different version. Regenerate with
// UPDATE_DATA_API_RUNTIME_VERSION=1 go test ./cmd/gregale/templates -run DataAPIRuntimeVersion.
func TestDataAPIRuntimeVersionMatchesItsFiles(t *testing.T) {
	digest, err := DataAPIRuntimeDigest()
	if err != nil {
		t.Fatal(err)
	}
	if os.Getenv("UPDATE_DATA_API_RUNTIME_VERSION") == "1" {
		content := []byte("{\n  \"version\": \"" + digest + "\"\n}\n")
		if err = os.WriteFile(filepath.Join("data-api", DataAPIRuntimeVersionFile), content, 0o644); err != nil {
			t.Fatal(err)
		}
		t.Skip("rewrote data-api/" + DataAPIRuntimeVersionFile + "; rerun to check")
	}
	version, err := DataAPIRuntimeVersion()
	if err != nil {
		t.Fatal(err)
	}
	if version != digest {
		t.Fatalf("data-api/%s = %s, runtime files digest to %s; run UPDATE_DATA_API_RUNTIME_VERSION=1 go test ./cmd/gregale/templates -run DataAPIRuntimeVersion", DataAPIRuntimeVersionFile, version, digest)
	}
}

func TestDataAPIRuntimeDigestIgnoresDocsAndTests(t *testing.T) {
	copied, err := dockerfileSourceCopies([]byte("FROM a AS b\nFROM c\nCOPY --from=b /bin/x /usr/bin/x\nCOPY package.json package-lock.json ./\nCOPY server.mjs runtime-version.json ./\n"))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"package.json", "package-lock.json", "server.mjs", "runtime-version.json"}
	if len(copied) != len(want) {
		t.Fatalf("copies = %v, want %v", copied, want)
	}
	for i := range want {
		if copied[i] != want[i] {
			t.Fatalf("copies = %v, want %v", copied, want)
		}
	}
	for _, dockerfile := range []string{"FROM c\nCOPY . ./\n", "FROM c\nCOPY *.mjs ./\n", "FROM c\n"} {
		if _, err := dockerfileSourceCopies([]byte(dockerfile)); err == nil {
			t.Fatalf("%q was accepted; the digest must name every runtime file", dockerfile)
		}
	}
}

func TestValidDataAPIRuntimeVersion(t *testing.T) {
	for v, want := range map[string]bool{"0123456789abcdef": true, "0123456789ABCDEF": false, "0123456789abcde": false, "0123456789abcdeg": false, "": false} {
		if got := ValidDataAPIRuntimeVersion(v); got != want {
			t.Errorf("ValidDataAPIRuntimeVersion(%q) = %v, want %v", v, got, want)
		}
	}
}
