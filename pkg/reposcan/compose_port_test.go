// adr: 20261103112845
package reposcan

import (
	"slices"
	"testing"
	"testing/fstest"
)

// A built Compose service listens on the container side of its port
// mapping, as an image service does.
func TestComposeBuiltServiceListensOnContainerPort(t *testing.T) {
	for _, tc := range []struct {
		ports string
		want  []int
	}{
		{`["8080:3000"]`, []int{3000}},
		{`["3000"]`, []int{3000}},
		{`["127.0.0.1:9000:3000"]`, []int{3000}},
		{"\n      - target: 3000\n        published: \"8080\"", []int{3000}},
		{`["5353:53/udp", "8080:3000"]`, []int{3000}},
	} {
		result, err := Scan(fstest.MapFS{
			"compose.yaml":   {Data: []byte("services:\n  api:\n    build: ./api\n    ports: " + tc.ports + "\n")},
			"api/Dockerfile": {Data: []byte("FROM node:22\nCMD [\"node\",\"server.js\"]\n")},
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Workloads) != 1 || !slices.Equal(result.Workloads[0].Ports, tc.want) {
			t.Fatalf("ports %s: workloads = %+v, want ports %v", tc.ports, result.Workloads, tc.want)
		}
	}
}
