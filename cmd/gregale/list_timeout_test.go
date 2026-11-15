package main_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

func TestListTimeoutValidationAndContext(t *testing.T) {
	for _, selected := range []bool{false, true} {
		for _, duration := range []time.Duration{-time.Second, 0, time.Nanosecond, time.Second} {
			err := main.ValidateListTimeout(duration, selected)
			if (err != nil) != (selected && duration <= 0) {
				t.Fatalf("%s selected=%t: %v", duration, selected, err)
			}
		}
	}
	parent := context.WithValue(context.Background(), struct{}{}, "kept")
	ctx, cancel := main.ListReadContext(parent, 0)
	cancel()
	if ctx != parent || ctx.Err() != nil {
		t.Fatal("omitted timeout changed caller context")
	}
	ctx, cancel = main.ListReadContext(parent, time.Hour)
	defer cancel()
	if _, ok := ctx.Deadline(); !ok || ctx.Value(struct{}{}) != "kept" {
		t.Fatal("deadline/value missing")
	}
	earlier, stop := context.WithDeadline(parent, time.Now().Add(time.Second))
	defer stop()
	child, finish := main.ListReadContext(earlier, time.Hour)
	defer finish()
	first, _ := earlier.Deadline()
	second, _ := child.Deadline()
	if !first.Equal(second) {
		t.Fatal("extended parent deadline")
	}
	cancelled, abort := context.WithCancel(parent)
	abort()
	child, finish = main.ListReadContext(cancelled, time.Hour)
	defer finish()
	if !errors.Is(child.Err(), context.Canceled) {
		t.Fatal(child.Err())
	}
}

func TestListReadErrorRetainsRealFailureAndDeadline(t *testing.T) {
	ctx, cancel := main.ListReadContext(context.Background(), time.Millisecond)
	defer cancel()
	<-ctx.Done()
	if !errors.Is(main.ListReadError(ctx, nil), context.DeadlineExceeded) {
		t.Fatal("successful terminal read ignored deadline")
	}
	actual := errors.New("actual read failure")
	if main.ListReadError(ctx, actual) != actual {
		t.Fatal("deadline replaced actual API/output failure")
	}
	if main.ListReadError(context.Background(), nil) != nil {
		t.Fatal("default context acquired an error")
	}
}

func TestListPublicationDeadlineDuringSync(t *testing.T) {
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err := main.PublishListAfterSyncDeadline(root, "rows.jsonl", 20*time.Millisecond); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(dir, "rows.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("expired export published: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("private file not cleaned: %v %v", entries, err)
	}
}
