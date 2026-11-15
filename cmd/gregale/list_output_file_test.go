package main_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

func newListFileTest(t *testing.T) (string, *main.ListOutputFile) {
	t.Helper()
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close() })
	file, err := main.NewListOutputFile(root, "rows.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = file.Close() })
	return dir, file
}

func assertListDestinationAbsent(t *testing.T, dir string) {
	t.Helper()
	if _, err := os.Lstat(filepath.Join(dir, "rows.jsonl")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("destination visible before successful publication: %v", err)
	}
}

// A real file and pinned directory qualify publication and receipt integrity.
func TestListOutputFilePublishesOnlyCompletePages(t *testing.T) {
	for _, empty := range []bool{false, true} {
		t.Run(fmt.Sprint(empty), func(t *testing.T) {
			dir, file := newListFileTest(t)
			type row struct {
				ID string `json:"id"`
				N  int64  `json:"n"`
			}
			calls := 0
			progress, err := main.StreamListPages(context.Background(), file, "", func(_ context.Context, cursor string) ([]row, string, error) {
				assertListDestinationAbsent(t, dir)
				calls++
				if empty {
					return nil, "", nil
				}
				switch cursor {
				case "":
					return []row{{"first", 9007199254740993}}, "empty", nil
				case "empty":
					return nil, "last", nil
				default:
					return []row{{"second", 2}}, "", nil
				}
			})
			if err != nil {
				t.Fatal(err)
			}
			assertListDestinationAbsent(t, dir)
			entries, err := os.ReadDir(dir)
			if err != nil || len(entries) != 1 {
				t.Fatalf("private files: %v %v", entries, err)
			}
			info, err := entries[0].Info()
			if err != nil || info.Mode().Perm() != 0600 {
				t.Fatalf("private mode: %v %v", info, err)
			}
			receipt, err := file.Publish(context.Background(), progress)
			if err != nil {
				t.Fatal(err)
			}
			if err := file.Close(); err != nil {
				t.Fatal(err)
			}
			if err := file.Close(); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(filepath.Join(dir, "rows.jsonl"))
			if err != nil {
				t.Fatal(err)
			}
			want := "{\"id\":\"first\",\"n\":9007199254740993}\n{\"id\":\"second\",\"n\":2}\n"
			pages, items := 3, 2
			if empty {
				want = ""
				pages = 1
				items = 0
			}
			digest := sha256.Sum256(data)
			if string(data) != want || !receipt.Published || receipt.Format != "jsonl" || receipt.PagesCompleted != pages || receipt.ItemsEmitted != items || receipt.BytesWritten != int64(len(data)) || receipt.SHA256 != hex.EncodeToString(digest[:]) || calls != pages {
				t.Fatalf("receipt=%+v calls=%d bytes=%q", receipt, calls, data)
			}
			info, err = os.Stat(filepath.Join(dir, "rows.jsonl"))
			if err != nil || info.Mode().Perm() != 0600 {
				t.Fatalf("published mode: %v %v", info, err)
			}
			entries, _ = os.ReadDir(dir)
			if len(entries) != 1 || entries[0].Name() != "rows.jsonl" {
				t.Fatalf("temporary output retained: %v", entries)
			}
		})
	}
}

func TestListOutputFileIOFailuresNeverPublish(t *testing.T) {
	for _, phase := range []string{"write", "short", "sync", "close", "cancel-before", "cancel-sync"} {
		t.Run(phase, func(t *testing.T) {
			dir, file := newListFileTest(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var stop context.CancelFunc
			if phase == "cancel-sync" {
				stop = cancel
			}
			main.FailListOutputFile(file, phase, stop)
			_, writeErr := file.Write([]byte("{\"id\":1}\n"))
			if (phase == "write" || phase == "short") && writeErr == nil {
				t.Fatal("write unexpectedly succeeded")
			}
			if phase == "cancel-before" {
				cancel()
			}
			receipt, err := file.Publish(ctx, main.ListStreamProgress{PagesCompleted: 1, ItemsEmitted: 1})
			if err == nil || receipt != nil {
				t.Fatalf("failed output published: %+v %v", receipt, err)
			}
			assertListDestinationAbsent(t, dir)
			_ = file.Close()
			entries, _ := os.ReadDir(dir)
			if len(entries) != 0 {
				t.Fatalf("temporary output retained: %v", entries)
			}
		})
	}
}

func TestListOutputFileRefusesDestinationRaces(t *testing.T) {
	for _, kind := range []string{"file", "directory", "symlink"} {
		for _, race := range []bool{false, true} {
			t.Run(fmt.Sprint(kind, "/", race), func(t *testing.T) {
				dir := t.TempDir()
				root, err := os.OpenRoot(dir)
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = root.Close() }()
				var file *main.ListOutputFile
				if race {
					file, err = main.NewListOutputFile(root, "rows.jsonl")
					if err != nil {
						t.Fatal(err)
					}
					defer func() { _ = file.Close() }()
					_, err = file.Write([]byte("private\n"))
					if err != nil {
						t.Fatal(err)
					}
				}
				path := filepath.Join(dir, "rows.jsonl")
				switch kind {
				case "file":
					err = os.WriteFile(path, []byte("keep"), 0600)
				case "directory":
					err = os.Mkdir(path, 0700)
				case "symlink":
					err = os.Symlink("untouched", path)
				}
				if err != nil {
					t.Fatal(err)
				}
				if race {
					_, err = file.Publish(context.Background(), main.ListStreamProgress{PagesCompleted: 1, ItemsEmitted: 1})
				} else {
					_, err = main.NewListOutputFile(root, "rows.jsonl")
				}
				if err == nil {
					t.Fatal("existing destination adopted or overwritten")
				}
				if kind == "file" {
					b, _ := os.ReadFile(path)
					if string(b) != "keep" {
						t.Fatalf("overwritten: %q", b)
					}
				}
				if kind == "symlink" {
					target, e := os.Readlink(path)
					if e != nil || target != "untouched" {
						t.Fatalf("symlink changed: %s %v", target, e)
					}
				}
			})
		}
	}
}

func TestListOutputFileTraversalFailureCleansPrivateBytes(t *testing.T) {
	for _, mode := range []string{"fetch", "encoding", "cycle"} {
		t.Run(mode, func(t *testing.T) {
			dir, file := newListFileTest(t)
			calls := 0
			_, err := main.StreamListPages(context.Background(), file, "", func(_ context.Context, cursor string) ([]float64, string, error) {
				calls++
				if calls == 1 {
					return []float64{1}, "next", nil
				}
				if mode == "fetch" {
					return nil, "", errors.New("read failed")
				}
				if mode == "encoding" {
					return []float64{math.NaN()}, "", nil
				}
				return []float64{2}, cursor, nil
			})
			if err == nil || calls != 2 {
				t.Fatalf("failure not propagated: %v calls=%d", err, calls)
			}
			assertListDestinationAbsent(t, dir)
			if err := file.Close(); err != nil {
				t.Fatal(err)
			}
			entries, _ := os.ReadDir(dir)
			if len(entries) != 0 {
				t.Fatalf("private partial retained: %v", entries)
			}
		})
	}
}
