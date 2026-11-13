package reposcan

import (
	"strings"
	"testing"
	"testing/fstest"

	"github.com/onebox-faas/faas/pkg/gregalemanifest"
)

// A functions project scans as one function workload per function, rooted
// where `gregale deploy` uploads it from, only with the capability on
// (ADR-20261108160000).
func TestScanFunctionsProject(t *testing.T) {
	fsys := fstest.MapFS{
		"gregale.yaml": {Data: []byte(`functions:
  - name: api
    handler: src/api.mjs
  - name: digest
    root: jobs/digest
triggers:
  - kind: cron
    app: digest
    schedule: "0 8 * * *"
    path: /
`)},
		"package.json":            {Data: []byte(`{"name":"shop"}`)},
		"src/api.mjs":             {Data: []byte("export default {}\n")},
		"jobs/digest/handler.py":  {Data: []byte("config = {\"ram_mb\": 256}\ndef handler(event, ctx): pass\n")},
		"jobs/digest/README.md":   {Data: []byte("digest")},
		"services/web/Dockerfile": {Data: []byte("FROM scratch\n")},
	}
	got, err := ScanWithOptions(fsys, ScanOptions{Functions: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Workloads) != 2 {
		t.Fatalf("workloads = %+v, want the two functions only", got.Workloads)
	}
	api, digest := got.Workloads[0], got.Workloads[1]
	if api.Name != "api" || api.RootDir != "" || api.Function == nil || api.Function.Runtime != "node22" || api.Function.Handler != "src/api.mjs" {
		t.Errorf("api = %+v (function %+v), want a node22 function at the root with handler src/api.mjs", api, api.Function)
	}
	if digest.Name != "digest" || digest.RootDir != "jobs/digest" || digest.Function == nil || digest.Function.Runtime != "python312" || digest.Function.Handler != gregalemanifest.DefaultFunctionHandler {
		t.Errorf("digest = %+v (function %+v), want a python312 function rooted at jobs/digest", digest, digest.Function)
	}
	if len(digest.Schedules) != 1 || digest.Schedules[0].Expression != "0 8 * * *" || digest.Schedule != "0 8 * * *" {
		t.Errorf("digest schedules = %+v, want the cron from gregale.yaml", digest.Schedules)
	}
	if api.DetectedBy.Detector != "functions" || api.SourceSHA256 == "" {
		t.Errorf("api detection/hash = %+v %q", api.DetectedBy, api.SourceSHA256)
	}

	off, err := ScanWithOptions(fsys, ScanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range off.Workloads {
		if w.Function != nil {
			t.Fatalf("without the capability no function workloads: %+v", off.Workloads)
		}
	}
}

func TestScanFunctionsProjectDiscoveredFromCode(t *testing.T) {
	fsys := fstest.MapFS{
		"functions/hello/handler.js": {Data: []byte("export const config = { cron: \"*/5 * * * *\" };\nexport default {};\n")},
		"functions/plain/handler.js": {Data: []byte("export default {};\n")},
	}
	got, err := ScanWithOptions(fsys, ScanOptions{Functions: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Workloads) != 1 || got.Workloads[0].Name != "hello" || got.Workloads[0].RootDir != "functions/hello" || got.Workloads[0].Schedule != "*/5 * * * *" {
		t.Fatalf("workloads = %+v, want only the function that declares a config", got.Workloads)
	}
}

func TestScanFunctionsProjectRefusals(t *testing.T) {
	fsys := fstest.MapFS{
		"gregale.yaml": {Data: []byte("functions:\n  - name: api\n    handler: src/missing.mjs\n")},
	}
	_, err := ScanWithOptions(fsys, ScanOptions{Functions: true})
	if err == nil || !strings.Contains(err.Error(), "is not a file") {
		t.Fatalf("err = %v, want the missing handler named", err)
	}
	plain := fstest.MapFS{"package.json": {Data: []byte(`{"name":"x","scripts":{"start":"node a.js"}}`)}}
	res, err := ScanWithOptions(plain, ScanOptions{Functions: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range res.Workloads {
		if w.Function != nil {
			t.Fatalf("a repository without functions scans as before: %+v", res.Workloads)
		}
	}
}
