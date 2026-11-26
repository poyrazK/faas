package gregalemanifest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Prod hunt r57: a top-level `name:` was refused as an unknown key with no
// way forward; the error now says where the app name comes from.
func TestLoad_TopLevelNameSaysHowToNameTheApp(t *testing.T) {
	for _, key := range []string{"name", "app", "slug"} {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "gregale.yaml"), []byte("schema_version: 1\n"+key+": my-worker\n"), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		_, _, err := Load(dir)
		if err == nil || !strings.Contains(err.Error(), "--name SLUG") {
			t.Errorf("%s: error = %v, want the --name hint", key, err)
		}
	}
}
