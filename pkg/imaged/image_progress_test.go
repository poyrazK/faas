package imaged

import (
	"context"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestImageProgressWritesPerWorkloadLines(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()

	imageProgressf(ctx, "no reporter: dropped")
	app := withImageProgress(ctx, store, "dep-1", "app image")
	imageProgressf(app, "preparing %s", "example.com/kroki@sha256:abc")
	comp := withImageProgress(app, store, "dep-1", companionWorkload("mermaid"))
	imageProgressf(comp, "pulled %d layers in %s", 7, "3.2s")

	rows, _, err := store.ListDeploymentLogs(ctx, "dep-1", 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %+v, want two progress lines", rows)
	}
	// DESC by seq.
	if rows[1].Stream != imageProgressStream || rows[1].Line != "[image] app image: preparing example.com/kroki@sha256:abc\n" {
		t.Fatalf("app line = %+v", rows[1])
	}
	if !strings.HasPrefix(rows[0].Line, `[image] companion "mermaid": pulled 7 layers`) {
		t.Fatalf("companion line = %q", rows[0].Line)
	}
	if got := withImageProgress(ctx, nil, "dep-1", "x"); got != ctx {
		t.Fatal("a nil store must leave ctx unchanged")
	}
}
