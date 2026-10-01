package reposcan

import (
	"testing"
	"testing/fstest"
)

func TestComputeRegionManifestDeclarations(t *testing.T) {
	for _, tc := range []struct {
		name, file, body, want string
		invalid                bool
	}{
		{name: "compose", file: "compose.yaml", body: "services:\n  customer-api:\n    build: .\n    x-gregale-region: eu-fsn\n", want: "eu-fsn"},
		{name: "service", file: "app.yaml", body: "services:\n  customer-api:\n    command: serve\n    compute_region: eu-hel\n", want: "eu-hel"},
		{name: "worker", file: "app.yaml", body: "workers:\n  customer-worker:\n    command: work\n    compute_region: eu-fsn\n", want: "eu-fsn"},
		{name: "job", file: "app.yaml", body: "jobs:\n  - name: customer-job\n    command: run\n    compute_region: eu-fsn\n", want: "eu-fsn"},
		{name: "invalid", file: "compose.yaml", body: "services:\n  customer-api:\n    build: .\n    x-gregale-region: EU-fsn\n", invalid: true},
		{name: "managed resource", file: "compose.yaml", body: "services:\n  database:\n    image: postgres:16\n    x-gregale-region: eu-fsn\n", invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := Scan(fstest.MapFS{tc.file: &fstest.MapFile{Data: []byte(tc.body)}})
			if tc.invalid {
				if err == nil {
					t.Fatal("accepted invalid declaration")
				}
				return
			}
			if err != nil || len(result.Workloads) != 1 || result.Workloads[0].ComputeRegion != tc.want {
				t.Fatalf("workloads=%+v err=%v", result.Workloads, err)
			}
		})
	}
}
