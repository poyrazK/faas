package reposcan_test

import (
	"testing"
	"testing/fstest"

	"github.com/onebox-faas/faas/pkg/reposcan"
)

func TestWorkspaceSourceHashIncludesSharedBuildInputs(t *testing.T) {
	fsys := fstest.MapFS{
		"package.json":              {Data: []byte(`{"private":true,"workspaces":["apps/*"]}`)},
		"package-lock.json":         {Data: []byte(`{"lockfileVersion":3}`)},
		"apps/web/package.json":     {Data: []byte(`{"name":"web","scripts":{"start":"node index.js"}}`)},
		"apps/web/index.js":         {Data: []byte("require('../../shared').start()")},
		"apps/backend/package.json": {Data: []byte(`{"name":"backend","scripts":{"start":"node index.js"}}`)},
		"apps/backend/index.js":     {Data: []byte("require('../../shared').start()")},
		"shared/index.js":           {Data: []byte("exports.start = () => 1")},
	}
	for _, name := range []string{"package-lock.json", "shared/index.js"} {
		t.Run(name, func(t *testing.T) {
			before, err := reposcan.Scan(fsys)
			if err != nil || len(before.Workloads) != 2 {
				t.Fatalf("scan: workloads=%d err=%v", len(before.Workloads), err)
			}
			fsys[name] = &fstest.MapFile{Data: append(append([]byte{}, fsys[name].Data...), '\n')}
			after, err := reposcan.Scan(fsys)
			if err != nil {
				t.Fatal(err)
			}
			for i, w := range before.Workloads {
				if w.SourceSHA256 == after.Workloads[i].SourceSHA256 {
					t.Fatalf("%s ignored shared input %s", w.Name, name)
				}
			}
		})
	}
}

func TestComposeSourceHashFollowsStagedContext(t *testing.T) {
	for _, dockerfile := range []bool{false, true} {
		t.Run(map[bool]string{false: "railpack", true: "dockerfile"}[dockerfile], func(t *testing.T) {
			fsys := fstest.MapFS{
				"compose.yaml":      {Data: []byte("services:\n  web:\n    build: ./web\n    ports: [\"3000:3000\"]\n")},
				"web/package.json":  {Data: []byte(`{"name":"web","scripts":{"start":"node index.js"}}`)},
				"web/index.js":      {Data: []byte("console.log('web')")},
				"package-lock.json": {Data: []byte(`{"lockfileVersion":3}`)},
			}
			if dockerfile {
				fsys["web/Dockerfile"] = &fstest.MapFile{Data: []byte("FROM node:22\nCOPY . .\nCMD [\"node\",\"index.js\"]\n")}
			}
			before, err := reposcan.Scan(fsys)
			if err != nil || len(before.Workloads) != 1 {
				t.Fatalf("scan: workloads=%d err=%v", len(before.Workloads), err)
			}
			fsys["package-lock.json"].Data = []byte(`{"lockfileVersion":3,"changed":true}`)
			after, err := reposcan.Scan(fsys)
			if err != nil {
				t.Fatal(err)
			}
			unchanged := before.Workloads[0].SourceSHA256 == after.Workloads[0].SourceSHA256
			if unchanged != dockerfile {
				t.Fatalf("root lockfile ignored=%v, narrowed Dockerfile context=%v", unchanged, dockerfile)
			}
		})
	}
}

func TestConventionSourceHashIncludesSiblingRemoval(t *testing.T) {
	fsys := fstest.MapFS{
		"apps/api/Dockerfile":    {Data: []byte("FROM alpine\nCOPY . /app\n")},
		"apps/api/index.js":      {Data: []byte("exports.handler = () => 'api'")},
		"apps/worker/Dockerfile": {Data: []byte("FROM alpine\nCOPY . /app\n")},
		"apps/worker/index.js":   {Data: []byte("exports.handler = () => 'worker'")},
	}
	before, err := reposcan.Scan(fsys)
	if err != nil || len(before.Workloads) != 2 {
		t.Fatalf("scan: %+v %v", before, err)
	}
	delete(fsys, "apps/worker/Dockerfile")
	delete(fsys, "apps/worker/index.js")
	after, err := reposcan.Scan(fsys)
	if err != nil || len(after.Workloads) != 1 {
		t.Fatalf("scan: %+v %v", after, err)
	}
	for _, workload := range before.Workloads {
		if workload.Name == after.Workloads[0].Name && workload.SourceSHA256 == after.Workloads[0].SourceSHA256 {
			t.Fatal("surviving repository-context build ignored sibling removal")
		}
	}
}
