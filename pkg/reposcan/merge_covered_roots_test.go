package reposcan

import (
	"testing"
	"testing/fstest"
)

// rc.318 field report: a Compose service whose name differs from the
// workspace package it builds became two workloads for one root, which
// blocked the project plan. The workspace package now folds into the
// service; a root two services build drops the inferred package.
func TestScanFoldsWorkspacePackageIntoComposeServiceAtSameRoot(t *testing.T) {
	workspace := map[string]*fstest.MapFile{
		"package.json":                  {Data: []byte(`{"name":"root","private":true,"workspaces":["services/*"]}`)},
		"services/backend/package.json": {Data: []byte(`{"name":"backend","scripts":{"start":"node index.js"}}`)},
		"services/backend/index.js":     {Data: []byte("console.log(1)\n")},
		"services/web/package.json":     {Data: []byte(`{"name":"web","scripts":{"start":"node index.js"}}`)},
		"services/web/index.js":         {Data: []byte("console.log(1)\n")},
	}
	for _, tc := range []struct {
		name    string
		compose string
		want    map[string]string // workload name -> root
	}{
		{
			name:    "one service",
			compose: "services:\n  api:\n    build: ./services/backend\n    ports: [\"3000:3000\"]\n",
			want:    map[string]string{"api": "services/backend", "web": "services/web"},
		},
		{
			name:    "two services from one context",
			compose: "services:\n  api:\n    build: ./services/backend\n    ports: [\"3000:3000\"]\n  jobs:\n    build: ./services/backend\n    command: node jobs.js\n",
			want:    map[string]string{"api": "services/backend", "jobs": "services/backend", "web": "services/web"},
		},
		{
			name:    "prebuilt image keeps the package",
			compose: "services:\n  api:\n    image: ghcr.io/acme/api:1\n    ports: [\"3000:3000\"]\n",
			want:    map[string]string{"api": "", "backend": "services/backend", "web": "services/web"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fsys := fstest.MapFS{"compose.yaml": {Data: []byte(tc.compose)}}
			for name, file := range workspace {
				fsys[name] = file
			}
			res, err := Scan(fsys)
			if err != nil {
				t.Fatalf("Scan: %v", err)
			}
			got := map[string]string{}
			for _, w := range res.Workloads {
				got[w.Name] = w.RootDir
			}
			if len(got) != len(tc.want) {
				t.Fatalf("workloads = %v, want %v", got, tc.want)
			}
			for name, root := range tc.want {
				if r, ok := got[name]; !ok || r != root {
					t.Fatalf("workloads = %v, want %v", got, tc.want)
				}
			}
		})
	}
}
