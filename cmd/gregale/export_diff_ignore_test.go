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

func exportDiffIgnoringFor(t *testing.T, before, after, key string, explain bool, paths []string) main.ExportDiffReport {
	t.Helper()
	directory := t.TempDir()
	left, right := filepath.Join(directory, "before.jsonl"), filepath.Join(directory, "after.jsonl")
	for path, data := range map[string]string{left: before, right: after} {
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	result, err := main.DiffExportFilesIgnoring(left, right, key, explain, paths)
	if err != nil {
		t.Fatal(err)
	}
	for path, data := range map[string]string{left: before, right: after} {
		got, err := os.ReadFile(path)
		if err != nil || string(got) != data {
			t.Fatal("input modified", err)
		}
	}
	return result
}

func TestExportDiffIgnoreComparisonAndExplanations(t *testing.T) {
	before := `{"id":"row","updated_at":"before-secret","settings":{"region":"eu","counter":1}}`
	after := `{"settings":{"counter":99,"region":"us"},"updated_at":"after-secret","id":"row"}`
	paths := []string{"/updated_at", "/settings/counter", "/updated_at"}
	report := exportDiffIgnoringFor(t, before, after, "id", true, paths)
	if !report.HasChanges || report.Summary.Changed != 1 || report.Explanations == nil || len(*report.Explanations) != 1 {
		t.Fatal(report)
	}
	encoded, err := json.Marshal(report.Explanations)
	if err != nil || string(encoded) != `[{"id":"row","changes":[{"path":"/settings/region","kind":"changed"}]}]` {
		t.Fatal(string(encoded), err)
	}
	if !reflect.DeepEqual(report.IgnoredPaths, []string{"/settings/counter", "/updated_at"}) {
		t.Fatal(report.IgnoredPaths)
	}
	paths[0] = "/changed-by-caller"
	if report.IgnoredPaths[1] != "/updated_at" {
		t.Fatal("report borrowed caller path slice")
	}
	for _, jsonMode := range []bool{false, true} {
		var out bytes.Buffer
		if err := main.WriteExportDiff(&out, report, jsonMode); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(out.String(), "secret") || strings.Contains(out.String(), "99") || strings.Contains(out.String(), `"eu"`) {
			t.Fatal("field value leaked", out.String())
		}
		if !jsonMode && !strings.Contains(out.String(), "Ignored paths: [\"/settings/counter\",\"/updated_at\"]\n") {
			t.Fatal(out.String())
		}
	}
	plain := exportDiffIgnoringFor(t, before, after, "id", false, []string{"/settings", "/updated_at"})
	if plain.HasChanges || plain.Summary.Unchanged != 1 || plain.Explanations != nil {
		t.Fatal(plain)
	}
	explained := exportDiffIgnoringFor(t, before, after, "id", true, []string{"/settings", "/updated_at"})
	actual, _ := json.Marshal(explained)
	if explained.HasChanges || !strings.Contains(string(actual), `"explanations":[]`) {
		t.Fatal(string(actual))
	}
}

func TestExportDiffIgnoreProjectionBoundaries(t *testing.T) {
	for _, tt := range []struct {
		name, before, after string
		paths               []string
		changed             bool
		wantPath            string
	}{
		{"field-presence", `{"id":1,"volatile":null}`, `{"id":1}`, []string{"/volatile"}, false, ""},
		{"subtree", `{"id":1,"meta":{"a":1}}`, `{"id":1}`, []string{"/meta"}, false, ""},
		{"retain-container", `{"id":1,"meta":{"time":1}}`, `{"id":1}`, []string{"/meta/time"}, true, "/meta"},
		{"null-container", `{"id":1,"meta":null}`, `{"id":1,"meta":{"time":1}}`, []string{"/meta/time"}, true, "/meta"},
		{"missing-or-scalar-path", `{"id":1,"meta":1}`, `{"id":1,"meta":2}`, []string{"/meta/time"}, true, "/meta"},
		{"empty-and-escaped", `{"id":1,"":1,"a/b":{"~x":1}}`, `{"id":1,"":2,"a/b":{"~x":2}}`, []string{"/", "/a~1b/~0x"}, false, ""},
		{"escape-order", `{"id":1,"a~1b":1,"a/b":1}`, `{"id":1,"a~1b":2,"a/b":1}`, []string{"/a~01b"}, false, ""},
		{"literal-wildcard", `{"id":1,"*":1,"keep":1}`, `{"id":1,"*":2,"keep":2}`, []string{"/*"}, true, "/keep"},
		{"literal-dot", `{"id":1,"a.b":1,"a":{"b":1}}`, `{"id":1,"a.b":2,"a":{"b":2}}`, []string{"/a.b"}, true, "/a/b"},
		{"array-whole-field", `{"id":1,"items":[1,2]}`, `{"id":1,"items":[2,1]}`, []string{"/items"}, false, ""},
		{"object-digit", `{"id":1,"items":{"0":1}}`, `{"id":1,"items":{"0":2}}`, []string{"/items/0"}, false, ""},
		{"overlap", `{"id":1,"items":[1]}`, `{"id":1,"items":[2]}`, []string{"/items/0", "/items"}, false, ""},
		{"exact-number", `{"id":1,"n":9007199254740993,"v":1}`, `{"id":1.0,"n":9007199254740994,"v":2}`, []string{"/v"}, true, "/n"},
		{"exact-semantic-equality", `{"id":1,"n":1e99999999999999999999,"v":1}`, `{"id":1.0,"v":2,"n":10e99999999999999999998}`, []string{"/v"}, false, ""},
		{"unicode-and-controls", `{"id":1,"😀":{"line\nname":1}}`, `{"id":1,"\ud83d\ude00":{"line\nname":2}}`, []string{"/😀/line\nname"}, false, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			report := exportDiffIgnoringFor(t, tt.before, tt.after, "id", true, tt.paths)
			if report.HasChanges != tt.changed || report.Explanations == nil {
				t.Fatal(report)
			}
			if tt.changed {
				if len(*report.Explanations) != 1 || len((*report.Explanations)[0].Changes) != 1 || (*report.Explanations)[0].Changes[0].Path != tt.wantPath {
					t.Fatal(report.Explanations)
				}
			} else if len(*report.Explanations) != 0 {
				t.Fatal(report.Explanations)
			}
		})
	}
}

