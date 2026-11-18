package imaged

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/onebox-faas/faas/pkg/oci"
	"github.com/onebox-faas/faas/pkg/state"
)

// The layer step logs where its time went, including the steps the rootfs
// builder reports through its Phase hook (2026-11-17: about 27 s of each
// deploy's preparation at p50, previously logged as one total).
func TestImagePreparationLogsLayerTiming(t *testing.T) {
	store, app, dep := imagePreparationFixture(t, "memory")
	builder := &fakeBuilder{bytesOut: 9}
	builder.buildHook = func() {
		in := builder.calls[len(builder.calls)-1]
		if in.Phase == nil {
			t.Error("layer build ran without a phase hook")
			return
		}
		in.Phase("mkfs")
	}
	var logs bytes.Buffer
	h := New(store, &fakeNotifier{}, fakePuller{digest: "sha256:abc", cfg: oci.ImageConfig{Cmd: []string{"./app"}}}, builder, "./init", t.TempDir(), slog.New(slog.NewJSONHandler(&logs, nil))).
		WithNodeName("node-a").WithGrypeRun(func(context.Context, string) (*ScanResult, error) { return &ScanResult{}, nil })
	if err := h.handleSnapshotBoot(t.Context(), snapshotBootPayload{AppID: app.ID, DeploymentID: dep.ID, NodeID: "node-a"}); err != nil {
		t.Fatal(err)
	}
	var line map[string]any
	for sc := bufio.NewScanner(&logs); sc.Scan(); {
		var entry map[string]any
		if json.Unmarshal(sc.Bytes(), &entry) == nil && entry["msg"] == "imaged: layer timing" {
			line = entry
		}
	}
	if line == nil {
		t.Fatal("no layer timing line")
	}
	if line["deployment"] != dep.ID || line["kind"] != string(state.DeploymentKindImage) {
		t.Fatalf("layer timing identifies %v/%v", line["deployment"], line["kind"])
	}
	for _, key := range []string{"open_ms", "mkfs_ms", "checkpoint_ms", "total_ms"} {
		if _, ok := line[key]; !ok {
			t.Fatalf("layer timing lacks %s: %v", key, line)
		}
	}
}
