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

func exportDiffExplainFor(t *testing.T, before, after string, explain bool) main.ExportDiffReport {
	t.Helper()
	directory := t.TempDir()
	beforePath, afterPath := filepath.Join(directory, "before.jsonl"), filepath.Join(directory, "after.jsonl")
	for path, data := range map[string]string{beforePath: before, afterPath: after} {
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	report, err := main.DiffExportFilesMode(beforePath, afterPath, "id", explain)
	if err != nil {
		t.Fatal(err)
	}
	for path, data := range map[string]string{beforePath: before, afterPath: after} {
		actual, err := os.ReadFile(path)
		if err != nil || string(actual) != data {
			t.Fatal("input modified", err)
		}
	}
	return report
}

func TestExportDiffExplainsObjectFields(t *testing.T) {
	before := `{"id":"row","settings":{"region":"before-secret","old":{"private":"secret"},"empty":{}},"array":[{"private":"before-secret"}],"node":{"child":1},"keep":1}`
	after := `{"keep":1.0,"node":null,"array":[{"private":"after-secret"}],"settings":{"empty":{"enabled":true},"new":{"private":"secret"},"region":"after-secret"},"id":"row"}`
	report := exportDiffExplainFor(t, before, after, true)
	want := `[{"id":"row","changes":[{"path":"/array","kind":"changed"},{"path":"/node","kind":"changed"},{"path":"/settings/empty/enabled","kind":"added"},{"path":"/settings/new","kind":"added"},{"path":"/settings/old","kind":"removed"},{"path":"/settings/region","kind":"changed"}]}]`
	got, err := json.Marshal(report.Explanations)
	if err != nil || string(got) != want || report.Summary.Changed != 1 || len(report.Changed) != 1 {
		t.Fatalf("report=%+v explanations=%s err=%v", report, got, err)
	}
	for _, jsonMode := range []bool{false, true} {
		var out bytes.Buffer
		if err := main.WriteExportDiff(&out, report, jsonMode); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(out.String(), "secret") || strings.Contains(out.String(), "private") || strings.Contains(out.String(), "/array/0") || strings.Contains(out.String(), "/node/child") {
			t.Fatal("value, array member, or replaced subtree was exposed", out.String())
		}
	}
}

func TestExportDiffExplainPointersAndAtomicValues(t *testing.T) {
	for _, tt := range []struct{ name, before, after, changes string }{
		{"escaped-names", `{"":1,"a/b":{"~x":1},"literal.dot":1,"line\nnext":1}`, `{"":2,"a/b":{"~x":2},"literal.dot":2,"line\nnext":2}`, `[{"path":"/","kind":"changed"},{"path":"/a~1b/~0x","kind":"changed"},{"path":"/line\nnext","kind":"changed"},{"path":"/literal.dot","kind":"changed"}]`},
		{"slash-versus-nesting", `{"a/b":1,"a":{"b":1}}`, `{"a/b":2,"a":{"b":2}}`, `[{"path":"/a/b","kind":"changed"},{"path":"/a~1b","kind":"changed"}]`},
		{"empty-vs-missing", `{"node":{}}`, `{}`, `[{"path":"/node","kind":"removed"}]`},
		{"null-vs-missing", `{}`, `{"node":null}`, `[{"path":"/node","kind":"added"}]`},
		{"empty-vs-scalar", `{"node":{}}`, `{"node":false}`, `[{"path":"/node","kind":"changed"}]`},
		{"array-order", `{"node":[1,2]}`, `{"node":[2,1]}`, `[{"path":"/node","kind":"changed"}]`},
		{"array-nested-values", `{"node":[{"a":1}]}`, `{"node":[{"a":2}]}`, `[{"path":"/node","kind":"changed"}]`},
		{"large-integer", `{"node":9007199254740993}`, `{"node":9007199254740994}`, `[{"path":"/node","kind":"changed"}]`},
		{"precise-fraction", `{"node":1.000000000000000000001}`, `{"node":1.000000000000000000002}`, `[{"path":"/node","kind":"changed"}]`},
		{"semantic-equality", `{"node":{"a":[1,-0],"b":1e99999999999999999999}}`, `{"node":{"b":10e99999999999999999998,"a":[1.00,0.0]}}`, `[]`},
		{"unicode", `{"\ud83d\ude00":{"a":1}}`, `{"😀":{"\u0061":2}}`, `[{"path":"/😀/a","kind":"changed"}]`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			before := strings.TrimSuffix(tt.before, "}") + `,"id":"row"}`
			after := strings.TrimSuffix(tt.after, "}") + `,"id":"row"}`
			// Empty object fixtures need no leading comma before their ID.
			before = strings.Replace(before, "{,", "{", 1)
			after = strings.Replace(after, "{,", "{", 1)
			report := exportDiffExplainFor(t, before, after, true)
			if report.Explanations == nil {
				t.Fatal("explanations omitted")
			}
			if tt.changes == "[]" {
				if len(*report.Explanations) != 0 || report.HasChanges {
					t.Fatal(report)
				}
				return
			}
			if len(*report.Explanations) != 1 {
				t.Fatal(report)
			}
			actual, err := json.Marshal((*report.Explanations)[0].Changes)
			if err != nil || string(actual) != tt.changes {
				t.Fatalf("changes=%s err=%v", actual, err)
			}
		})
	}
}

