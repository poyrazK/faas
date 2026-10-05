package imaged

// adr: 596

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

func TestRuntimeReleaseSplitBoxHandoff(t *testing.T) {
	dir := t.TempDir()
	output := filepath.Join(dir, "transfers")
	for _, command := range []string{"ssh", "rsync"} {
		body := "#!/bin/sh\nprintf '%s\\n' \"$@\" >> \"$REPLICATOR_TEST_OUTPUT\"\n"
		if err := os.WriteFile(filepath.Join(dir, command), []byte(body), 0700); err != nil {
			t.Fatal(err)
		}
	}
	r := state.RuntimeRelease{Runtime: "node22", Architecture: "amd64", ID: strings.Repeat("a", 64)}
	keys := []string{r.BaseKey(), baseContentKey(r.BaseKey()), wire.ScanKeyForBaseKey(r.BaseKey())}
	for _, key := range keys {
		path := filepath.Join(dir, key)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("published evidence"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	body, err := os.ReadFile("../../deploy/scripts/faas-artifact-replicator.sh")
	if err != nil {
		t.Fatal(err)
	}
	helper := filepath.Join(dir, "replicate.sh")
	if err := os.WriteFile(helper, body, 0700); err != nil {
		t.Fatal(err)
	}
	replicator := CommandArtifactReplicator{Path: helper, ExtraEnv: []string{
		"PATH=" + dir + ":/usr/bin:/bin", "REPLICATOR_TEST_OUTPUT=" + output,
		"FAAS_STORAGE_ROOT=" + dir, "FAAS_ARTIFACT_SYNC_TARGET=runtime.test.invalid", "FAAS_ARTIFACT_SYNC_USER=root",
		"FAAS_ARTIFACT_SYNC_STORAGE_ROOT=/test/destination",
	}}
	h := &Handler{replicator: replicator}
	if err := h.replicateRuntimeRelease(t.Context(), r); err != nil {
		t.Fatal(err)
	}
	transfers, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range keys {
		if !strings.Contains(string(transfers), filepath.Join(dir, key)+"\n") || !strings.Contains(string(transfers), "root@runtime.test.invalid:/test/destination/"+key+"\n") {
			t.Fatalf("missing runtime handoff %q: %s", key, transfers)
		}
	}
	if err := os.Remove(filepath.Join(dir, keys[2])); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(output, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := h.replicateRuntimeRelease(t.Context(), r); err == nil {
		t.Fatal("missing scan evidence allowed handoff")
	}
	if got, err := os.ReadFile(output); err != nil || len(got) != 0 {
		t.Fatal("incomplete release started transfer", string(got), err)
	}
	if err := replicator.ReplicateRuntimeRelease(t.Context(), "base/releases/../../escape.ext4"); err == nil {
		t.Fatal("unsafe key allowed")
	}
	h.replicator = &fakeReplicator{}
	if err := h.replicateRuntimeRelease(t.Context(), r); err == nil {
		t.Fatal("old helper capability silently accepted")
	}
}
