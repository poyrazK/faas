package main_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

func readExportProgress(t *testing.T, data []byte) []main.ListExportProgressEvent {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(data))
	events := []main.ListExportProgressEvent{}
	for {
		var event main.ListExportProgressEvent
		err := decoder.Decode(&event)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("bad progress: %s: %v", data, err)
		}
		events = append(events, event)
	}
	return events
}

func TestExportProgressModesThrottleAndTerminalEvents(t *testing.T) {
	for _, value := range []string{"off", "plain", "ndjson"} {
		if _, err := main.ParseListExportProgress(value); err != nil {
			t.Fatal(err)
		}
	}
	for _, value := range []string{"", "auto", "private-input", "NDJSON"} {
		_, err := main.ParseListExportProgress(value)
		if err == nil || strings.Contains(err.Error(), value) && value != "" {
			t.Fatalf("invalid mode retained raw input: %q %v", value, err)
		}
	}
	var output bytes.Buffer
	if reporter := main.NewListExportProgress(&output, "off", func() time.Time { t.Fatal("disabled progress read the clock"); return time.Time{} }); reporter != nil || output.Len() != 0 {
		t.Fatal("default progress emitted output")
	}
	now := time.Unix(0, 0)
	reporter := main.NewListExportProgress(&output, "ndjson", func() time.Time { return now })
	now = now.Add(249 * time.Millisecond)
	main.ObserveListExportProgress(reporter, main.ListStreamProgress{PagesCompleted: 1, ItemsEmitted: 2}, 8)
	now = now.Add(time.Millisecond)
	main.ObserveListExportProgress(reporter, main.ListStreamProgress{PagesCompleted: 2, ItemsEmitted: 3}, 12)
	now = now.Add(time.Millisecond)
	main.ObserveListExportProgress(reporter, main.ListStreamProgress{PagesCompleted: 3, ItemsEmitted: 4}, 16)
	main.FinishListExportProgress(reporter, true, main.ListStreamProgress{PagesCompleted: 3, ItemsEmitted: 4}, 16)
	main.FinishListExportProgress(reporter, false, main.ListStreamProgress{}, 0)
	main.ObserveListExportProgress(reporter, main.ListStreamProgress{}, 0)
	events := readExportProgress(t, output.Bytes())
	if len(events) != 3 || events[0].Event != "start" || events[1].Event != "update" || events[2].Event != "published" || !events[2].Published || events[1].PagesCompleted != 2 || events[2].PagesCompleted != 3 || events[2].ItemsEmitted != 4 || events[2].BytesWritten != 16 || events[2].ElapsedMS != 251 {
		t.Fatalf("throttle/final counters: %+v", events)
	}
	for _, event := range events {
		if event.Type != "list_export_progress" || event.SchemaVersion != 1 {
			t.Fatal(event)
		}
	}
	for _, field := range []string{`"published":false`, `"elapsed_ms":0`, `"pages_completed":0`, `"items_emitted":0`, `"bytes_written":0`} {
		if !bytes.Contains(output.Bytes(), []byte(field)) {
			t.Fatalf("meaningful zero omitted: %s", output.Bytes())
		}
	}
	var shape map[string]any
	line, _, _ := bytes.Cut(output.Bytes(), []byte("\n"))
	if json.Unmarshal(line, &shape) != nil || len(shape) != 8 {
		t.Fatalf("unexpected progress fields: %s", line)
	}
}

func TestExportProgressPlainAndStoppedClock(t *testing.T) {
	var plain bytes.Buffer
	now := time.Unix(0, 0)
	reporter := main.NewListExportProgress(&plain, "plain", func() time.Time { return now })
	now = now.Add(300 * time.Millisecond)
	main.ObserveListExportProgress(reporter, main.ListStreamProgress{PagesCompleted: 1, ItemsEmitted: 2}, 10)
	now = now.Add(-time.Second)
	main.FinishListExportProgress(reporter, false, main.ListStreamProgress{PagesCompleted: 1, ItemsEmitted: 3}, 13)
	want := "Export start: pages=0 items=0 bytes=0 elapsed=0ms\nExport update: pages=1 items=2 bytes=10 elapsed=300ms\nExport stopped: pages=1 items=3 bytes=13 elapsed=300ms\n"
	if plain.String() != want {
		t.Fatalf("plain progress: %q", plain.String())
	}
}

type exportProgressFaultWriter struct {
	calls int
	short bool
}

func (w *exportProgressFaultWriter) Write(data []byte) (int, error) {
	w.calls++
	if w.short {
		return len(data) - 1, nil
	}
	return 0, syscall.EPIPE
}

