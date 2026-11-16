package main_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

func exportDiffIndexFor(t *testing.T, source, key, label string) map[string]main.ExportDiffEntry {
	t.Helper()
	index, err := main.ReadExportDiffIndex(strings.NewReader(source), key, label)
	if err != nil {
		t.Fatal(err)
	}
	return index
}

func TestExportDiffSemanticComparison(t *testing.T) {
	before := `{"id":"same","nested":{"b":[1,true,null],"a":"x"},"n":1e999999999999999999999999}` + "\n" +
		`{"id":"changed","n":9007199254740993}` + "\n" + `{"id":"gone","value":"private-before"}` + "\n"
	after := `{"value":"private-after","id":"new"}` + "\n" + `{"n":9007199254740994,"id":"changed"}` + "\n" +
		`{"n":10e999999999999999999999998,"nested":{"a":"\u0078","b":[1.0,true,null]},"\u0069d":"same"}` + "\n"
	old, current := exportDiffIndexFor(t, before, "id", "before"), exportDiffIndexFor(t, after, "id", "after")
	report := main.CompareExportDiffIndexes(old, current, "id")
	if !report.HasChanges || report.SchemaVersion != 1 || report.Key != "id" || report.Summary.Before != 3 || report.Summary.After != 3 || report.Summary.Added != 1 || report.Summary.Removed != 1 || report.Summary.Changed != 1 || report.Summary.Unchanged != 1 {
		t.Fatalf("%+v", report)
	}
	if string(report.Added[0]) != `"new"` || string(report.Removed[0]) != `"gone"` || string(report.Changed[0]) != `"changed"` {
		t.Fatalf("%+v", report)
	}
	var output bytes.Buffer
	if err := main.WriteExportDiff(&output, report, true); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), "private-") || strings.Contains(output.String(), "900719925474099") {
		t.Fatal("row body leaked")
	}
	var parsed map[string]json.RawMessage
	if json.Unmarshal(output.Bytes(), &parsed) != nil || len(parsed) != 7 || string(parsed["has_changes"]) != "true" {
		t.Fatal(output.String())
	}
	// Reordering rows gives identical bytes, not only equivalent counts.
	reordered := strings.Join([]string{strings.Split(after, "\n")[2], strings.Split(after, "\n")[0], strings.Split(after, "\n")[1]}, "\n")
	other := main.CompareExportDiffIndexes(old, exportDiffIndexFor(t, reordered, "id", "after"), "id")
	var again bytes.Buffer
	if err := main.WriteExportDiff(&again, other, true); err != nil || again.String() != output.String() {
		t.Fatalf("order changed: %s %v", again.String(), err)
	}
}

func TestExportDiffExactNumbersAndArrayOrder(t *testing.T) {
	for _, tt := range []struct {
		name, left, right string
		changed           bool
	}{
		{"large-integer", "9007199254740993", "9007199254740994", true},
		{"decimal-equality", "100.00", "1e2", false},
		{"negative-zero", "-0.000e999999999999999999999999999", "0", false},
		{"tiny", "1e-9999999999999999999999999", "10e-10000000000000000000000000", false},
		{"fraction", "0.000000000000000000000000001", "1e-27", false},
		{"negative", "-12.30", "-123e-1", false},
		{"precise-fraction", "1.0000000000000000000001", "1.0000000000000000000002", true},
		{"number-vs-string", "1", `"1"`, true},
		{"array-order", "[1,2]", "[2,1]", true},
		{"nested-order", `{"a":1,"b":{"c":2,"d":3}}`, `{"b":{"d":3,"c":2},"a":1}`, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			before := exportDiffIndexFor(t, `{"id":"row","value":`+tt.left+"}\n", "id", "before")
			after := exportDiffIndexFor(t, `{"value":`+tt.right+`,"id":"row"}`+"\n", "id", "after")
			report := main.CompareExportDiffIndexes(before, after, "id")
			if report.HasChanges != tt.changed || (report.Summary.Changed == 1) != tt.changed {
				t.Fatalf("%+v", report)
			}
		})
	}
}

