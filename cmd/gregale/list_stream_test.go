package main_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"reflect"
	"syscall"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

// adr: 20261115105130 — bounded pages, immediate ordered rows, explicit partial output.
func TestStreamListPageGuards(t *testing.T) {
	for _, mode := range []string{"success", "empty", "terminal-empty", "error", "repeat", "cycle", "bound", "last-page", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var out bytes.Buffer
			calls := 0
			failure := errors.New("page failed")
			progress, err := main.StreamListPages(ctx, &out, "start", func(_ context.Context, cursor string) ([]int, string, error) {
				calls++
				if calls == 2 && mode != "empty" && out.String() != "1\n" {
					t.Fatalf("first page was not emitted before fetching the next: %q", out.String())
				}
				if mode == "error" && calls == 2 {
					return nil, "", failure
				}
				if mode == "cancel" {
					cancel()
				}
				if mode == "repeat" {
					return []int{calls}, cursor, nil
				}
				if mode == "cycle" && calls == 3 {
					return []int{calls}, "page-1", nil
				}
				if mode == "terminal-empty" {
					return nil, "", nil
				}
				if mode == "last-page" && calls == main.MaxCLIListPages {
					return []int{calls}, "", nil
				}
				if mode == "empty" && calls == 1 {
					return nil, "page-1", nil
				}
				if (mode == "success" || mode == "empty") && calls == 2 {
					return []int{calls}, "", nil
				}
				return []int{calls}, fmt.Sprintf("page-%d", calls), nil
			})
			want := main.ListStreamProgress{}
			switch mode {
			case "success":
				want = main.ListStreamProgress{PagesCompleted: 2, ItemsEmitted: 2}
				if err != nil || out.String() != "1\n2\n" {
					t.Fatal(out.String(), err)
				}
			case "empty":
				want = main.ListStreamProgress{PagesCompleted: 2, ItemsEmitted: 1}
				if err != nil || out.String() != "2\n" {
					t.Fatal(out.String(), err)
				}
			case "terminal-empty":
				want = main.ListStreamProgress{PagesCompleted: 1}
				if err != nil || out.Len() != 0 {
					t.Fatal(out.String(), err)
				}
			case "error":
				want = main.ListStreamProgress{PagesCompleted: 1, ItemsEmitted: 1, Partial: true}
				if !errors.Is(err, failure) || calls != 2 {
					t.Fatal(err, calls)
				}
			case "repeat":
				if err == nil || calls != 1 || out.Len() != 0 {
					t.Fatal(err, calls, out.String())
				}
			case "cycle":
				want = main.ListStreamProgress{PagesCompleted: 2, ItemsEmitted: 2, Partial: true}
				if err == nil || calls != 3 || out.String() != "1\n2\n" {
					t.Fatal(err, calls, out.String())
				}
			case "bound":
				want = main.ListStreamProgress{PagesCompleted: main.MaxCLIListPages, ItemsEmitted: main.MaxCLIListPages, Partial: true}
				if err == nil || calls != main.MaxCLIListPages {
					t.Fatal(err, calls)
				}
			case "last-page":
				want = main.ListStreamProgress{PagesCompleted: main.MaxCLIListPages, ItemsEmitted: main.MaxCLIListPages}
				if err != nil || calls != main.MaxCLIListPages {
					t.Fatal(err, calls)
				}
			case "cancel":
				if !errors.Is(err, context.Canceled) || calls != 1 || out.Len() != 0 {
					t.Fatal(err, calls, out.String())
				}
			}
			if !reflect.DeepEqual(progress, want) {
				t.Fatalf("progress=%+v want=%+v", progress, want)
			}
		})
	}
}

type brokenListWriter struct {
	bytes.Buffer
	writes int
	short  bool
}

func (w *brokenListWriter) Write(p []byte) (int, error) {
	w.writes++
	if w.short {
		return 1, nil
	}
	if w.writes == 2 {
		return 0, syscall.EPIPE
	}
	return w.Buffer.Write(p)
}

func TestStreamListStopsOnOutputFailure(t *testing.T) {
	for _, short := range []bool{false, true} {
		writer := &brokenListWriter{short: short}
		calls := 0
		progress, err := main.StreamListPages(context.Background(), writer, "", func(context.Context, string) ([]int, string, error) {
			calls++
			return []int{100, 200, 300}, "next", nil
		})
		wantErr := error(syscall.EPIPE)
		wantRows := 1
		if short {
			wantErr = io.ErrShortWrite
			wantRows = 0
		}
		if !errors.Is(err, wantErr) || calls != 1 || progress.PagesCompleted != 0 || progress.ItemsEmitted != wantRows || !progress.Partial {
			t.Fatalf("progress=%+v calls=%d err=%v", progress, calls, err)
		}
	}
}

func TestStreamListCancellationBetweenRows(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	writer := &cancelListWriter{cancel: cancel}
	progress, err := main.StreamListPages(ctx, writer, "", func(context.Context, string) ([]int, string, error) { calls++; return []int{1, 2}, "next", nil })
	if !errors.Is(err, context.Canceled) || calls != 1 || writer.String() != "1\n" || progress.PagesCompleted != 0 || progress.ItemsEmitted != 1 || !progress.Partial {
		t.Fatalf("progress=%+v calls=%d err=%v output=%q", progress, calls, err, writer.String())
	}
}

type cancelListWriter struct {
	bytes.Buffer
	cancel context.CancelFunc
}

func (w *cancelListWriter) Write(p []byte) (int, error) {
	n, err := w.Buffer.Write(p)
	w.cancel()
	return n, err
}
