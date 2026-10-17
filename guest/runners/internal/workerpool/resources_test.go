// adr: 1573
package workerpool

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestResourceLifecycleRequiresExplicitSupportedHandshake(t *testing.T) {
	for _, version := range []int{0, 1, 2} {
		spec := Spec{Executable: os.Args[0], Args: []string{"-test.run=^TestWorkerpoolMuxHelperProcess$", "--"}, RequireResourceLifecycle: true,
			Env: append(os.Environ(), "GO_WANT_WORKERPOOL_MUX_HELPER=1", "HELPER_CONCURRENCY=1", "HELPER_COMPLETION_VERSION=1", "HELPER_RESOURCE_VERSION="+strconv.Itoa(version))}
		w, err := startWorker(t.Context(), spec)
		if w != nil {
			w.close()
		}
		if (err == nil) != (version == 1) {
			t.Fatalf("resource handshake version=%d err=%v", version, err)
		}
	}
}

func TestResourceLifecycleCannotFallBackOrReuseUnqualifiedPool(t *testing.T) {
	spec := Spec{Executable: "must-not-execute", HandlerPath: filepath.Join(t.TempDir(), "legacy.js"), RequireResourceLifecycle: true}
	if err := os.WriteFile(spec.HandlerPath, []byte("legacy adapter"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, handled, err := OpenIfSupported(t.Context(), spec, nil); !handled || err == nil {
		t.Fatal("qualified resource execution fell back to a legacy process")
	}
	if handled, err := InvokeIfSupported(t.Context(), spec, nil, nil); !handled || err == nil {
		t.Fatal("qualified resource invocation fell back to a legacy process")
	}
	if handled, err := PrewarmIfSupported(t.Context(), spec); !handled || err == nil {
		t.Fatal("qualified prewarm accepted a legacy process")
	}
	qualified := poolFor(spec)
	spec.RequireResourceLifecycle = false
	if poolFor(spec) == qualified {
		t.Fatal("qualified pool reused an unqualified command identity")
	}
}