func TestExportDiffIgnorePreservesIdentityAndOrdering(t *testing.T) {
	before := "{\"key.id\":1,\"id\":\"old\",\"v\":1}\n{\"key.id\":\"1\",\"id\":\"old\",\"v\":1}\n{\"key.id\":\"gone\",\"v\":1}\n"
	after := "{\"key.id\":\"new\",\"v\":2}\n{\"key.id\":\"1\",\"id\":\"new\",\"v\":2}\n{\"v\":2,\"key.id\":1.00,\"id\":\"new\"}\n"
	paths := []string{"/v", "/id"}
	report := exportDiffIgnoringFor(t, before, after, "key.id", true, paths)
	if report.Summary.Added != 1 || report.Summary.Removed != 1 || report.Summary.Changed != 0 || report.Summary.Unchanged != 2 || !report.HasChanges || len(*report.Explanations) != 0 {
		t.Fatal(report)
	}
	if string(report.Added[0]) != `"new"` || string(report.Removed[0]) != `"gone"` {
		t.Fatal(report)
	}
	lines := strings.Split(strings.TrimSuffix(after, "\n"), "\n")
	again := exportDiffIgnoringFor(t, before, strings.Join([]string{lines[2], lines[0], lines[1]}, "\n"), "key.id", true, []string{"/id", "/v", "/id"})
	a, b := new(bytes.Buffer), new(bytes.Buffer)
	if err := main.WriteExportDiff(a, report, true); err != nil {
		t.Fatal(err)
	}
	if err := main.WriteExportDiff(b, again, true); err != nil {
		t.Fatal(err)
	}
	if a.String() != b.String() {
		t.Fatal("row or exclusion order changed output", a.String(), b.String())
	}
	empty := exportDiffIgnoringFor(t, "", "", "id", true, []string{"/unused"})
	if empty.HasChanges || !reflect.DeepEqual(empty.IgnoredPaths, []string{"/unused"}) || len(*empty.Explanations) != 0 {
		t.Fatal(empty)
	}
}