func TestExportDiffKeysEmptyAndLargeRecords(t *testing.T) {
	for _, data := range []string{"", `{"key":9007199254740993,"data":"` + strings.Repeat("x", 128*1024) + `"}` + "\r\n", `{"key":"\ud83d\ude00"}`} {
		index := exportDiffIndexFor(t, data, "key", "before")
		report := main.CompareExportDiffIndexes(index, index, "key")
		if report.HasChanges || report.Added == nil || report.Removed == nil || report.Changed == nil || report.Summary.Unchanged != len(index) {
			t.Fatalf("%+v", report)
		}
		var b bytes.Buffer
		if err := main.WriteExportDiff(&b, report, true); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(b.String(), `"added":[]`) || !strings.Contains(b.String(), `"has_changes":false`) {
			t.Fatal(b.String())
		}
	}
	index := exportDiffIndexFor(t, "{\"id\":\"1\"}\n{\"id\":1}\n", "id", "before")
	if len(index) != 2 {
		t.Fatal("string and numeric keys collapsed")
	}
	report := main.CompareExportDiffIndexes(map[string]main.ExportDiffEntry{}, index, "id")
	if len(report.Added) != 2 || string(report.Added[0]) != "1e0" || string(report.Added[1]) != `"1"` {
		t.Fatalf("%+v", report)
	}
	left := exportDiffIndexFor(t, `{"id":1,"value":2}`, "id", "before")
	right := exportDiffIndexFor(t, `{"id":1.0,"value":2.00}`, "id", "after")
	if got := main.CompareExportDiffIndexes(left, right, "id"); got.HasChanges {
		t.Fatal(got)
	}
}

func TestExportDiffRejectsAmbiguousAndInvalidRecords(t *testing.T) {
	for _, input := range []string{
		"\n", "{}", "null", "[]", `{"id":""}`, `{"id":null}`, `{"id":true}`, `{"id":[]}`, `{"id":{}}`,
		`{"id":"credential-secret","id":"other"}`, `{"id":"x","nested":{"p":1,"p":2}}`,
		`{"id":"credential-secret"}` + "\n" + `{"id":"credential-secret"}`,
		`{"id":1}` + "\n" + `{"id":1.00}`,
		`{"id":"x","value":"\ud800"}`, `{"id":"\udc00"}`, `{"id":"\ud800\u0061"}`,
		"{\"id\":\"\xff\"}", "{\"id\":credential-secret}", "{} {}\n",
	} {
		_, err := main.ReadExportDiffIndex(strings.NewReader(input), "id", "after")
		if err == nil || !strings.Contains(err.Error(), "after export line ") || strings.Contains(err.Error(), "credential-secret") {
			t.Fatalf("error=%v", err)
		}
	}
	_, err := main.ReadExportDiffIndex(strings.NewReader("{\"id\":\"first\"}\n\n"), "id", "before")
	if err == nil || !strings.Contains(err.Error(), "line 2") {
		t.Fatal(err)
	}
	cause := errors.New("read failed")
	_, err = main.ReadExportDiffIndex(io.MultiReader(strings.NewReader("{\"id\":1}\n"), exportDiffFailReader{cause}), "id", "after")
	if !errors.Is(err, cause) {
		t.Fatal(err)
	}
}

type exportDiffFailReader struct{ err error }

func (r exportDiffFailReader) Read([]byte) (int, error) { return 0, r.err }

type exportDiffShortWriter struct{}

func (exportDiffShortWriter) Write(p []byte) (int, error) { return len(p) - 1, nil }

type exportDiffFailWriter struct{}

func (exportDiffFailWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestExportDiffReadOnlyFilesAndOutput(t *testing.T) {
	dir := t.TempDir()
	beforePath, afterPath := filepath.Join(dir, "before.jsonl"), filepath.Join(dir, "after.jsonl")
	before, after := []byte("{\"id\":\"line\\nquoted\\\"\",\"value\":1}\n"), []byte("{\"id\":\"added\",\"value\":2}\n")
	for path, data := range map[string][]byte{beforePath: before, afterPath: after} {
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	report, err := main.DiffExportFiles(beforePath, afterPath, "id")
	if err != nil || report.Summary.Added != 1 || report.Summary.Removed != 1 {
		t.Fatalf("%+v %v", report, err)
	}
	var human bytes.Buffer
	if err := main.WriteExportDiff(&human, report, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(human.String(), `Removed: "line\nquoted\""`) || strings.Contains(human.String(), "line\nquoted") {
		t.Fatal(human.String())
	}
	for _, mode := range []bool{false, true} {
		if err := main.WriteExportDiff(exportDiffShortWriter{}, report, mode); !errors.Is(err, io.ErrShortWrite) {
			t.Fatal(err)
		}
		if err := main.WriteExportDiff(exportDiffFailWriter{}, report, mode); !errors.Is(err, io.ErrClosedPipe) {
			t.Fatal(err)
		}
	}
	for path, want := range map[string][]byte{beforePath: before, afterPath: after} {
		got, err := os.ReadFile(path)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatal("input modified", err)
		}
	}
	if _, err := main.DiffExportFiles(dir, afterPath, "id"); err == nil {
		t.Fatal("directory accepted")
	}
	if err := os.WriteFile(afterPath, []byte("invalid-credential-secret"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := main.DiffExportFiles(beforePath, afterPath, "id"); err == nil || strings.Contains(err.Error(), "credential-secret") {
		t.Fatal(err)
	}
}