func TestExportProgressWriterFailureDoesNotAbortPublication(t *testing.T) {
	for _, short := range []bool{false, true} {
		dir := t.TempDir()
		root, err := os.OpenRoot(dir)
		if err != nil {
			t.Fatal(err)
		}
		defer root.Close()
		file, err := main.NewListOutputFile(root, "rows.jsonl")
		if err != nil {
			t.Fatal(err)
		}
		defer file.Close()
		writer := &exportProgressFaultWriter{short: short}
		now := time.Unix(0, 0)
		reporter := main.NewListExportProgress(writer, "ndjson", func() time.Time { return now })
		progress, err := main.StreamListPagesObserved(context.Background(), file, "", func(context.Context, string) ([]int, string, error) {
			now = now.Add(time.Second)
			return []int{1}, "", nil
		}, func(p main.ListStreamProgress) {
			main.ObserveListExportProgress(reporter, p, main.ListExportBytes(file))
		})
		if err != nil {
			t.Fatal(err)
		}
		receipt, err := file.Publish(context.Background(), progress)
		if err != nil {
			t.Fatal(err)
		}
		main.FinishListExportProgress(reporter, true, progress, receipt.BytesWritten)
		data, err := os.ReadFile(filepath.Join(dir, "rows.jsonl"))
		if err != nil || string(data) != "1\n" || !receipt.Published || writer.calls != 1 {
			t.Fatalf("progress failure changed export: %q %+v calls=%d %v", data, receipt, writer.calls, err)
		}
	}
}

func TestExportProgressObservesCompletePagesAndPrivateFailure(t *testing.T) {
	for _, failure := range []string{"", "read", "write"} {
		t.Run(failure, func(t *testing.T) {
			dir := t.TempDir()
			root, err := os.OpenRoot(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer root.Close()
			file, err := main.NewListOutputFile(root, "private-output.jsonl")
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			var output bytes.Buffer
			now := time.Unix(0, 0)
			reporter := main.NewListExportProgress(&output, "ndjson", func() time.Time { return now })
			if failure == "write" {
				main.FailListOutputFile(file, "write", nil)
			}
			actual := errors.New("private read-error value")
			calls := 0
			var seen []main.ListStreamProgress
			progress, err := main.StreamListPagesObserved(context.Background(), file, "private-cursor", func(_ context.Context, cursor string) ([]int, string, error) {
				calls++
				now = now.Add(250 * time.Millisecond)
				if calls == 1 {
					return []int{1, 2}, "private-next", nil
				}
				if cursor != "private-next" {
					t.Fatal(cursor)
				}
				if failure == "read" {
					return nil, "", actual
				}
				return []int{}, "", nil
			}, func(p main.ListStreamProgress) {
				seen = append(seen, p)
				main.ObserveListExportProgress(reporter, p, main.ListExportBytes(file))
			})
			if failure != "" {
				if err == nil || failure == "read" && !errors.Is(err, actual) {
					t.Fatal(err)
				}
				main.FinishListExportProgress(reporter, false, progress, main.ListExportBytes(file))
				if _, err := os.Lstat(filepath.Join(dir, "private-output.jsonl")); !os.IsNotExist(err) {
					t.Fatal(err)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				receipt, err := file.Publish(context.Background(), progress)
				if err != nil {
					t.Fatal(err)
				}
				main.FinishListExportProgress(reporter, true, progress, receipt.BytesWritten)
			}
			events := readExportProgress(t, output.Bytes())
			last := events[len(events)-1]
			if strings.Contains(output.String(), "private") || last.PagesCompleted != progress.PagesCompleted || last.ItemsEmitted != progress.ItemsEmitted || last.BytesWritten != main.ListExportBytes(file) || last.Published != (failure == "") {
				t.Fatalf("unsafe/inaccurate progress: %s", output.String())
			}
			wantPages := 2
			if failure == "read" {
				wantPages = 1
			}
			if failure == "write" {
				wantPages = 0
			}
			if len(seen) != wantPages {
				t.Fatalf("observer called before complete page: %+v", seen)
			}
			if failure == "" && !reflect.DeepEqual(seen, []main.ListStreamProgress{{PagesCompleted: 1, ItemsEmitted: 2}, {PagesCompleted: 2, ItemsEmitted: 2}}) {
				t.Fatal(seen)
			}
		})
	}
}

func TestExportProgressDiscoveryAndHiddenOption(t *testing.T) {
	for _, visible := range []bool{false, true} {
		flags := []string{"all", "stream", "output"}
		if visible {
			flags = append(flags, "progress")
		}
		contract := main.NewCLIOutputContract(main.CLIOutputBuildList, flags)
		for _, mode := range contract.Modes {
			if mode.File == nil || !visible {
				if mode.Progress != nil {
					t.Fatal("hidden/non-file progress advertised")
				}
				continue
			}
			if mode.Progress == nil || mode.Progress.Flag != "progress" || mode.Progress.Stream != "stderr" || mode.Progress.Default != "off" || mode.Progress.NDJSONType != "list_export_progress" || mode.Progress.SchemaVersion != 1 || mode.Progress.MinUpdateIntervalMS != 250 || !mode.Progress.BestEffort || !reflect.DeepEqual(mode.Progress.Events, []string{"start", "update", "stopped", "published"}) {
				t.Fatalf("progress discovery drift: %+v", mode.Progress)
			}
		}
	}
}