func TestExportDiffIgnoreRejectsInvalidPoliciesAndInput(t *testing.T) {
	for _, tt := range []struct{ key, path string }{
		{"id", ""}, {"id", "updated_at"}, {"id", "#/time"}, {"id", "/bad~"}, {"id", "/bad~2escape"}, {"id", "/\xff"},
		{"id", "/id"}, {"id", "/id/child"}, {"literal.id", "/literal.id"}, {"a/b", "/a~1b"}, {"~id", "/~0id"},
	} {
		_, err := main.DiffExportFilesIgnoring("missing-before", "missing-after", tt.key, false, []string{tt.path})
		if err == nil || strings.Contains(err.Error(), "missing-before") || strings.Contains(err.Error(), "bad~") {
			t.Fatal(err)
		}
	}
	for _, source := range []string{
		`{"id":1,"ignored":{"a":1,"a":2}}`, `{"id":1,"ignored":"\ud800"}`, `{"id":1,"ignored":credential-secret}`,
		"{\"id\":1,\"ignored\":1}\n{\"id\":1.0,\"ignored\":2}",
	} {
		options, err := main.NewExportDiffReadOptions("id", false, []string{"/ignored"})
		if err != nil {
			t.Fatal(err)
		}
		_, err = main.ReadExportDiffIndexOptions(strings.NewReader(source), "id", "after", options)
		if err == nil || !strings.Contains(err.Error(), "after export line") || strings.Contains(err.Error(), "credential-secret") {
			t.Fatal(err)
		}
	}
	options, err := main.NewExportDiffReadOptions("id", false, []string{"/items/0"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = main.ReadExportDiffIndexOptions(strings.NewReader(`{"id":1,"items":["secret"]}`), "id", "before", options); err == nil || strings.Contains(err.Error(), "secret") || !strings.Contains(err.Error(), "before export line 1") {
		t.Fatal(err)
	}
	cause := errors.New("read failure")
	if _, err = main.ReadExportDiffIndexOptions(io.MultiReader(strings.NewReader("{\"id\":1}\n"), exportDiffFailReader{cause}), "id", "after", options); !errors.Is(err, cause) {
		t.Fatal(err)
	}
}

func TestExportDiffIgnoreOmissionAndOutputErrors(t *testing.T) {
	before, after := `{"id":"row","a":1}`, `{"id":"row","a":2}`
	for _, explain := range []bool{false, true} {
		old := exportDiffExplainFor(t, before, after, explain)
		ordinary := exportDiffIgnoringFor(t, before, after, "id", explain, nil)
		for _, jsonMode := range []bool{false, true} {
			a, b := new(bytes.Buffer), new(bytes.Buffer)
			if err := main.WriteExportDiff(a, old, jsonMode); err != nil {
				t.Fatal(err)
			}
			if err := main.WriteExportDiff(b, ordinary, jsonMode); err != nil {
				t.Fatal(err)
			}
			if a.String() != b.String() || strings.Contains(b.String(), "ignored_paths") || strings.Contains(b.String(), "Ignored paths") {
				t.Fatal("omission changed output", b.String())
			}
		}
	}
	report := exportDiffIgnoringFor(t, before, after, "id", true, []string{"/line\nname"})
	var out bytes.Buffer
	if err := main.WriteExportDiff(&out, report, false); err != nil || !strings.Contains(out.String(), `Ignored paths: ["/line\nname"]`) {
		t.Fatal(out.String(), err)
	}
	for _, jsonMode := range []bool{false, true} {
		if err := main.WriteExportDiff(exportDiffShortWriter{}, report, jsonMode); !errors.Is(err, io.ErrShortWrite) {
			t.Fatal(err)
		}
		if err := main.WriteExportDiff(exportDiffFailWriter{}, report, jsonMode); !errors.Is(err, io.ErrClosedPipe) {
			t.Fatal(err)
		}
	}
}
