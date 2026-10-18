// spec: §4.2
package rootfs

import (
	"os"
	"path/filepath"
	"testing"
)

func TestApplyTarballLeadingDotWrapper(t *testing.T) {
	for _, name := range []string{"project/handler.js", "./project/handler.js", "././project/handler.js", "././handler.js"} {
		t.Run(name, func(t *testing.T) {
			archive := filepath.Join(t.TempDir(), "source.tar.gz")
			const source = "exports.handler = () => 'ok';\n"
			writeGzTar(t, archive, name, []byte(source))
			staging := t.TempDir()
			if err := ApplyTarball(staging, archive, 1<<20); err != nil {
				t.Fatal(err)
			}
			body, err := os.ReadFile(filepath.Join(staging, "app", "handler.js"))
			if err != nil || string(body) != source {
				t.Errorf("runtime handler = %q, %v; want source at /app/handler.js", body, err)
			}
		})
	}
}
