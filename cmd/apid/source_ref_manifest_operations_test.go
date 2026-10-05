// ADR-521: deploy bundles only bounded schemas from the selected source archive.
package main

import (
	"archive/tar"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
)

type operationSchemaEntry struct {
	name, body string
	kind       byte
}

func operationSchemaArchive(t *testing.T, entries []operationSchemaEntry) string {
	t.Helper()
	filename := filepath.Join(t.TempDir(), "source.tar.gz")
	f, err := os.Create(filename)
	if err != nil {
		t.Fatal(err)
	}
	zip := gzip.NewWriter(f)
	archive := tar.NewWriter(zip)
	for _, entry := range entries {
		size := int64(len(entry.body))
		if entry.kind == tar.TypeSymlink {
			size = 0
		}
		if err := archive.WriteHeader(&tar.Header{Name: entry.name, Typeflag: entry.kind, Mode: 0600, Size: size, Linkname: "input.json"}); err != nil {
			t.Fatal(err)
		}
		if size > 0 {
			if _, err := archive.Write([]byte(entry.body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, close := range []func() error{archive.Close, zip.Close, f.Close} {
		if err := close(); err != nil {
			t.Fatal(err)
		}
	}
	return filename
}

func TestOperationSourceSchemaArchiveBoundary(t *testing.T) {
	valid := []operationSchemaEntry{{"apps/export/input.json", `{"type":"object"}`, tar.TypeReg}, {"./apps/export/output.json", `true`, tar.TypeReg}}
	cases := []struct {
		name               string
		extra              []operationSchemaEntry
		removeOutput, fail bool
	}{
		{name: "selected manifest and unrelated app", extra: []operationSchemaEntry{{"other/input.json", strings.Repeat("x", api.MustLimitsFor(api.PlanPro).Operations.SchemaBytes+1), tar.TypeReg}}},
		{name: "duplicate alias", extra: []operationSchemaEntry{{"./apps/export/input.json", `true`, tar.TypeReg}}, fail: true},
		{name: "symlink", extra: []operationSchemaEntry{{"apps/export/output.json", "", tar.TypeSymlink}}, removeOutput: true, fail: true},
		{name: "oversized", extra: []operationSchemaEntry{{"apps/export/output.json", strings.Repeat(" ", api.MustLimitsFor(api.PlanPro).Operations.SchemaBytes+1), tar.TypeReg}}, removeOutput: true, fail: true},
		{name: "missing", removeOutput: true, fail: true},
		{name: "invalid JSON schema", extra: []operationSchemaEntry{{"apps/export/output.json", "invalid", tar.TypeReg}}, removeOutput: true, fail: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			entries := append([]operationSchemaEntry(nil), valid...)
			if tc.removeOutput {
				entries = entries[:1]
			}
			entries = append(entries, tc.extra...)
			m := &gregalemanifest.Manifest{Operations: []gregalemanifest.Operation{
				{Name: "export", Method: "POST", Path: "/exports", Owner: api.OperationOwnerPlatformTenant, InputSchema: "input.json", OutputSchema: "output.json", ProgressStages: []string{"generating"}},
				{App: "other", Name: "other", Method: "POST", Path: "/other", Owner: api.OperationOwnerPlatformTenant, InputSchema: "missing.json", OutputSchema: "also-missing.json", ProgressStages: []string{"generating"}},
			}}
			err := resolveSourceOperations(operationSchemaArchive(t, entries), "apps/export/gregale.yaml", "exports", api.PlanPro, m)
			if (err != nil) != tc.fail {
				t.Fatalf("error=%v, want failure %v", err, tc.fail)
			}
			if tc.fail && len(m.ResolvedOperations) != 0 {
				t.Fatal("partially resolved bundle published")
			}
			if !tc.fail && (len(m.ResolvedOperations) != 1 || string(m.ResolvedOperations[0].OutputSchema) != "true") {
				t.Fatalf("selected bundle: %+v", m.ResolvedOperations)
			}
		})
	}
}