func TestExportDiffExplainOptInOrderingAndEmpty(t *testing.T) {
	before := "{\"id\":\"1\",\"value\":1}\n{\"id\":1,\"value\":1}\n{\"id\":\"gone\",\"private\":1}\n"
	after := "{\"value\":2,\"id\":1.0}\n{\"value\":2,\"id\":\"1\"}\n{\"id\":\"new\",\"private\":2}\n"
	plain := exportDiffExplainFor(t, before, after, false)
	report := exportDiffExplainFor(t, before, after, true)
	if report.Explanations == nil || len(*report.Explanations) != 2 || string((*report.Explanations)[0].ID) != "1e0" || string((*report.Explanations)[1].ID) != `"1"` {
		t.Fatal(report)
	}
	var explained, ordinary bytes.Buffer
	if err := main.WriteExportDiff(&explained, report, true); err != nil {
		t.Fatal(err)
	}
	if err := main.WriteExportDiff(&ordinary, plain, true); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(ordinary.String(), "explanations") || plain.Explanations != nil {
		t.Fatal(ordinary.String())
	}
	report.Explanations = nil
	var unchanged bytes.Buffer
	if err := main.WriteExportDiff(&unchanged, report, true); err != nil || unchanged.String() != ordinary.String() {
		t.Fatal("existing JSON changed", err)
	}
	a, b := main.ExportDiffReport{}, main.ExportDiffReport{}
	if json.Unmarshal(explained.Bytes(), &a) != nil || json.Unmarshal(ordinary.Bytes(), &b) != nil {
		t.Fatal("invalid report")
	}
	a.Explanations = nil
	if !reflect.DeepEqual(a, b) {
		t.Fatal("explanation changed comparison")
	}
	lines := strings.Split(strings.TrimSuffix(after, "\n"), "\n")
	reordered := exportDiffExplainFor(t, before, strings.Join([]string{lines[2], lines[1], lines[0]}, "\n"), true)
	var again bytes.Buffer
	if err := main.WriteExportDiff(&again, reordered, true); err != nil || again.String() != explained.String() {
		t.Fatal("ordering changed", again.String(), err)
	}
	for _, inventory := range []string{"", `{"id":"same","value":"` + strings.Repeat("x", 128*1024) + `"}` + "\r\n"} {
		empty := exportDiffExplainFor(t, inventory, inventory, true)
		encoded, err := json.Marshal(empty)
		if err != nil || empty.HasChanges || !strings.Contains(string(encoded), `"explanations":[]`) {
			t.Fatal(string(encoded), err)
		}
	}
}

func TestExportDiffExplainHumanEscapingAndOutputErrors(t *testing.T) {
	report := exportDiffExplainFor(t, `{"id":"line\nID","line\nfield":1}`, `{"id":"line\nID","line\nfield":2}`, true)
	var human bytes.Buffer
	if err := main.WriteExportDiff(&human, report, false); err != nil {
		t.Fatal(err)
	}
	want := "Export diff: added=0 removed=0 changed=1 unchanged=0\nChanged: \"line\\nID\"\nFields for \"line\\nID\":\n  changed: \"/line\\nfield\"\n"
	if human.String() != want {
		t.Fatal(human.String())
	}
	for _, jsonMode := range []bool{false, true} {
		if err := main.WriteExportDiff(exportDiffShortWriter{}, report, jsonMode); !errors.Is(err, io.ErrShortWrite) {
			t.Fatal(err)
		}
		if err := main.WriteExportDiff(exportDiffFailWriter{}, report, jsonMode); !errors.Is(err, io.ErrClosedPipe) {
			t.Fatal(err)
		}
	}
	for _, source := range []string{`{"id":"secret","nested":{"a":1,"a":2}}`, `{"id":1}` + "\n" + `{"id":1.0}`, `{"id":"row","s":"\ud800"}`, "\n"} {
		if _, err := main.ReadExportDiffIndexMode(strings.NewReader(source), "id", "after", true); err == nil || strings.Contains(err.Error(), "secret") {
			t.Fatal(err)
		}
	}
	cause := errors.New("read failure")
	if _, err := main.ReadExportDiffIndexMode(io.MultiReader(strings.NewReader("{\"id\":1}\n"), exportDiffFailReader{cause}), "id", "after", true); !errors.Is(err, cause) {
		t.Fatal(err)
	}
}
