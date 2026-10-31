// adr: 20261025111015

package main

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

// A restore that promoted moves the database onto a new timeline, which
// fences object storage; accept records the new one and who accepted it.
func TestObjectStorageAcceptTakesTheCurrentTimeline(t *testing.T) {
	ctx := context.Background()
	st := state.NewMemStore()
	if err := st.BootstrapObjectStorageTimeline(ctx); err != nil {
		t.Fatal(err)
	}
	st.SetObjectStorageTimelineForTest("00000002")
	if accepted, current, _ := st.ObjectStorageTimelines(ctx); accepted == current {
		t.Fatalf("a restored timeline was already accepted: %s", accepted)
	}
	env := map[string]string{"SUDO_USER": "oncall"}
	got, err := acceptObjectStorageTimeline(ctx, st, "", func(k string) string { return env[k] })
	if err != nil || got != "00000002" {
		t.Fatalf("accept = %q, %v", got, err)
	}
	if accepted, current, _ := st.ObjectStorageTimelines(ctx); accepted != current {
		t.Fatalf("after accept: accepted %q current %q", accepted, current)
	}
}
